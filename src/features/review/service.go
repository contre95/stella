package review

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/contre95/stella/src/srs"
	"github.com/google/uuid"
)

// Stats are the aggregate counts shown on the dashboard.
type Stats struct {
	DueToday    int
	TotalCards  int
	StudyCards  int // active cards in study-enabled decks
	NewCards    int // non-memorised cards in study-enabled decks
	UnseenCards int // never-reviewed cards in study-enabled decks (subset of NewCards)
	DeckCount   int
}

// CardInSession pairs a card with its parent fact and session metadata.
type CardInSession struct {
	Card      *srs.Card
	Fact      *srs.Fact
	Remaining int
	DeckName  string
	DeckColor string
	DeckImage string
	// DueLabel is a short badge describing when the card is due: "New",
	// "Learning", "Today", "Tomorrow", "In 3 days", "2 days overdue". DueTone
	// classifies it for styling: new, learning, today, future, overdue.
	DueLabel string
	DueTone  string
	// Previews holds, per grade 0-5, the interval that grade would schedule,
	// as a human label ("now", "in 3 days"). Computed with a dry run.
	Previews [6]string
}

// session is one user's review-queue state: an exact port of Mnemosyne's
// SM2Mnemosyne scheduler state (see reset/rebuildQueue/popNextID). Each user
// studies an independent queue.
type session struct {
	// cardIDsInQueue is the review queue. It may contain the same card twice:
	// grade-0 cards are queued double so they get re-tested within the session.
	cardIDsInQueue []string
	// factIDsInQueue keeps sister cards from entering the queue together.
	factIDsInQueue map[string]struct{}
	// factIDsMemorised are facts memorised today; it persists across queue
	// rebuilds and stops stage 4 from pulling a sister card out of the unseen
	// pile on the same day. Seeded from the review log on reset.
	factIDsMemorised map[string]struct{}
	// cardIDLast avoids showing the same card twice in a row.
	cardIDLast string
	// stage remembers where queue building left off, skipping useless queries.
	stage int
	// sessionKey tracks the deck filter + learn-ahead flag the queue was built
	// for, and dayStart the study day; the queue resets when either changes
	// (Mnemosyne resets on programme start, which a long-running server lacks).
	sessionKey string
	dayStart   int64
}

// Service drives the review loop: queue selection, grading, and aggregate
// stats. Per-user sessions are guarded by mu.
type Service struct {
	repo      srs.Repository
	scheduler *srs.Scheduler
	shuffle   func(n int, swap func(i, j int)) // injectable for deterministic tests

	mu       sync.Mutex
	sessions map[string]*session // keyed by user ID
}

// NewService constructs the review service.
func NewService(repo srs.Repository, scheduler *srs.Scheduler) *Service {
	return &Service{repo: repo, scheduler: scheduler, shuffle: rand.Shuffle, sessions: map[string]*session{}}
}

// session returns the user's queue state, creating a fresh (unbuilt) one on
// first use. Callers must hold s.mu.
func (s *Service) session(userID string) *session {
	sess, ok := s.sessions[userID]
	if !ok {
		sess = &session{stage: -1}
		s.sessions[userID] = sess
	}
	return sess
}

// NextCard returns the next card to review, or nil when the queue is empty.
// deckIDs restricts the session to those decks; nil means all decks.
// learnAhead allows serving future-scheduled cards once nothing else is left.
// Selection is a port of Mnemosyne's queue: see rebuildQueue and popNextID.
func (s *Service) NextCard(ctx context.Context, userID string, deckIDs []string, learnAhead bool) (*CardInSession, error) {
	now := s.scheduler.AdjustedNow(s.scheduler.Now())

	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(userID)
	if err := s.ensureSession(ctx, userID, sess, deckIDs, learnAhead); err != nil {
		return nil, err
	}

	var card *srs.Card
	for card == nil {
		id, ok, err := s.popNextID(ctx, userID, sess, deckIDs, learnAhead)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		c, err := s.repo.GetCard(ctx, userID, id)
		switch {
		case errors.Is(err, srs.ErrNotFound):
			// Deleted mid-session. Mnemosyne removes deleted cards from the
			// queue eagerly; serving from a database re-query, we drop them
			// lazily when popped.
			sess.dropFromQueue(id)
		case err != nil:
			return nil, err
		case !c.Active:
			sess.dropFromQueue(id)
		default:
			card = c
		}
	}

	return s.buildCardInSession(ctx, userID, card, now, deckIDs)
}

