package srs

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"
)

// Time constants, in seconds. All intervals in the engine are seconds, since
// times are stored as integer POSIX timestamps.
const (
	HOUR int64 = 60 * 60
	DAY  int64 = 24 * HOUR
)

// Rand abstracts the two sources of randomness the algorithm uses, so tests can
// make scheduling deterministic. It mirrors Python's random.choice / uniform.
type Rand interface {
	Choice(opts []int) int
	Uniform(low, high float64) float64
}

// mathRand is the production Rand backed by math/rand.
type mathRand struct{ r *rand.Rand }

// NewMathRand returns a Rand seeded from seed.
func NewMathRand(seed int64) Rand {
	return &mathRand{r: rand.New(rand.NewSource(seed))}
}

func (m *mathRand) Choice(opts []int) int { return opts[m.r.Intn(len(opts))] }

func (m *mathRand) Uniform(low, high float64) float64 {
	return low + m.r.Float64()*(high-low)
}

// Params are the scheduler tunables, ported from Mnemosyne's config keys. The
// domain defines its own struct so it stays free of any feature/config imports.
type Params struct {
	DayStartsAt              int
	NonMemorisedCardsInHand  int
	MaxScheduledIntervalDays int
	RandomiseScheduledCards  bool
	RandomiseNewCards        bool
}

// Scheduler is the SM2 engine: a faithful port of Mnemosyne's SM2Mnemosyne.
// It is pure with respect to storage — all the grading math operates on Cards
// in memory. Queue building (which needs the Repository) lives in the review
// feature.
type Scheduler struct {
	Params Params
	Loc    *time.Location
	Now    func() time.Time
	Rand   Rand
}

// NewScheduler builds a Scheduler, filling in sensible defaults for the
// injectable dependencies.
func NewScheduler(p Params, loc *time.Location, now func() time.Time, r Rand) *Scheduler {
	if loc == nil {
		loc = time.Local
	}
	if now == nil {
		now = time.Now
	}
	if r == nil {
		r = NewMathRand(time.Now().UnixNano())
	}
	return &Scheduler{Params: p, Loc: loc, Now: now, Rand: r}
}

// MidnightUTC rounds a POSIX timestamp to midnight UTC of its local date, so
// all cards due on the same day share a single next_rep value regardless of
// timezone. Ported from Scheduler.midnight_UTC.
func (s *Scheduler) MidnightUTC(ts int64) int64 {
	t := time.Unix(ts, 0).In(s.Loc)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
}

// AdjustedNow converts a wall-clock instant into a value comparable against the
// midnight-UTC next_rep, honouring day_starts_at and the local UTC offset. A
// card is due when AdjustedNow >= next_rep. Ported from Scheduler.adjusted_now.
func (s *Scheduler) AdjustedNow(now time.Time) int64 {
	sec := now.Unix() - int64(s.Params.DayStartsAt)*HOUR
	// Python subtracts time.timezone/altzone (offset west of UTC); the offset
	// east returned by Zone() is its negation, so we add it.
	_, offsetEast := now.In(s.Loc).Zone()
	return sec + int64(offsetEast)
}

// CalculateInitialInterval returns the interval for a card's first repetition.
// Ported from SM2Mnemosyne.calculate_initial_interval.
func (s *Scheduler) CalculateInitialInterval(grade int) int64 {
	return []int64{0, 0, 1 * DAY, 3 * DAY, 4 * DAY, 7 * DAY}[grade]
}

// CalculateIntervalNoise spreads cards out so equal intervals don't all fall on
// the same day. Ported from SM2Mnemosyne.calculate_interval_noise.
func (s *Scheduler) CalculateIntervalNoise(interval int64) int64 {
	switch {
	case interval == 0:
		return 0
	case interval <= 10*DAY:
		return int64(s.Rand.Choice([]int{0, int(DAY)}))
	case interval <= 60*DAY:
		return int64(s.Rand.Uniform(-3*float64(DAY), 3*float64(DAY)))
	default:
		return int64(s.Rand.Uniform(-0.05*float64(interval), 0.05*float64(interval)))
	}
}

