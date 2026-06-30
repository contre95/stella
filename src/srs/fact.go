package srs

// Fact is the raw content from which one or more sister cards are generated.
// In the Q/A + reverse model a fact has a front and a back; the forward card
// quizzes front->back and the optional reverse card quizzes back->front.
type Fact struct {
	ID       string
	Front    string
	Back     string
	DeckID   string
	Created  int64 // POSIX seconds
	Modified int64 // POSIX seconds
}