// buildCardInSession assembles the fact, deck, due-badge, and grade-preview
// metadata that goes alongside a queue card. Shared by NextCard and CardByID.
func (s *Service) buildCardInSession(ctx context.Context, userID string, card *srs.Card, now int64, deckIDs []string) (*CardInSession, error) {
	fact, err := s.repo.GetFact(ctx, userID, card.FactID)
	if err != nil {
		return nil, err
	}

	remaining, err := s.queueSize(ctx, userID, now, deckIDs)
	if err != nil {
		return nil, err
	}

	dn, dc, di := "", "", ""
	if d, err := s.repo.GetDeck(ctx, userID, card.DeckID); err == nil && d != nil {
		dn, dc, di = d.Name, d.Color, d.Image
	}

	ci := &CardInSession{Card: card, Fact: fact, Remaining: remaining, DeckName: dn, DeckColor: dc, DeckImage: di}
	ci.DueLabel, ci.DueTone = dueBadge(s.scheduler, card, now)
	for g := 0; g <= 5; g++ {
		ci.Previews[g] = intervalLabel(s.scheduler.GradeAnswer(card, g, true))
	}
	return ci, nil
}

// CardByID re-fetches a specific card's current session view without
// touching the review queue — used to refresh the on-screen card after its
// fact is edited mid-review, so editing doesn't consume a turn.
func (s *Service) CardByID(ctx context.Context, userID, cardID string, deckIDs []string) (*CardInSession, error) {
	now := s.scheduler.AdjustedNow(s.scheduler.Now())
	card, err := s.repo.GetCard(ctx, userID, cardID)
	if err != nil {
		return nil, err
	}
	return s.buildCardInSession(ctx, userID, card, now, deckIDs)
}

// dueBadge describes when a card is due, as a short label plus a tone used for
// styling. Unseen cards read "New" and in-acquisition (grade 0/1) cards read
// "Learning" — neither has a scheduled day. Retention cards borrow the
// scheduler's next-rep phrasing ("Today", "Tomorrow", "In 3 days", "2 days
// overdue"), with the tone split by whether they are future, due today, or
// overdue (mirroring the scheduler's own on-time/late boundary at now-DAY).
func dueBadge(sch *srs.Scheduler, card *srs.Card, now int64) (label, tone string) {
	switch {
	case card.Grade == srs.GradeUnseen:
		return "New", "new"
	case card.Grade < 2:
		return "Learning", "learning"
	}
	label = capitalize(sch.NextRepToIntervalString(card.NextRep, now))
	switch {
	case card.NextRep > now:
		tone = "future"
	case card.NextRep <= now-srs.DAY:
		tone = "overdue"
	default:
		tone = "today"
	}
	return label, tone
}

// capitalize upper-cases the first character of an ASCII label.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// intervalLabel renders a dry-run interval as a short human label for the
// grade-button tooltips.
func intervalLabel(interval int64) string {
	days := interval / srs.DAY
	switch {
	case days <= 0:
		return "again now"
	case days == 1:
		return "in 1 day"
	default:
		return fmt.Sprintf("in %d days", days)
	}
}

// Grade applies a grade to a card, persists the updated scheduling state, and
// logs the review event. It also performs the queue bookkeeping Mnemosyne's
// grade_answer does: remembering memorised facts, dropping the duplicate queue
// copy of a graduating grade-0 card, and shifting next_rep by whole days while
// a sister card is scheduled on the same day (avoid_sister_cards).
func (s *Service) Grade(ctx context.Context, userID, cardID string, grade int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(userID)

	card, err := s.repo.GetCard(ctx, userID, cardID)
	if err != nil {
		return err
	}

	oldGrade := card.Grade
	scheduledInterval := card.NextRep - card.LastRep
	nowUnix := s.scheduler.Now().Unix()
	actualInterval := nowUnix - card.LastRep
	if oldGrade == srs.GradeUnseen {
		actualInterval = 0
	}

	if oldGrade < 2 && grade >= 2 {
		// Memorising a card keeps its sisters out of the unseen pile today.
		if sess.factIDsMemorised != nil {
			sess.factIDsMemorised[card.FactID] = struct{}{}
		}
		// A grade-0 card sits in the queue twice; make sure the second copy
		// doesn't show up again now that the card graduated.
		if oldGrade == 0 {
			for i, qid := range sess.cardIDsInQueue {
				if qid == card.ID {
					sess.cardIDsInQueue = append(sess.cardIDsInQueue[:i], sess.cardIDsInQueue[i+1:]...)
					break
				}
			}
		}
	}

	s.scheduler.GradeAnswer(card, grade, false)

	if grade >= 2 {
		for {
			count, err := s.repo.SisterCardCountScheduledBetween(
				ctx, userID, card.FactID, card.ID, card.NextRep, card.NextRep+srs.DAY)
			if err != nil {
				return err
			}
			if count == 0 {
				break
			}
			card.NextRep += srs.DAY
		}
	}

	if err := s.repo.UpdateCard(ctx, card); err != nil {
		return err
	}

	return s.repo.AddReview(ctx, &srs.Review{
		ID:                uuid.NewString(),
		UserID:            userID,
		CardID:            card.ID,
		Grade:             grade,
		RetReps:           card.RetReps,
		ScheduledInterval: scheduledInterval,
		ActualInterval:    actualInterval,
		ThinkingTime:      0,
		Timestamp:         nowUnix,
	})
}

