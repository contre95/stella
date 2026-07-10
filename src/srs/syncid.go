package srs

import (
	"crypto/rand"
	"encoding/hex"
)

// NewSyncID returns a fresh 8-hex-char (32-bit) sync ID. Uniqueness per user
// is enforced by the repository's unique index; callers retry on collision.
func NewSyncID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return hex.EncodeToString(b)
}

// DeckExportRow is one card in a deck export for the device sync protocol:
// the card's sync ID plus the question/answer text with the view direction
// already applied (a reverse card's front is the fact's back).
type DeckExportRow struct {
	SyncID string
	Front  string
	Back   string
}