// trueScheduledInterval recovers the interval that led up to this repetition.
// Ported from SM2Mnemosyne.true_scheduled_interval.
func (s *Scheduler) trueScheduledInterval(card *Card, now time.Time) int64 {
	interval := card.NextRep - card.LastRep
	if card.Grade < 2 {
		return interval
	}
	_, offsetEast := now.In(s.Loc).Zone()
	interval += int64(s.Params.DayStartsAt)*HOUR - int64(offsetEast)
	return interval
}

// SetInitialGrade gives a set of sister cards their initial grade, staggering
// their next_rep so they don't all fall due on the same day. Ported from
// SM2Mnemosyne.set_initial_grade. Returns the per-card intervals applied.
func (s *Scheduler) SetInitialGrade(cards []*Card, grade int) {
	now := s.Now()
	lastRep := now.Unix()
	interval := s.CalculateInitialInterval(grade)
	interval += s.CalculateIntervalNoise(interval)
	nextRep := s.MidnightUTC(lastRep + interval)
	for _, card := range cards {
		card.Grade = grade
		card.Easiness = 2.5
		card.AcqReps = 1
		card.AcqRepsSinceLapse = 1
		card.LastRep = lastRep
		card.NextRep = nextRep
		nextRep += DAY
	}
}

// timing classifies whether a repetition is happening early, on time, or late.
const (
	timingEarly  = "EARLY"
	timingOnTime = "ON TIME"
	timingLate   = "LATE"
)

// GradeAnswer applies a new grade to a card, mutating its scheduling state and
// returning the newly scheduled interval in seconds. When dryRun is true the
// card is left untouched and no interval noise is added, so callers can preview
// the interval each grade would produce. Ported from SM2Mnemosyne.grade_answer.
//
// Note: Mnemosyne measures the actual interval from when the card was *shown*;
// here we measure from grading time (Now), which is equivalent for scheduling.
func (s *Scheduler) GradeAnswer(card *Card, newGrade int, dryRun bool) int64 {
	now := s.Now()
	nowUnix := now.Unix()
	adjustedNow := s.AdjustedNow(now)

	if dryRun {
		clone := *card
		card = &clone
	}

	// Determine the timing relative to when the card was due.
	var timing string
	switch {
	case adjustedNow-DAY >= card.NextRep:
		timing = timingLate
	case adjustedNow < card.NextRep:
		timing = timingEarly
	default:
		timing = timingOnTime
	}

	scheduledInterval := s.trueScheduledInterval(card, now)

	var actualInterval int64
	if card.Grade != GradeUnseen {
		actualInterval = nowUnix - card.LastRep
	}

	oldGrade := card.Grade
	var newInterval int64

	switch {
	case oldGrade == GradeUnseen:
		// First time the card is graded.
		card.Easiness = 2.5
		card.AcqReps = 1
		card.AcqRepsSinceLapse = 1
		newInterval = s.CalculateInitialInterval(newGrade)

	case gradeIn(oldGrade, 0, 1) && gradeIn(newGrade, 0, 1):
		// Acquisition phase, staying there.
		card.AcqReps++
		card.AcqRepsSinceLapse++
		newInterval = 0

	case gradeIn(oldGrade, 0, 1) && gradeIn(newGrade, 2, 3, 4, 5):
		// Acquisition -> retention.
		card.AcqReps++
		card.AcqRepsSinceLapse++
		switch newGrade {
		case 2:
			newInterval = DAY
		case 3:
			newInterval = int64(s.Rand.Choice([]int{1, 1, 2})) * DAY
		case 4:
			newInterval = int64(s.Rand.Choice([]int{1, 2, 2})) * DAY
		case 5:
			newInterval = 2 * DAY
		}

	case gradeIn(oldGrade, 2, 3, 4, 5) && gradeIn(newGrade, 0, 1):
		// Retention -> acquisition (a lapse).
		card.RetReps++
		card.Lapses++
		card.AcqRepsSinceLapse = 0
		card.RetRepsSinceLapse = 0
		newInterval = 0

	case gradeIn(oldGrade, 2, 3, 4, 5) && gradeIn(newGrade, 2, 3, 4, 5):
		// Retention phase, staying there.
		card.RetReps++
		card.RetRepsSinceLapse++
		// Don't update easiness when learning ahead (EARLY).
		if timing == timingLate || timing == timingOnTime {
			switch newGrade {
			case 2:
				card.Easiness -= 0.16
			case 3:
				card.Easiness -= 0.14
			case 5:
				card.Easiness += 0.10
			}
			if card.Easiness < 1.3 {
				card.Easiness = 1.3
			}
		}
		if card.RetRepsSinceLapse == 1 {
			newInterval = 6 * DAY
		} else {
			switch newGrade {
			case 2, 3:
				if timing == timingOnTime || timing == timingEarly {
					newInterval = int64(float64(actualInterval) * card.Easiness)
				} else {
					// Late and the interval was too long; reuse the scheduled
					// interval rather than the much larger actual*easiness.
					newInterval = scheduledInterval
				}
			case 4:
				newInterval = int64(float64(actualInterval) * card.Easiness)
			case 5:
				if timing == timingEarly {
					// Learning ahead; avoid explosive growth.
					newInterval = scheduledInterval
				} else {
					newInterval = int64(float64(actualInterval) * card.Easiness)
				}
			}
			// Pathological case where actual_interval collapsed to ~0.
			if newInterval < DAY {
				newInterval = DAY
			}
		}
	}

	// Optional cap on the interval.
	if s.Params.MaxScheduledIntervalDays > 0 {
		capped := int64(s.Params.MaxScheduledIntervalDays) * DAY
		if newInterval > capped {
			newInterval = capped
		}
	}

	if dryRun {
		return newInterval
	}

	// Add randomness to spread cards out.
	newInterval += s.CalculateIntervalNoise(newInterval)

	card.Grade = newGrade
	card.LastRep = nowUnix
	if newGrade >= 2 {
		card.NextRep = s.MidnightUTC(nowUnix + newInterval)
		// Sister-card avoidance is applied in the review service, which has the
		// repository needed to look up sisters.
	} else {
		card.NextRep = card.LastRep
	}
	return newInterval
}

