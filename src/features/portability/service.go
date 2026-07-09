package portability

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/srs"
	"github.com/google/uuid"
)

// Service handles import and export of deck and group archives.
type Service struct {
	cards *cards.Service
}

// NewService constructs the portability service.
func NewService(cardsService *cards.Service) *Service {
	return &Service{cards: cardsService}
}

// saveAudioData writes raw audio bytes to a new file in mediaPath and returns
// the generated filename. ext must include the leading dot (e.g. ".mp3").
func saveAudioData(mediaPath string, data []byte, ext string) (string, error) {
	if err := os.MkdirAll(mediaPath, 0o755); err != nil {
		return "", err
	}
	filename := uuid.NewString() + ext
	if err := os.WriteFile(filepath.Join(mediaPath, filename), data, 0o644); err != nil {
		return "", err
	}
	return filename, nil
}

// saveImageData writes raw image bytes to a new file in mediaPath and returns
// the generated filename. The extension is inferred from the content.
func saveImageData(mediaPath string, data []byte) (string, error) {
	if err := os.MkdirAll(mediaPath, 0o755); err != nil {
		return "", err
	}
	var ext string
	switch {
	case len(data) > 2 && data[0] == 0xff && data[1] == 0xd8:
		ext = ".jpg"
	case len(data) > 4 && bytes.Equal(data[:4], []byte("\x89PNG")):
		ext = ".png"
	case len(data) > 3 && bytes.Equal(data[:3], []byte("GIF")):
		ext = ".gif"
	case len(data) > 11 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		ext = ".webp"
	default:
		return "", fmt.Errorf("unrecognised image format")
	}
	filename := uuid.NewString() + ext
	if err := os.WriteFile(filepath.Join(mediaPath, filename), data, 0o644); err != nil {
		return "", err
	}
	return filename, nil
}

// ExportDeck returns a DeckExport for the given deck ID with media bytes
// embedded.
func (s *Service) ExportDeck(ctx context.Context, userID, deckID string) (*DeckExport, error) {
	deck, err := s.cards.GetDeck(ctx, userID, deckID)
	if err != nil {
		return nil, err
	}
	rows, err := s.cards.ListFactsByDeck(ctx, userID, deckID)
	if err != nil {
		return nil, err
	}
	mediaPath := s.cards.ImagePath()
	exp := &DeckExport{Name: deck.Name, Description: deck.Description, Image: deck.Image, Facts: make([]FactExport, 0, len(rows))}
	if deck.Image != "" {
		if data, err := os.ReadFile(filepath.Join(mediaPath, deck.Image)); err == nil {
			exp.ImageData = data
		}
	}
	for _, row := range rows {
		f := row.Fact
		hasReverse := false
		for _, c := range row.Cards {
			if c.Card.View == srs.ViewReverse {
				hasReverse = true
				break
			}
		}
		fe := FactExport{Front: f.Front, Back: f.Back, ImageFront: f.ImageFront, ImageBack: f.ImageBack, AudioFront: f.AudioFront, AudioBack: f.AudioBack, Reverse: hasReverse}
		if f.ImageFront != "" {
			if data, err := os.ReadFile(filepath.Join(mediaPath, f.ImageFront)); err == nil {
				fe.ImageFrontData = data
			}
		}
		if f.ImageBack != "" {
			if data, err := os.ReadFile(filepath.Join(mediaPath, f.ImageBack)); err == nil {
				fe.ImageBackData = data
			}
		}
		if f.AudioFront != "" {
			if data, err := os.ReadFile(filepath.Join(mediaPath, f.AudioFront)); err == nil {
				fe.AudioFrontData = data
			}
		}
		if f.AudioBack != "" {
			if data, err := os.ReadFile(filepath.Join(mediaPath, f.AudioBack)); err == nil {
				fe.AudioBackData = data
			}
		}
		exp.Facts = append(exp.Facts, fe)
	}
	return exp, nil
}

