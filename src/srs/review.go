package srs

// Review is a log entry recorded every time a card is graded. It mirrors the
// data Mnemosyne writes to its log on each repetition and is the basis for
// statistics.
type Review struct {
	ID                string
	CardID            string
	Grade             int
	ScheduledInterval int64 // interval that led up to this repetition
	ActualInterval    int64 // time actually elapsed since last rep
	ThinkingTime      int64 // seconds spent before grading
	Timestamp         int64 // POSIX seconds when graded
}