// Stats returns aggregate counts for the dashboard (across all decks).
func (s *Service) Stats(ctx context.Context, userID string) (*Stats, error) {
	now := s.scheduler.AdjustedNow(s.scheduler.Now())
	enabledIDs, err := s.repo.ListStudyEnabledDeckIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	due, err := s.repo.ScheduledCount(ctx, userID, now, enabledIDs)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.ActiveCount(ctx, userID)
	if err != nil {
		return nil, err
	}
	studyCards, err := s.repo.FactCountForDecks(ctx, userID, enabledIDs)
	if err != nil {
		return nil, err
	}
	newCards, err := s.repo.NonMemorisedCount(ctx, userID, enabledIDs)
	if err != nil {
		return nil, err
	}
	unseenCards, err := s.repo.UnseenCount(ctx, userID, enabledIDs)
	if err != nil {
		return nil, err
	}
	decks, err := s.repo.ListDecks(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &Stats{DueToday: due, TotalCards: total, StudyCards: studyCards, NewCards: newCards, UnseenCards: unseenCards, DeckCount: len(decks)}, nil
}

// GroupSlice is one wedge of a per-group pie chart: a group's share of a
// dashboard metric, plus the color it's drawn in.
type GroupSlice struct {
	Label   string
	Count   int
	Percent float64
	Color   string
}

// GroupPieChart is a metric broken down by group, pre-rendered as a CSS
// conic-gradient string so the template needs no JS to draw it. Zero value
// (Total == 0) means there's nothing to chart.
type GroupPieChart struct {
	Total    int
	Slices   []GroupSlice
	Gradient template.CSS
}

// GroupBreakdown holds the per-group pie charts shown on the dashboard:
// due-today, studying, and not-memorised, each split by group. Studying also
// has a StudyingWithRest variant that adds a grey slice for every other
// active card, so the "Studying" chart can be toggled between "share of
// what's studying" and "share of your whole collection".
type GroupBreakdown struct {
	DueToday         GroupPieChart
	Studying         GroupPieChart
	StudyingWithRest GroupPieChart
	NotMemorized     GroupPieChart
}

// groupPalette assigns a stable, distinguishable color to each group by
// list order; it cycles if there are more groups than colors.
var groupPalette = []string{
	"#ec4899", "#f97316", "#eab308", "#22c55e",
	"#06b6d4", "#6366f1", "#a855f7", "#f43f5e",
	"#84cc16", "#0ea5e9",
}

// ungroupedColor marks the synthetic "Ungrouped" bucket distinctly from any
// real group, regardless of palette cycling.
const ungroupedColor = "#9ca3af"

// notStudyingColor marks the "rest of your cards" slice added to
// StudyingWithRest — cards outside the study-enabled breakdown entirely, so
// it stays visually distinct from ungroupedColor (a real, studying bucket).
const notStudyingColor = "#d1d5db"

// GroupBreakdown computes the due-today, studying, and not-memorized totals
// scoped to each group's study-enabled decks, for the dashboard pie charts.
func (s *Service) GroupBreakdown(ctx context.Context, userID string) (*GroupBreakdown, error) {
	now := s.scheduler.AdjustedNow(s.scheduler.Now())
	groups, err := s.repo.ListGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	decks, err := s.repo.ListDecks(ctx, userID)
	if err != nil {
		return nil, err
	}
	totalActive, err := s.repo.ActiveCount(ctx, userID)
	if err != nil {
		return nil, err
	}

	byGroup := make(map[string][]string)
	for _, d := range decks {
		if d.StudyEnabled {
			byGroup[d.GroupID] = append(byGroup[d.GroupID], d.ID)
		}
	}

	type bucket struct {
		label string
		color string
		ids   []string
	}
	buckets := make([]bucket, 0, len(groups)+1)
	for i, g := range groups {
		buckets = append(buckets, bucket{label: g.Name, color: groupPalette[i%len(groupPalette)], ids: byGroup[g.ID]})
	}
	if ids := byGroup[""]; len(ids) > 0 {
		buckets = append(buckets, bucket{label: "Ungrouped", color: ungroupedColor, ids: ids})
	}

	due := make([]GroupSlice, 0, len(buckets))
	studying := make([]GroupSlice, 0, len(buckets))
	notMemorized := make([]GroupSlice, 0, len(buckets))

	for _, b := range buckets {
		if len(b.ids) == 0 {
			continue
		}
		dueCount, err := s.repo.ScheduledCount(ctx, userID, now, b.ids)
		if err != nil {
			return nil, err
		}
		if dueCount > 0 {
			due = append(due, GroupSlice{Label: b.label, Count: dueCount, Color: b.color})
		}
		studyCount, err := s.repo.FactCountForDecks(ctx, userID, b.ids)
		if err != nil {
			return nil, err
		}
		if studyCount > 0 {
			studying = append(studying, GroupSlice{Label: b.label, Count: studyCount, Color: b.color})
		}
		newCount, err := s.repo.NonMemorisedCount(ctx, userID, b.ids)
		if err != nil {
			return nil, err
		}
		if newCount > 0 {
			notMemorized = append(notMemorized, GroupSlice{Label: b.label, Count: newCount, Color: b.color})
		}
	}

	studyingTotal := 0
	for _, sl := range studying {
		studyingTotal += sl.Count
	}
	studyingWithRest := append([]GroupSlice(nil), studying...)
	if rest := totalActive - studyingTotal; rest > 0 {
		studyingWithRest = append(studyingWithRest, GroupSlice{Label: "Not studying", Count: rest, Color: notStudyingColor})
	}

	return &GroupBreakdown{
		DueToday:         buildPieChart(due),
		Studying:         buildPieChart(studying),
		StudyingWithRest: buildPieChart(studyingWithRest),
		NotMemorized:     buildPieChart(notMemorized),
	}, nil
}

// buildPieChart sorts slices largest-first, fills in each Percent, and
// renders the whole set as a single conic-gradient() CSS value.
func buildPieChart(slices []GroupSlice) GroupPieChart {
	total := 0
	for _, sl := range slices {
		total += sl.Count
	}
	if total == 0 {
		return GroupPieChart{}
	}
	sort.Slice(slices, func(i, j int) bool { return slices[i].Count > slices[j].Count })

	parts := make([]string, 0, len(slices))
	cum := 0
	for i := range slices {
		slices[i].Percent = float64(slices[i].Count) / float64(total) * 100
		start := float64(cum) / float64(total) * 100
		cum += slices[i].Count
		end := float64(cum) / float64(total) * 100
		parts = append(parts, fmt.Sprintf("%s %.3f%% %.3f%%", slices[i].Color, start, end))
	}
	return GroupPieChart{
		Total:    total,
		Slices:   slices,
		Gradient: template.CSS("conic-gradient(" + strings.Join(parts, ", ") + ")"),
	}
}

// ForecastDay is one bar in the upcoming-reviews chart: how many cards fall
// due on that day, plus a 0–100 height relative to the busiest day.
type ForecastDay struct {
	Label   string
	Count   int
	Percent int
}

// Forecast returns the number of cards due today and on each of the following
// days (across all decks), for the dashboard's upcoming-reviews chart. Today's
// bucket includes anything already due or overdue; later buckets group retention
// cards by their scheduled day. days must be >= 1.
func (s *Service) Forecast(ctx context.Context, userID string, days int) ([]ForecastDay, error) {
	if days < 1 {
		days = 1
	}
	now := s.scheduler.AdjustedNow(s.scheduler.Now())

	enabledIDs, err := s.repo.ListStudyEnabledDeckIDs(ctx, userID)
	if err != nil {
		return nil, err
	}

	counts := make([]int, days)
	due, err := s.repo.ScheduledCount(ctx, userID, now, enabledIDs)
	if err != nil {
		return nil, err
	}
	counts[0] = due

	// All retention cards scheduled in the future, bucketed by day offset.
	future, err := s.repo.CardsLearnAhead(ctx, userID, now, "next_rep", -1, enabledIDs)
	if err != nil {
		return nil, err
	}
	for _, c := range future {
		day := int((c.NextRep-now)/srs.DAY) + 1
		if day >= 1 && day < days {
			counts[day]++
		}
	}

	max := 1
	for _, n := range counts {
		if n > max {
			max = n
		}
	}

	wall := s.scheduler.Now()
	out := make([]ForecastDay, days)
	for i := range counts {
		out[i] = ForecastDay{
			Label:   forecastLabel(i, wall),
			Count:   counts[i],
			Percent: counts[i] * 100 / max,
		}
	}
	return out, nil
}

func forecastLabel(i int, now time.Time) string {
	if i == 0 {
		return "Today"
	}
	return now.AddDate(0, 0, i).Format("Mon")
}

// HeatCell is one day square in the study-activity heatmap.
type HeatCell struct {
	Level int    // 0 (none) .. 4 (most) intensity
	Count int    // reviews that day
	Title string // tooltip
	Today bool
	Empty bool // padding cell for a future day; rendered blank
}

// Heatmap is a GitHub-style contribution grid of study activity.
type Heatmap struct {
	Weeks         [][]HeatCell // week columns, each 7 cells (Sun..Sat)
	MonthLabels   []string     // one per week column, "" unless the month starts there
	TotalDays     int          // days with at least one review, within the window
	TotalReviews  int          // reviews within the window
	CurrentStreak int          // consecutive study days ending today
}

const heatmapWeeks = 53

// StudyHeatmap builds a ~year-long study-activity heatmap from the review log,
// bucketing each review into a study day (honouring day_starts_at, so a late
// session before the day boundary counts toward the previous day).
func (s *Service) StudyHeatmap(ctx context.Context, userID string) (*Heatmap, error) {
	loc := s.scheduler.Loc
	if loc == nil {
		loc = time.Local
	}
	dayStarts := s.scheduler.Params.DayStartsAt

	today := studyDay(s.scheduler.Now().Unix(), loc, dayStarts)
	startOfWeek := today.AddDate(0, 0, -int(today.Weekday())) // back to Sunday
	gridStart := startOfWeek.AddDate(0, 0, -(heatmapWeeks-1)*7)

	timestamps, err := s.repo.ReviewTimestampsSince(ctx, userID, gridStart.Unix()-srs.DAY)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(timestamps))
	for _, ts := range timestamps {
		counts[studyDay(ts, loc, dayStarts).Format("2006-01-02")]++
	}

	hm := &Heatmap{
		Weeks:       make([][]HeatCell, heatmapWeeks),
		MonthLabels: make([]string, heatmapWeeks),
	}
	prevMonth := time.Month(0)
	cur := gridStart
	for w := 0; w < heatmapWeeks; w++ {
		if m := cur.Month(); m != prevMonth {
			hm.MonthLabels[w] = cur.Format("Jan")
			prevMonth = m
		}
		week := make([]HeatCell, 7)
		for d := 0; d < 7; d++ {
			var cell HeatCell
			if cur.After(today) {
				cell.Empty = true
			} else {
				n := counts[cur.Format("2006-01-02")]
				cell.Count = n
				cell.Level = heatLevel(n)
				cell.Today = cur.Equal(today)
				cell.Title = heatTitle(n, cur)
				if n > 0 {
					hm.TotalDays++
					hm.TotalReviews += n
				}
			}
			week[d] = cell
			cur = cur.AddDate(0, 0, 1)
		}
		hm.Weeks[w] = week
	}

	// The leading partial month may span only 1–2 columns; dropping its label
	// prevents it from visually overlapping the next month label.
	if hm.MonthLabels[1] != "" || (len(hm.MonthLabels) > 2 && hm.MonthLabels[2] != "") {
		hm.MonthLabels[0] = ""
	}

	// Current streak: consecutive study days ending today. Today counting as
	// blank does not break it (you may not have studied yet).
	d := today
	if counts[d.Format("2006-01-02")] == 0 {
		d = d.AddDate(0, 0, -1)
	}
	for counts[d.Format("2006-01-02")] > 0 {
		hm.CurrentStreak++
		d = d.AddDate(0, 0, -1)
	}

	return hm, nil
}