// ExportGroup builds a GroupExport containing every deck that belongs to the
// group.
func (s *Service) ExportGroup(ctx context.Context, userID, groupID string) (*GroupExport, error) {
	group, err := s.cards.GetGroup(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	decks, err := s.cards.ListDecks(ctx, userID)
	if err != nil {
		return nil, err
	}
	exp := &GroupExport{Name: group.Name}
	for _, d := range decks {
		if d.GroupID != groupID {
			continue
		}
		de, err := s.ExportDeck(ctx, userID, d.ID)
		if err != nil {
			return nil, err
		}
		exp.Decks = append(exp.Decks, *de)
	}
	return exp, nil
}

// ImportDeck creates a new deck from a DeckExport. If nameOverride is
// non-empty it replaces the name stored in the export.
func (s *Service) ImportDeck(ctx context.Context, userID string, exp *DeckExport, nameOverride string) (*srs.Deck, int, error) {
	name := exp.Name
	if nameOverride != "" {
		name = nameOverride
	}
	mediaPath := s.cards.ImagePath()
	if exp.Image != "" && exp.Image != filepath.Base(exp.Image) {
		return nil, 0, fmt.Errorf("invalid media filename in import: %q", exp.Image)
	}
	cover := exp.Image
	if len(exp.ImageData) > 0 {
		saved, err := saveImageData(mediaPath, exp.ImageData)
		if err != nil {
			return nil, 0, err
		}
		cover = saved
	}
	deck, err := s.cards.AddDeck(ctx, userID, name, exp.Description, cover, "")
	if err != nil {
		return nil, 0, err
	}
	imported := 0
	for _, fe := range exp.Facts {
		for _, name := range []string{fe.ImageFront, fe.ImageBack, fe.AudioFront, fe.AudioBack} {
			if name != "" && name != filepath.Base(name) {
				return deck, imported, fmt.Errorf("invalid media filename in import: %q", name)
			}
		}
		imgFront := fe.ImageFront
		imgBack := fe.ImageBack
		if len(fe.ImageFrontData) > 0 {
			saved, err := saveImageData(mediaPath, fe.ImageFrontData)
			if err != nil {
				return deck, imported, err
			}
			imgFront = saved
		}
		if len(fe.ImageBackData) > 0 {
			saved, err := saveImageData(mediaPath, fe.ImageBackData)
			if err != nil {
				return deck, imported, err
			}
			imgBack = saved
		}
		audFront := fe.AudioFront
		audBack := fe.AudioBack
		if len(fe.AudioFrontData) > 0 {
			ext := filepath.Ext(fe.AudioFront)
			if ext == "" {
				ext = ".mp3"
			}
			saved, err := saveAudioData(mediaPath, fe.AudioFrontData, ext)
			if err != nil {
				return deck, imported, err
			}
			audFront = saved
		}
		if len(fe.AudioBackData) > 0 {
			ext := filepath.Ext(fe.AudioBack)
			if ext == "" {
				ext = ".mp3"
			}
			saved, err := saveAudioData(mediaPath, fe.AudioBackData, ext)
			if err != nil {
				return deck, imported, err
			}
			audBack = saved
		}
		if _, err := s.cards.AddFact(ctx, userID, fe.Front, fe.Back, deck.ID, -1, fe.Reverse, imgFront, imgBack, audFront, audBack); err != nil {
			return deck, imported, err
		}
		imported++
	}
	return deck, imported, nil
}

// ImportGroup creates a new group (or assigns decks to an existing group when
// groupID is non-empty) and imports all decks from the export.
func (s *Service) ImportGroup(ctx context.Context, userID string, exp *GroupExport, groupID string) (*srs.Group, int, error) {
	var group *srs.Group
	var err error
	if groupID == "" {
		group, err = s.cards.AddGroup(ctx, userID, exp.Name)
		if err != nil {
			return nil, 0, err
		}
	} else {
		group, err = s.cards.GetGroup(ctx, userID, groupID)
		if err != nil {
			return nil, 0, err
		}
	}
	imported := 0
	for i := range exp.Decks {
		deck, n, err := s.ImportDeck(ctx, userID, &exp.Decks[i], "")
		if err != nil {
			return group, imported, err
		}
		if err := s.cards.SetDeckGroup(ctx, userID, deck.ID, group.ID); err != nil {
			return group, imported, err
		}
		imported += n
	}
	return group, imported, nil
}
