package srs

import (
	"math"
	"testing"
	"time"
)

// detRand makes scheduling deterministic: random.choice picks the first option
// and random.uniform returns 0, so interval noise is always 0 and the grade
// 3/4 acquisition picks are fixed.
type detRand struct{}

func (detRand) Choice(opts []int) int             { return opts[0] }
func (detRand) Uniform(low, high float64) float64 { return 0 }

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func newTestScheduler(now time.Time, dayStartsAt int) *Scheduler {
	return NewScheduler(
		Params{DayStartsAt: dayStartsAt, NonMemorisedCardsInHand: 10},
		time.UTC,
		fixedNow(now),
		detRand{},
	)
}

func TestCalculateInitialInterval(t *testing.T) {
	s := newTestScheduler(time.Unix(0, 0).UTC(), 3)
	want := []int64{0, 0, DAY, 3 * DAY, 4 * DAY, 7 * DAY}
	for g, w := range want {
		if got := s.CalculateInitialInterval(g); got != w {
			t.Errorf("grade %d: got %d want %d", g, got, w)
		}
	}
}

func TestMidnightUTC(t *testing.T) {
	s := newTestScheduler(time.Now(), 3)
	midnight := time.Date(2012, 1, 14, 0, 0, 0, 0, time.UTC).Unix()
	for _, hm := range []struct{ h, m int }{{0, 0}, {2, 0}, {23, 59}} {
		ts := time.Date(2012, 1, 14, hm.h, hm.m, 0, 0, time.UTC).Unix()
		if got := s.MidnightUTC(ts); got != midnight {
			t.Errorf("%02d:%02d -> %d want %d", hm.h, hm.m, got, midnight)
		}
	}
}

// A card stored at midnight UTC becomes due at day_starts_at local time:
// before that it is not yet due, at/after it is.
func TestAdjustedNowBoundary(t *testing.T) {
	s := newTestScheduler(time.Now(), 3)
	midnight := time.Date(2012, 1, 14, 0, 0, 0, 0, time.UTC).Unix()
	before := time.Date(2012, 1, 14, 2, 59, 0, 0, time.UTC)
	after := time.Date(2012, 1, 14, 3, 0, 0, 0, time.UTC)
	if s.AdjustedNow(before) >= midnight {
		t.Errorf("02:59 should be before due time")
	}
	if s.AdjustedNow(after) < midnight {
		t.Errorf("03:00 should be at/after due time")
	}
}

func TestGradeAnswer_Unseen(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	s := newTestScheduler(now, 3)
	cases := []struct {
		grade int
		want  int64
	}{{0, 0}, {1, 0}, {2, DAY}, {3, 3 * DAY}, {4, 4 * DAY}, {5, 7 * DAY}}
	for _, c := range cases {
		card := &Card{Grade: GradeUnseen, Easiness: 2.5}
		got := s.GradeAnswer(card, c.grade, false)
		if got != c.want {
			t.Errorf("unseen->%d: interval got %d want %d", c.grade, got, c.want)
		}
		if card.Grade != c.grade {
			t.Errorf("unseen->%d: grade not updated (%d)", c.grade, card.Grade)
		}
		if card.AcqReps != 1 || card.AcqRepsSinceLapse != 1 {
			t.Errorf("unseen->%d: acq reps not initialised", c.grade)
		}
		if c.grade >= 2 {
			if want := s.MidnightUTC(now.Unix() + c.want); card.NextRep != want {
				t.Errorf("unseen->%d: next_rep got %d want %d", c.grade, card.NextRep, want)
			}
		} else if card.NextRep != card.LastRep {
			t.Errorf("unseen->%d: next_rep should equal last_rep for grade<2", c.grade)
		}
	}
}

func TestGradeAnswer_AcqToRetention(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	s := newTestScheduler(now, 3)
	// detRand picks the first option, so grade 3 -> 1*DAY and grade 4 -> 1*DAY.
	cases := []struct {
		grade int
		want  int64
	}{{2, DAY}, {3, DAY}, {4, DAY}, {5, 2 * DAY}}
	for _, c := range cases {
		card := &Card{Grade: 1, Easiness: 2.5, AcqReps: 2, AcqRepsSinceLapse: 2}
		got := s.GradeAnswer(card, c.grade, false)
		if got != c.want {
			t.Errorf("acq->%d: interval got %d want %d", c.grade, got, c.want)
		}
		if card.AcqReps != 3 {
			t.Errorf("acq->%d: acq_reps got %d want 3", c.grade, card.AcqReps)
		}
	}
}

func TestGradeAnswer_Lapse(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	s := newTestScheduler(now, 3)
	card := &Card{
		Grade: 4, Easiness: 2.5, RetReps: 3, RetRepsSinceLapse: 3,
		LastRep: now.Unix() - 10*DAY, NextRep: s.AdjustedNow(now),
	}
	got := s.GradeAnswer(card, 0, false)
	if got != 0 {
		t.Errorf("lapse interval got %d want 0", got)
	}
	if card.Lapses != 1 {
		t.Errorf("lapses got %d want 1", card.Lapses)
	}
	if card.AcqRepsSinceLapse != 0 || card.RetRepsSinceLapse != 0 {
		t.Errorf("since-lapse counters not reset: acq=%d ret=%d", card.AcqRepsSinceLapse, card.RetRepsSinceLapse)
	}
	if card.NextRep != card.LastRep {
		t.Errorf("lapse: next_rep should equal last_rep")
	}
}

