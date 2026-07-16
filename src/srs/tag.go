package srs

// Tag is a free-form label attached to decks. Unlike groups (at most one per
// deck), a deck can carry any number of tags. Names are unique per user.
type Tag struct {
	ID     string
	UserID string
	Name   string
}
