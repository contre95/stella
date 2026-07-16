// Package portability handles import and export of deck and group archives,
// providing the full data-transfer story for Stella. It depends on the cards
// feature for CRUD operations but owns the wire-format types and ZIP codec.
package portability

import "github.com/contre95/stella/src/srs"

// GroupExport is the JSON-serialisable manifest for a group export zip.
type GroupExport struct {
	Name  string       `json:"name"`
	Decks []DeckExport `json:"decks"`
}

// DeckExport is the JSON-serialisable representation of a deck plus its facts.
type DeckExport struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Image       string       `json:"image,omitempty"`
	ImageData   []byte       `json:"image_data,omitempty"`
	Facts       []FactExport `json:"facts"`
}

// FactExport is the JSON-serialisable representation of a single fact.
//
// Cards carries the scheduling state and review history of the fact's sister
// cards. When present it is authoritative and Reverse is ignored on import:
// the cards are restored verbatim so an imported deck resumes where it left
// off. Reverse remains the content-only signal for hand-authored exports that
// carry no stats (empty Cards): a forward card is created plus a reverse one
// when Reverse is true.
type FactExport struct {
	Front          string      `json:"front"`
	Back           string      `json:"back"`
	ImageFront     string      `json:"image_front,omitempty"`
	ImageBack      string      `json:"image_back,omitempty"`
	ImageFrontData []byte      `json:"image_front_data,omitempty"`
	ImageBackData  []byte      `json:"image_back_data,omitempty"`
	AudioFront     string      `json:"audio_front,omitempty"`
	AudioBack      string      `json:"audio_back,omitempty"`
	AudioFrontData []byte      `json:"audio_front_data,omitempty"`
	AudioBackData  []byte      `json:"audio_back_data,omitempty"`
	Reverse        bool        `json:"reverse"`
	Created        int64       `json:"created,omitempty"`
	Modified       int64       `json:"modified,omitempty"`
	Cards          []CardStats `json:"cards,omitempty"`
}

// CardStats is the SRS scheduling state for one sister card (one view of a
// fact) plus its review log. Fields mirror srs.Card; see that type for their
// meaning.
type CardStats struct {
	View              srs.CardView  `json:"view"`
	Grade             int           `json:"grade"`
	Easiness          float64       `json:"easiness"`
	AcqReps           int           `json:"acq_reps"`
	RetReps           int           `json:"ret_reps"`
	Lapses            int           `json:"lapses"`
	AcqRepsSinceLapse int           `json:"acq_reps_since_lapse"`
	RetRepsSinceLapse int           `json:"ret_reps_since_lapse"`
	LastRep           int64         `json:"last_rep"`
	NextRep           int64         `json:"next_rep"`
	Active            bool          `json:"active"`
	Reviews           []ReviewStats `json:"reviews,omitempty"`
}

// ReviewStats is one entry of a card's review log. Fields mirror srs.Review
// (the card is identified by its owning CardStats, so no card id is carried).
type ReviewStats struct {
	Grade             int    `json:"grade"`
	RetReps           int    `json:"ret_reps"`
	ScheduledInterval int64  `json:"scheduled_interval"`
	ActualInterval    int64  `json:"actual_interval"`
	ThinkingTime      int64  `json:"thinking_time"`
	Timestamp         int64  `json:"timestamp"`
	Source            string `json:"source,omitempty"`
}