// studyDay maps a POSIX timestamp to midnight (local) of its study day.
func studyDay(unix int64, loc *time.Location, dayStarts int) time.Time {
	t := time.Unix(unix-int64(dayStarts)*srs.HOUR, 0).In(loc)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

func heatLevel(n int) int {
	switch {
	case n <= 0:
		return 0
	case n <= 3:
		return 1
	case n <= 7:
		return 2
	case n <= 14:
		return 3
	default:
		return 4
	}
}

func heatTitle(n int, day time.Time) string {
	date := day.Format("Mon, Jan 2")
	switch n {
	case 0:
		return "No reviews · " + date
	case 1:
		return "1 review · " + date
	default:
		return fmt.Sprintf("%d reviews · %s", n, date)
	}
}

// reset re-initialises the user's queue state; port of SM2Mnemosyne.reset
// (always with new_only=False, a mode Stella does not expose). Mnemosyne runs
// it at programme start; the server-side equivalents are a session change
// (deck filter or learn-ahead flag) and a study-day rollover — see
// ensureSession.
func (s *Service) reset(ctx context.Context, userID string, sess *session) error {
	sess.cardIDsInQueue = nil
	sess.factIDsInQueue = map[string]struct{}{}
	sess.factIDsMemorised = map[string]struct{}{}
	sess.cardIDLast = ""
	sess.stage = 1
	from, to := s.scheduler.TodayStartAndEnd()
	factIDs, err := s.repo.FactIDsLearnedToday(ctx, userID, from, to)
	if err != nil {
		return err
	}
	for _, id := range factIDs {
		sess.factIDsMemorised[id] = struct{}{}
	}
	sess.dayStart = from
	return nil
}

// ensureSession resets the queue when it was built for a different deck
// filter or learn-ahead mode, or for a previous study day. Callers must hold
// s.mu.
func (s *Service) ensureSession(ctx context.Context, userID string, sess *session, deckIDs []string, learnAhead bool) error {
	key := strings.Join(deckIDs, ",")
	if learnAhead {
		key += "|learnahead"
	}
	dayStart, _ := s.scheduler.TodayStartAndEnd()
	if sess.stage < 1 || key != sess.sessionKey || dayStart != sess.dayStart {
		if err := s.reset(ctx, userID, sess); err != nil {
			return err
		}
		sess.sessionKey = key
	}
	return nil
}

// rebuildQueue refills the card queue. It is an exact port of
// SM2Mnemosyne.rebuild_queue and must be kept in step with it, including the
// stage bookkeeping that lets later rebuilds skip stages known to be empty.
// Callers must hold s.mu.
func (s *Service) rebuildQueue(ctx context.Context, userID string, sess *session, deckIDs []string, learnAhead bool) error {
	now := s.scheduler.AdjustedNow(s.scheduler.Now())
	sess.cardIDsInQueue = sess.cardIDsInQueue[:0]
	sess.factIDsInQueue = map[string]struct{}{}

	// enqueue adds a card; copies is 2 for grade-0 cards, which are queued
	// double so they are re-tested later in the same session.
	enqueue := func(c *srs.Card, copies int) {
		for range copies {
			sess.cardIDsInQueue = append(sess.cardIDsInQueue, c.ID)
		}
		sess.factIDsInQueue[c.FactID] = struct{}{}
	}
	shuffleQueue := func() {
		s.shuffle(len(sess.cardIDsInQueue), func(i, j int) {
			sess.cardIDsInQueue[i], sess.cardIDsInQueue[j] = sess.cardIDsInQueue[j], sess.cardIDsInQueue[i]
		})
	}

	// Stage 1
	//
	// Cards scheduled for today or overdue, shortest interval first (being a
	// day late on an interval of 2 is much worse than on one of 50). Fetch at
	// most 50 at a time as a memory/re-query trade-off.
	if sess.stage == 1 {
		sortKey := "interval"
		if s.scheduler.Params.RandomiseScheduledCards {
			sortKey = "random"
		}
		cards, err := s.repo.CardsDueForRetRep(ctx, userID, now, sortKey, 50, deckIDs)
		if err != nil {
			return err
		}
		for _, c := range cards {
			enqueue(c, 1)
		}
		if len(sess.cardIDsInQueue) > 0 {
			return nil
		}
		sess.stage = 2
	}

	// Stages 2-4 concentrate on a limited number of non-memorised cards, to
	// avoid too-long intervals between repetitions of the cards in hand.
	limit := s.scheduler.Params.NonMemorisedCardsInHand
	nonMemorisedInQueue := 0
	// addNonMemorised is the shared stage 2/3 loop body: skip sister cards of
	// facts already queued, and stop filling once the hand is full.
	addNonMemorised := func(cards []*srs.Card, copies int) {
		for _, c := range cards {
			if _, in := sess.factIDsInQueue[c.FactID]; in {
				continue
			}
			if nonMemorisedInQueue < limit {
				enqueue(c, copies)
				nonMemorisedInQueue++
			}
			if nonMemorisedInQueue == limit {
				return
			}
		}
	}

	// Stage 2
	//
	// Rememorise the cards we got wrong (lapsed retention cards).
	if sess.stage == 2 {
		relearn1, err := s.repo.CardsToRelearn(ctx, userID, 1, "-interval", deckIDs)
		if err != nil {
			return err
		}
		addNonMemorised(relearn1, 1)
		relearn0, err := s.repo.CardsToRelearn(ctx, userID, 0, "-interval", deckIDs)
		if err != nil {
			return err
		}
		addNonMemorised(relearn0, 2)
		shuffleQueue()
		// Only stop when the hand is full; otherwise keep going to add some
		// extra cards for more spread.
		if nonMemorisedInQueue == limit {
			return nil
		}
		if len(sess.cardIDsInQueue) == 0 {
			sess.stage = 3
		}
	}

	// Stage 3
	//
	// Cards never yet committed to long-term memory, but seen before. Uses <=
	// so earlier stages can top up the hand from this stage.
	if sess.stage <= 3 {
		memorising1, err := s.repo.CardsNewMemorising(ctx, userID, 1, deckIDs)
		if err != nil {
			return err
		}
		addNonMemorised(memorising1, 1)
		memorising0, err := s.repo.CardsNewMemorising(ctx, userID, 0, deckIDs)
		if err != nil {
			return err
		}
		addNonMemorised(memorising0, 2)
		shuffleQueue()
		if nonMemorisedInQueue == limit {
			return nil
		}
		if len(sess.cardIDsInQueue) == 0 {
			sess.stage = 4
		}
	}

	// Stage 4
	//
	// Introduce cards seen for the first time, preferentially keeping away
	// from sister cards of facts memorised today for as long as possible.
	if sess.stage <= 4 {
		sortKey := ""
		if s.scheduler.Params.RandomiseNewCards {
			sortKey = "random"
		}
		unseen, err := s.repo.CardsUnseen(ctx, userID, sortKey, min(limit, 50), deckIDs)
		if err != nil {
			return err
		}
		for _, c := range unseen {
			_, inQueue := sess.factIDsInQueue[c.FactID]
			_, memorised := sess.factIDsMemorised[c.FactID]
			if inQueue || memorised {
				continue
			}
			enqueue(c, 1)
			nonMemorisedInQueue++
			if nonMemorisedInQueue == limit {
				sess.stage = 2
				return nil
			}
		}
		// If the hand is not full enough, start pulling in sister cards.
		if nonMemorisedInQueue < limit {
			unseen, err = s.repo.CardsUnseen(ctx, userID, sortKey, min(limit, 50), deckIDs)
			if err != nil {
				return err
			}
			for _, c := range unseen {
				if _, in := sess.factIDsInQueue[c.FactID]; in {
					continue
				}
				enqueue(c, 1)
				nonMemorisedInQueue++
				if nonMemorisedInQueue == limit {
					sess.stage = 2
					return nil
				}
			}
		}
		if len(sess.cardIDsInQueue) == 0 {
			sess.stage = 5
		}
	}

	// Stage 5
	//
	// No more scheduled or new cards; serve future-scheduled cards when the
	// user asked to learn ahead. Not shuffled — reviewing the earliest
	// scheduled cards first is more useful. Like Mnemosyne, this block is not
	// gated on the stage so a partially filled hand can still learn ahead.
	if !learnAhead {
		sess.stage = 2
		return nil
	}
	ahead, err := s.repo.CardsLearnAhead(ctx, userID, now, "next_rep", 50, deckIDs)
	if err != nil {
		return err
	}
	for _, c := range ahead {
		sess.cardIDsInQueue = append(sess.cardIDsInQueue, c.ID)
	}
	// Relearn cards we get wrong during learn ahead.
	sess.stage = 2
	return nil
}

// popNextID pops the id of the next card to show, rebuilding the queue when
// it runs empty and making sure the same card is never shown twice in
// succession. Port of SM2Mnemosyne.next_card; ok is false when there is
// nothing left to review. Callers must hold s.mu.
func (s *Service) popNextID(ctx context.Context, userID string, sess *session, deckIDs []string, learnAhead bool) (id string, ok bool, err error) {
	pop := func() string {
		id := sess.cardIDsInQueue[0]
		sess.cardIDsInQueue = sess.cardIDsInQueue[1:]
		return id
	}
	if len(sess.cardIDsInQueue) == 0 {
		if err := s.rebuildQueue(ctx, userID, sess, deckIDs, learnAhead); err != nil {
			return "", false, err
		}
		if len(sess.cardIDsInQueue) == 0 {
			return "", false, nil
		}
	}
	id = pop()
	if sess.cardIDLast != "" {
		for id == sess.cardIDLast {
			// Make sure we have enough cards to vary, but exit in hopeless
			// situations where the just-shown card is the only one left.
			if len(sess.cardIDsInQueue) == 0 {
				if err := s.rebuildQueue(ctx, userID, sess, deckIDs, learnAhead); err != nil {
					return "", false, err
				}
				if len(sess.cardIDsInQueue) == 0 {
					return "", false, nil
				}
				if sess.onlyCardInQueue(id) {
					return id, true, nil
				}
			}
			id = pop()
		}
	}
	sess.cardIDLast = id
	return id, true, nil
}

// onlyCardInQueue reports whether the (non-empty) queue consists solely of
// copies of the given card.
func (sess *session) onlyCardInQueue(id string) bool {
	for _, qid := range sess.cardIDsInQueue {
		if qid != id {
			return false
		}
	}
	return true
}

// dropFromQueue removes every queued copy of a card that turned out to be
// deleted or suspended. Callers must hold the service's mu.
func (sess *session) dropFromQueue(id string) {
	out := sess.cardIDsInQueue[:0]
	for _, qid := range sess.cardIDsInQueue {
		if qid != id {
			out = append(out, qid)
		}
	}
	sess.cardIDsInQueue = out
}

// EnabledDeckIDs returns the IDs of all decks the user has marked for study.
// The review handler uses this to populate the default queue when no explicit
// deck filter is provided.
func (s *Service) EnabledDeckIDs(ctx context.Context, userID string) ([]string, error) {
	return s.repo.ListStudyEnabledDeckIDs(ctx, userID)
}

// DeckSummary is a minimal per-deck view used to break down a group/tag's
// decks by study status in the study picker.
type DeckSummary struct {
	Name         string
	StudyEnabled bool
}

// sortStudyFirst stable-sorts decks so study-enabled ones come first,
// keeping their relative order otherwise — the picker lists "in study"
// decks before greyed-out ones.
func sortStudyFirst(decks []DeckSummary) {
	sort.SliceStable(decks, func(i, j int) bool { return decks[i].StudyEnabled && !decks[j].StudyEnabled })
}

// GroupForPicker is one entry in the pre-session group selector.
type GroupForPicker struct {
	Group      *srs.Group // nil for the "ungrouped" synthetic entry
	DeckIDs    string     // comma-joined IDs of study-enabled decks, used as the ?decks= param
	TotalCount int        // total decks in the group
	StudyCount int        // decks in the group with StudyEnabled=true
	Decks      []DeckSummary
}

// GroupsForPicker returns named groups and an optional ungrouped entry, each
// paired with the IDs of their study-enabled decks. Used by the study-picker
// screen, which studies only "in study" decks when a group is picked.
func (s *Service) GroupsForPicker(ctx context.Context, userID string) (named []GroupForPicker, ungrouped *GroupForPicker, err error) {
	groups, err := s.repo.ListGroups(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	decks, err := s.repo.ListDecks(ctx, userID)
	if err != nil {
		return nil, nil, err
	}

	byGroupTotal := make(map[string]int)
	byGroupStudy := make(map[string][]string)
	byGroupDecks := make(map[string][]DeckSummary)
	for _, d := range decks {
		byGroupTotal[d.GroupID]++
		byGroupDecks[d.GroupID] = append(byGroupDecks[d.GroupID], DeckSummary{Name: d.Name, StudyEnabled: d.StudyEnabled})
		if d.StudyEnabled {
			byGroupStudy[d.GroupID] = append(byGroupStudy[d.GroupID], d.ID)
		}
	}

	named = make([]GroupForPicker, 0, len(groups))
	for _, g := range groups {
		studyIDs := byGroupStudy[g.ID]
		groupDecks := byGroupDecks[g.ID]
		sortStudyFirst(groupDecks)
		named = append(named, GroupForPicker{
			Group:      g,
			DeckIDs:    strings.Join(studyIDs, ","),
			TotalCount: byGroupTotal[g.ID],
			StudyCount: len(studyIDs),
			Decks:      groupDecks,
		})
	}

	if total := byGroupTotal[""]; total > 0 {
		studyIDs := byGroupStudy[""]
		unDecks := byGroupDecks[""]
		sortStudyFirst(unDecks)
		ungrouped = &GroupForPicker{
			DeckIDs:    strings.Join(studyIDs, ","),
			TotalCount: total,
			StudyCount: len(studyIDs),
			Decks:      unDecks,
		}
	}

	return named, ungrouped, nil
}

// TagForPicker is one entry in the pre-session tag selector.
type TagForPicker struct {
	Tag        *srs.Tag
	DeckIDs    string // comma-joined IDs of study-enabled decks, used as the ?decks= param
	TotalCount int    // total decks carrying the tag
	StudyCount int    // decks carrying the tag with StudyEnabled=true
	Decks      []DeckSummary
}

// TagsForPicker returns every tag paired with the IDs of its study-enabled
// decks. Used by the study-picker screen, which studies only "in study"
// decks when a tag is picked.
func (s *Service) TagsForPicker(ctx context.Context, userID string) ([]TagForPicker, error) {
	tags, err := s.repo.ListTags(ctx, userID)
	if err != nil {
		return nil, err
	}
	decks, err := s.repo.ListDecks(ctx, userID)
	if err != nil {
		return nil, err
	}
	deckByID := make(map[string]*srs.Deck, len(decks))
	for _, d := range decks {
		deckByID[d.ID] = d
	}

	out := make([]TagForPicker, 0, len(tags))
	for _, t := range tags {
		ids, err := s.repo.DeckIDsByTag(ctx, userID, t.ID)
		if err != nil {
			return nil, err
		}
		studyIDs := make([]string, 0, len(ids))
		summaries := make([]DeckSummary, 0, len(ids))
		for _, id := range ids {
			d := deckByID[id]
			if d == nil {
				continue
			}
			summaries = append(summaries, DeckSummary{Name: d.Name, StudyEnabled: d.StudyEnabled})
			if d.StudyEnabled {
				studyIDs = append(studyIDs, id)
			}
		}
		sortStudyFirst(summaries)
		out = append(out, TagForPicker{
			Tag:        t,
			DeckIDs:    strings.Join(studyIDs, ","),
			TotalCount: len(ids),
			StudyCount: len(studyIDs),
			Decks:      summaries,
		})
	}
	return out, nil
}

// HasLearnAhead reports whether any retention card is scheduled in the future,
// i.e. whether a learn-ahead session would have something to show.
func (s *Service) HasLearnAhead(ctx context.Context, userID string, deckIDs []string) (bool, error) {
	now := s.scheduler.AdjustedNow(s.scheduler.Now())
	cards, err := s.repo.CardsLearnAhead(ctx, userID, now, "next_rep", 1, deckIDs)
	if err != nil {
		return false, err
	}
	return len(cards) > 0, nil
}

// queueSize is how many cards still need work in the session: due retention
// reps plus every non-memorised card. The hand size only paces how many unseen
// cards are juggled at once — pickNext keeps introducing them as the hand
// empties, so they all belong to the session and must all be counted here.
func (s *Service) queueSize(ctx context.Context, userID string, now int64, deckIDs []string) (int, error) {
	due, err := s.repo.ScheduledCount(ctx, userID, now, deckIDs)
	if err != nil {
		return 0, err
	}
	nm, err := s.repo.NonMemorisedCount(ctx, userID, deckIDs)
	if err != nil {
		return 0, err
	}
	return due + nm, nil
}
