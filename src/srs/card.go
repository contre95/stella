package srs

// CardView identifies which direction of a fact a card quizzes.
type CardView string

const (
	ViewForward CardView = "forward" // front -> back
	ViewReverse CardView = "reverse" // back -> front
)

// GradeUnseen marks a card that has never been graded.
const GradeUnseen = -1

// Card holds the scheduling state for one direction of a fact. Field meanings
// are ported verbatim from Mnemosyne:
//
//   - Grade: -1 unseen, 0-1 acquisition phase, 2-5 retention phase.
//   - Easiness: SM2 ease factor, starts at 2.5, floored at 1.3.
//   - AcqReps/RetReps: repetitions seen in the acquisition / retention phase.
//   - Lapses: times a retained card (grade >= 2) was forgotten (graded 0/1).
//   - *SinceLapse: counters reset when the card lapses.
//   - LastRep/NextRep: POSIX seconds. NextRep is stored as midnight UTC for
//     retention reps so all cards due the same day share a value.
type Card struct {
	ID                string
	UserID            string
	FactID            string
	DeckID            string
	View              CardView
	Grade             int
	Easiness          float64
	AcqReps           int
	RetReps           int
	Lapses            int
	AcqRepsSinceLapse int
	RetRepsSinceLapse int
	LastRep           int64
	NextRep           int64
	Active            bool
}

// ResetLearningData returns a card to the unseen state, used on creation or
// when resetting a card's history.
func (c *Card) ResetLearningData() {
	c.Grade = GradeUnseen
	c.NextRep = -1
	c.LastRep = -1
	c.Easiness = 2.5
	c.AcqReps = 0
	c.RetReps = 0
	c.Lapses = 0
	c.AcqRepsSinceLapse = 0
	c.RetRepsSinceLapse = 0
}
