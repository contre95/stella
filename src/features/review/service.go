package review

import (
	"context"
	"fmt"
	"time"

	"github.com/contre95/stella/src/srs"
	"github.com/google/uuid"
)

// Stats are the aggregate counts shown on the dashboard.
type Stats struct {
	DueToday   int
	TotalCards int
	NewCards   int
	DeckCount  int
}

// CardInSession pairs a card with its parent fact and session metadata.
type CardInSession struct {
	Card      *srs.Card
	Fact      *srs.Fact
	Remaining int
	DeckName  string
	DeckColor string
}

// Service drives the review loop: queue selection, grading, and aggregate stats.
type Service struct {
	repo      srs.Repository
	scheduler *srs.Scheduler
}

// NewService constructs the review service.
func NewService(repo srs.Repository, scheduler *srs.Scheduler) *Service {
	return &Service{repo: repo, scheduler: scheduler}
}

// NextCard returns the highest-priority card to review, or nil when the queue
// is empty. deckIDs restricts the session to those decks; nil means all decks.
// Queue priority mirrors Mnemosyne's SM2:
//  1. Relearn grade-0 (failed immediately after grading, need re-test now)
//  2. Relearn grade-1 (lapsed retention cards brought back to acquisition)
//  3. Retention reps due today
//  4. Acquisition-phase cards already in hand (grade 1, no lapses)
//  5. Unseen cards, introduced up to NonMemorisedCardsInHand
//  6. Learn-ahead (nothing else scheduled)
func (s *Service) NextCard(ctx context.Context, deckIDs []string) (*CardInSession, error) {
	now := s.scheduler.AdjustedNow(time.Now())

	card, err := s.pickNext(ctx, now, deckIDs)
	if err != nil {
		return nil, err
	}
	if card == nil {
		return nil, nil
	}

	fact, err := s.repo.GetFact(ctx, card.FactID)
	if err != nil {
		return nil, err
	}

	remaining, err := s.queueSize(ctx, now, deckIDs)
	if err != nil {
		return nil, err
	}

	dn, dc := "", ""
	if d, err := s.repo.GetDeck(ctx, card.DeckID); err == nil && d != nil {
		dn, dc = d.Name, d.Color
	}

	return &CardInSession{Card: card, Fact: fact, Remaining: remaining, DeckName: dn, DeckColor: dc}, nil
}

// Grade applies a grade to a card, persists the updated scheduling state, logs
// the review event, and shifts next_rep by one day when a sister card is
// already scheduled on the same midnight-UTC value (sister-card avoidance).
func (s *Service) Grade(ctx context.Context, cardID string, grade int) error {
	card, err := s.repo.GetCard(ctx, cardID)
	if err != nil {
		return err
	}

	scheduledInterval := card.NextRep - card.LastRep
	nowUnix := time.Now().Unix()
	actualInterval := nowUnix - card.LastRep
	if card.Grade == srs.GradeUnseen {
		actualInterval = 0
	}

	s.scheduler.GradeAnswer(card, grade, false)

	if grade >= 2 && card.NextRep > 0 {
		count, err := s.repo.SisterCardCountScheduledBetween(
			ctx, card.FactID, card.ID, card.NextRep, card.NextRep+srs.DAY)
		if err != nil {
			return err
		}
		if count > 0 {
			card.NextRep += srs.DAY
		}
	}

	if err := s.repo.UpdateCard(ctx, card); err != nil {
		return err
	}

	return s.repo.AddReview(ctx, &srs.Review{
		ID:                uuid.NewString(),
		CardID:            card.ID,
		Grade:             grade,
		ScheduledInterval: scheduledInterval,
		ActualInterval:    actualInterval,
		ThinkingTime:      0,
		Timestamp:         nowUnix,
	})
}