// NextRepToIntervalString renders a human label like "tomorrow" or "in 2 days".
// Ported from Scheduler.next_rep_to_interval_string.
func (s *Scheduler) NextRepToIntervalString(nextRep, now int64) string {
	days := float64(nextRep-now) / float64(DAY)
	switch {
	case days >= 365:
		return "in " + fmt.Sprintf("%.1f", days/365) + " years"
	case days >= 62:
		return "in " + strconv.Itoa(int(days/31)) + " months"
	case days >= 31:
		return "in 1 month"
	case days >= 1:
		return "in " + strconv.Itoa(int(days)+1) + " days"
	case days >= 0:
		return "tomorrow"
	case days >= -1:
		return "today"
	case days >= -2:
		return "1 day overdue"
	case days >= -31:
		return strconv.Itoa(int(-days)) + " days overdue"
	case days >= -62:
		return "1 month overdue"
	case days >= -365:
		return strconv.Itoa(int(-days/31)) + " months overdue"
	default:
		return fmt.Sprintf("%.1f ", -days/365) + "years overdue"
	}
}

// LastRepToIntervalString renders a human label like "yesterday" or "2 days
// ago". Both timestamps are snapped to midnight UTC first. Ported from
// Scheduler.last_rep_to_interval_string.
func (s *Scheduler) LastRepToIntervalString(lastRep, now int64) string {
	now = s.MidnightUTC(now - int64(s.Params.DayStartsAt)*HOUR)
	lastRep = s.MidnightUTC(lastRep - int64(s.Params.DayStartsAt)*HOUR)
	days := float64(lastRep-now) / float64(DAY)
	switch {
	case days > -1:
		return "today"
	case days > -2:
		return "yesterday"
	case days > -31:
		return strconv.Itoa(int(-days)) + " days ago"
	case days > -62:
		return "1 month ago"
	case days > -365:
		return strconv.Itoa(int(-days/31)) + " months ago"
	default:
		return fmt.Sprintf("%.1f ", -days/365) + "years ago"
	}
}

// gradeIn reports whether g is one of the given grades.
func gradeIn(g int, set ...int) bool {
	for _, v := range set {
		if g == v {
			return true
		}
	}
	return false
}
