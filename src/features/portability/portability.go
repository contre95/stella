// Package portability handles import and export of deck and group archives,
// providing the full data-transfer story for Stella. It depends on the cards
// feature for CRUD operations but owns the wire-format types and ZIP codec.
package portability

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
type FactExport struct {
	Front          string `json:"front"`
	Back           string `json:"back"`
	ImageFront     string `json:"image_front,omitempty"`
	ImageBack      string `json:"image_back,omitempty"`
	ImageFrontData []byte `json:"image_front_data,omitempty"`
	ImageBackData  []byte `json:"image_back_data,omitempty"`
	AudioFront     string `json:"audio_front,omitempty"`
	AudioBack      string `json:"audio_back,omitempty"`
	AudioFrontData []byte `json:"audio_front_data,omitempty"`
	AudioBackData  []byte `json:"audio_back_data,omitempty"`
	Reverse        bool   `json:"reverse"`
}