// Stats returns aggregate counts for the dashboard (across all decks).
func (s *Service) Stats(ctx context.Context) (*Stats, error) {
	now := s.scheduler.AdjustedNow(time.Now())
	due, err := s.repo.ScheduledCount(ctx, now, nil)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.ActiveCount(ctx)
	if err != nil {
		return nil, err
	}
	newCards, err := s.repo.NonMemorisedCount(ctx, nil)
	if err != nil {
		return nil, err
	}
	decks, err := s.repo.ListDecks(ctx)
	if err != nil {
		return nil, err
	}
	return &Stats{DueToday: due, TotalCards: total, NewCards: newCards, DeckCount: len(decks)}, nil
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
func (s *Service) Forecast(ctx context.Context, days int) ([]ForecastDay, error) {
	if days < 1 {
		days = 1
	}
	now := s.scheduler.AdjustedNow(time.Now())

	counts := make([]int, days)
	due, err := s.repo.ScheduledCount(ctx, now, nil)
	if err != nil {
		return nil, err
	}
	counts[0] = due

	// All retention cards scheduled in the future, bucketed by day offset.
	future, err := s.repo.CardsLearnAhead(ctx, now, "next_rep", -1, nil)
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

	wall := time.Now()
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
func (s *Service) StudyHeatmap(ctx context.Context) (*Heatmap, error) {
	loc := s.scheduler.Loc
	if loc == nil {
		loc = time.Local
	}
	dayStarts := s.scheduler.Params.DayStartsAt

	today := studyDay(time.Now().Unix(), loc, dayStarts)
	startOfWeek := today.AddDate(0, 0, -int(today.Weekday())) // back to Sunday
	gridStart := startOfWeek.AddDate(0, 0, -(heatmapWeeks-1)*7)

	timestamps, err := s.repo.ReviewTimestampsSince(ctx, gridStart.Unix()-srs.DAY)
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

func (s *Service) pickNext(ctx context.Context, now int64, deckIDs []string) (*srs.Card, error) {
	// 1. Relearn: grade-0 cards need an immediate re-test.
	cards, err := s.repo.CardsToRelearn(ctx, 0, "interval", deckIDs)
	if err != nil {
		return nil, err
	}
	if len(cards) > 0 {
		return cards[0], nil
	}

	// 2. Relearn: lapsed retention cards now back in grade-1 acquisition.
	cards, err = s.repo.CardsToRelearn(ctx, 1, "interval", deckIDs)
	if err != nil {
		return nil, err
	}
	if len(cards) > 0 {
		return cards[0], nil
	}

	// 3. Retention reps due today.
	cards, err = s.repo.CardsDueForRetRep(ctx, now, "interval", 1, deckIDs)
	if err != nil {
		return nil, err
	}
	if len(cards) > 0 {
		return cards[0], nil
	}

	// 4. Cards already in the memorising hand (grade 1, no lapses).
	memorising1, err := s.repo.CardsNewMemorising(ctx, 1, deckIDs)
	if err != nil {
		return nil, err
	}
	if len(memorising1) > 0 {
		return memorising1[0], nil
	}

	// 5. Introduce unseen cards up to the hand limit.
	memorising0, err := s.repo.CardsNewMemorising(ctx, 0, deckIDs)
	if err != nil {
		return nil, err
	}
	inHand := len(memorising0) + len(memorising1)
	if inHand < s.scheduler.Params.NonMemorisedCardsInHand {
		unseen, err := s.repo.CardsUnseen(ctx, "", 1, deckIDs)
		if err != nil {
			return nil, err
		}
		if len(unseen) > 0 {
			return unseen[0], nil
		}
	}

	// 6. Nothing due today — offer learn-ahead.
	cards, err = s.repo.CardsLearnAhead(ctx, now, "interval", 1, deckIDs)
	if err != nil {
		return nil, err
	}
	if len(cards) > 0 {
		return cards[0], nil
	}

	return nil, nil
}

// queueSize estimates how many actionable cards remain in the session.
func (s *Service) queueSize(ctx context.Context, now int64, deckIDs []string) (int, error) {
	due, err := s.repo.ScheduledCount(ctx, now, deckIDs)
	if err != nil {
		return 0, err
	}
	nm, err := s.repo.NonMemorisedCount(ctx, deckIDs)
	if err != nil {
		return 0, err
	}
	cap := s.scheduler.Params.NonMemorisedCardsInHand
	if nm > cap {
		nm = cap
	}
	return due + nm, nil
}