// First retention rep after acquisition always schedules 6 days out.
func TestGradeAnswer_FirstRetentionRep(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	s := newTestScheduler(now, 3)
	card := &Card{
		Grade: 2, Easiness: 2.5, RetReps: 0, RetRepsSinceLapse: 0,
		LastRep: now.Unix() - 1*DAY, NextRep: s.AdjustedNow(now),
	}
	got := s.GradeAnswer(card, 4, false)
	if got != 6*DAY {
		t.Errorf("first retention rep interval got %d want %d", got, 6*DAY)
	}
}

// Subsequent on-time retention rep grows the interval by actual * easiness;
// grade 4 leaves easiness unchanged.
func TestGradeAnswer_RetentionGrowth(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	s := newTestScheduler(now, 3)
	card := &Card{
		Grade: 4, Easiness: 2.5, RetReps: 5, RetRepsSinceLapse: 5,
		LastRep: now.Unix() - 10*DAY, NextRep: s.AdjustedNow(now), // due today => ON TIME
	}
	got := s.GradeAnswer(card, 4, false)
	if want := int64(float64(10*DAY) * 2.5); got != want {
		t.Errorf("retention growth interval got %d want %d", got, want)
	}
	if card.Easiness != 2.5 {
		t.Errorf("grade 4 should not change easiness, got %v", card.Easiness)
	}
}

// Easiness adjustments: grade 2 drops it 0.16, grade 5 raises it 0.10, applied
// before the interval is computed.
func TestGradeAnswer_EasinessAdjustments(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	s := newTestScheduler(now, 3)

	for _, c := range []struct {
		newGrade int
		delta    float64
	}{{2, -0.16}, {3, -0.14}, {5, +0.10}} {
		card := &Card{
			Grade: 3, Easiness: 2.5, RetReps: 5, RetRepsSinceLapse: 5,
			LastRep: now.Unix() - 10*DAY, NextRep: s.AdjustedNow(now),
		}
		got := s.GradeAnswer(card, c.newGrade, false)
		wantEase := 2.5 + c.delta
		if math.Abs(card.Easiness-wantEase) > 1e-9 {
			t.Errorf("grade %d: easiness got %v want %v", c.newGrade, card.Easiness, wantEase)
		}
		// grade 5 when not EARLY uses actual*easiness; grades 2/3 on time too.
		if want := int64(float64(10*DAY) * card.Easiness); got != want {
			t.Errorf("grade %d: interval got %d want %d", c.newGrade, got, want)
		}
	}
}

// Learning late: the interval was too long, so reuse the scheduled interval
// rather than the much larger actual * easiness.
func TestGradeAnswer_LateUsesScheduledInterval(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	s := newTestScheduler(now, 3)
	lastRep := now.Unix() - 30*DAY
	nextRep := now.Unix() - 20*DAY // well overdue => LATE
	card := &Card{
		Grade: 3, Easiness: 2.5, RetReps: 5, RetRepsSinceLapse: 5,
		LastRep: lastRep, NextRep: nextRep,
	}
	wantScheduled := (nextRep - lastRep) + int64(s.Params.DayStartsAt)*HOUR // UTC offset 0
	got := s.GradeAnswer(card, 2, false)
	if got != wantScheduled {
		t.Errorf("late grade 2 interval got %d want %d (scheduled)", got, wantScheduled)
	}
}

func TestGradeAnswer_DryRunDoesNotMutate(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	s := newTestScheduler(now, 3)
	card := &Card{Grade: 1, Easiness: 2.5, AcqReps: 2}
	before := *card
	got := s.GradeAnswer(card, 5, true)
	if got != 2*DAY {
		t.Errorf("dry run interval got %d want %d", got, 2*DAY)
	}
	if *card != before {
		t.Errorf("dry run mutated the card: %+v != %+v", *card, before)
	}
}

func TestNextRepToIntervalString(t *testing.T) {
	s := newTestScheduler(time.Now(), 3)
	cases := []struct {
		diff float64 // in days
		want string
	}{
		{0.5, "tomorrow"},
		{1.5, "in 2 days"},
		{32, "in 1 month"},
		{63, "in 2 months"},
		{366, "in 1.0 years"},
		{-0.5, "today"},
		{-1.5, "1 day overdue"},
		{-5, "5 days overdue"},
		{-40, "1 month overdue"},
		{-90, "2 months overdue"},
		{-400, "1.1 years overdue"},
	}
	for _, c := range cases {
		nextRep := int64(c.diff * float64(DAY))
		if got := s.NextRepToIntervalString(nextRep, 0); got != c.want {
			t.Errorf("diff %.1fd: got %q want %q", c.diff, got, c.want)
		}
	}
}

func TestLastRepToIntervalString(t *testing.T) {
	s := newTestScheduler(time.Now(), 3)
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		daysAgo int
		want    string
	}{
		{0, "today"},
		{1, "yesterday"},
		{2, "2 days ago"},
		{40, "1 month ago"},
		{70, "2 months ago"},
		{366, "1.0 years ago"},
	}
	for _, c := range cases {
		lastRep := now.AddDate(0, 0, -c.daysAgo).Unix()
		if got := s.LastRepToIntervalString(lastRep, now.Unix()); got != c.want {
			t.Errorf("%d days ago: got %q want %q", c.daysAgo, got, c.want)
		}
	}
}
