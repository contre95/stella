package srs

import "unicode/utf8"

// Deck is a named grouping of facts/cards (Mnemosyne's tags collapsed into a
// single grouping dimension for this simpler app).
type Deck struct {
	ID          string
	UserID      string
	Name        string
	Color       string
	Description string
	// Image is the filename of the deck's cover picture in the media
	// directory; empty means none.
	Image string
	// GroupID is the ID of the Group this deck belongs to; empty means none.
	GroupID string
}

// TruncatedName returns the first 12 runes of the name, appending ellipsis if
// the original is longer.
func (d *Deck) TruncatedName() string {
	if d == nil {
		return ""
	}
	const max = 12
	if utf8.RuneCountInString(d.Name) <= max {
		return d.Name
	}
	var runes []rune
	for _, r := range d.Name {
		runes = append(runes, r)
		if len(runes) >= max {
			break
		}
	}
	return string(runes) + "…"
}
