package cards

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/contre95/stella/src/features/config"
	"github.com/contre95/stella/src/srs"
	"github.com/google/uuid"
)

// Service holds the cards business logic.
type Service struct {
	repo      srs.Repository
	scheduler *srs.Scheduler
	cfg       *config.Manager
}

// NewService constructs the cards service.
func NewService(repo srs.Repository, scheduler *srs.Scheduler, cfg *config.Manager) *Service {
	return &Service{repo: repo, scheduler: scheduler, cfg: cfg}
}

// ImagesPath returns the directory where card images are stored.
func (s *Service) ImagesPath() string {
	return s.cfg.Get().ImagesPath
}

// saveImageData writes raw image bytes to a new file in imagesPath and returns
// the generated filename. The extension is inferred from the content.
func saveImageData(imagesPath string, data []byte) (string, error) {
	if err := os.MkdirAll(imagesPath, 0o755); err != nil {
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
	if err := os.WriteFile(filepath.Join(imagesPath, filename), data, 0o644); err != nil {
		return "", err
	}
	return filename, nil
}

// AddFact creates a fact and its sister cards. A forward card is always made; a
// reverse card is added when reverse is true and a back exists. When grade >= 0
// the cards are given that initial grade (counted as a first repetition);
// grade < 0 leaves them unseen for the review process to grade.
// imageFront and imageBack are filenames of already-saved images (empty means no image).
func (s *Service) AddFact(ctx context.Context, front, back, deckID string, grade int, reverse bool, imageFront, imageBack string) (*srs.Fact, error) {
	front = strings.TrimSpace(front)
	back = strings.TrimSpace(back)
	now := time.Now().Unix()
	fact := &srs.Fact{
		ID:         uuid.NewString(),
		Front:      front,
		Back:       back,
		ImageFront: imageFront,
		ImageBack:  imageBack,
		DeckID:     deckID,
		Created:    now,
		Modified:   now,
	}
	if err := s.repo.AddFact(ctx, fact); err != nil {
		return nil, err
	}

	cards := []*srs.Card{newCard(fact, srs.ViewForward)}
	if reverse && back != "" {
		cards = append(cards, newCard(fact, srs.ViewReverse))
	}
	if grade >= 0 {
		s.scheduler.SetInitialGrade(cards, grade)
	}
	for _, c := range cards {
		if err := s.repo.AddCard(ctx, c); err != nil {
			return nil, err
		}
	}
	return fact, nil
}

// ListFacts returns every fact with its sister cards and their statuses,
// newest first.
func (s *Service) ListFacts(ctx context.Context) ([]*FactRow, error) {
	facts, err := s.repo.ListFacts(ctx)
	if err != nil {
		return nil, err
	}
	decks, err := s.repo.ListDecks(ctx)
	if err != nil {
		return nil, err
	}
	deckMap := make(map[string]*srs.Deck, len(decks))
	for _, d := range decks {
		deckMap[d.ID] = d
	}
	now := s.scheduler.AdjustedNow(time.Now())
	rows := make([]*FactRow, 0, len(facts))
	for _, f := range facts {
		cards, err := s.repo.CardsForFact(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		cardRows := make([]*CardRow, 0, len(cards))
		for _, c := range cards {
			cardRows = append(cardRows, &CardRow{Card: c, Status: s.status(c, now)})
		}
		dk := deckMap[f.DeckID]
		dn, dc := "", ""
		if dk != nil {
			dn, dc = dk.Name, dk.Color
		}
		rows = append(rows, &FactRow{Fact: f, DeckName: dn, DeckColor: dc, Cards: cardRows})
	}
	return rows, nil
}

// DeleteFact removes a fact and (via cascade) its cards.
func (s *Service) DeleteFact(ctx context.Context, id string) error {
	return s.repo.DeleteFact(ctx, id)
}

// ResetFact resets all sister cards of a fact back to unseen.
func (s *Service) ResetFact(ctx context.Context, factID string) error {
	cards, err := s.repo.CardsForFact(ctx, factID)
	if err != nil {
		return err
	}
	for _, c := range cards {
		c.Grade = srs.GradeUnseen
		c.AcqReps = 0
		c.RetReps = 0
		c.Lapses = 0
		c.LastRep = 0
		c.NextRep = 0
		if err := s.repo.UpdateCard(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// ListDecks returns all decks ordered by name.
func (s *Service) ListDecks(ctx context.Context) ([]*srs.Deck, error) {
	return s.repo.ListDecks(ctx)
}

// DeleteDeck removes a deck and all its facts (which cascade to cards).
func (s *Service) DeleteDeck(ctx context.Context, id string) error {
	return s.repo.DeleteDeck(ctx, id)
}

var deckColors = []string{
	"#f472b6", "#a78bfa", "#60a5fa", "#34d399", "#fbbf24",
	"#fb923c", "#f87171", "#2dd4bf", "#818cf8", "#e879f9",
	"#6ee7b7", "#38bdf8", "#f97316", "#a855f7", "#14b8a6",
}

// AddDeck creates a new named deck with a random colour.
func (s *Service) AddDeck(ctx context.Context, name, description string) (*srs.Deck, error) {
	all, _ := s.repo.ListDecks(ctx)
	c := deckColors[len(all)%len(deckColors)]
	d := &srs.Deck{ID: uuid.NewString(), Name: name, Color: c, Description: description}
	if err := s.repo.AddDeck(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// ListFactsByDeck returns facts belonging to deckID with their cards and statuses.
func (s *Service) ListFactsByDeck(ctx context.Context, deckID string) ([]*FactRow, error) {
	facts, err := s.repo.ListFactsByDeck(ctx, deckID)
	if err != nil {
		return nil, err
	}
	deck, _ := s.repo.GetDeck(ctx, deckID)
	dn, dc := "", ""
	if deck != nil {
		dn, dc = deck.Name, deck.Color
	}
	now := s.scheduler.AdjustedNow(time.Now())
	rows := make([]*FactRow, 0, len(facts))
	for _, f := range facts {
		cards, err := s.repo.CardsForFact(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		cardRows := make([]*CardRow, 0, len(cards))
		for _, c := range cards {
			cardRows = append(cardRows, &CardRow{Card: c, Status: s.status(c, now)})
		}
		rows = append(rows, &FactRow{Fact: f, DeckName: dn, DeckColor: dc, Cards: cardRows})
	}
	return rows, nil
}

// DeckExport is the JSON-serialisable representation of a deck plus its facts.
type DeckExport struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Facts       []FactExport  `json:"facts"`
}

// FactExport is the JSON-serialisable representation of a single fact.
type FactExport struct {
	Front          string `json:"front"`
	Back           string `json:"back"`
	ImageFront     string `json:"image_front,omitempty"`
	ImageBack      string `json:"image_back,omitempty"`
	ImageFrontData []byte `json:"image_front_data,omitempty"`
	ImageBackData  []byte `json:"image_back_data,omitempty"`
	Reverse        bool   `json:"reverse"`
}

// ExportDeck returns a DeckExport for the given deck ID.
func (s *Service) ExportDeck(ctx context.Context, deckID string) (*DeckExport, error) {
	deck, err := s.repo.GetDeck(ctx, deckID)
	if err != nil {
		return nil, err
	}
	facts, err := s.repo.ListFactsByDeck(ctx, deckID)
	if err != nil {
		return nil, err
	}
	exp := &DeckExport{Name: deck.Name, Description: deck.Description, Facts: make([]FactExport, 0, len(facts))}
	for _, f := range facts {
		cards, err := s.repo.CardsForFact(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		hasReverse := false
		for _, c := range cards {
			if c.View == srs.ViewReverse {
				hasReverse = true
				break
			}
		}
		fe := FactExport{Front: f.Front, Back: f.Back, ImageFront: f.ImageFront, ImageBack: f.ImageBack, Reverse: hasReverse}
		imagesPath := s.cfg.Get().ImagesPath
		if f.ImageFront != "" {
			if data, err := os.ReadFile(filepath.Join(imagesPath, f.ImageFront)); err == nil {
				fe.ImageFrontData = data
			}
		}
		if f.ImageBack != "" {
			if data, err := os.ReadFile(filepath.Join(imagesPath, f.ImageBack)); err == nil {
				fe.ImageBackData = data
			}
		}
		exp.Facts = append(exp.Facts, fe)
	}
	return exp, nil
}

// ImportDeck creates a new deck from a DeckExport. If name is non-empty it
// overrides the name stored in the export.
func (s *Service) ImportDeck(ctx context.Context, exp *DeckExport, nameOverride string) (*srs.Deck, int, error) {
	name := exp.Name
	if nameOverride != "" {
		name = nameOverride
	}
	deck, err := s.AddDeck(ctx, name, exp.Description)
	if err != nil {
		return nil, 0, err
	}
	imagesPath := s.cfg.Get().ImagesPath
	imported := 0
	for _, fe := range exp.Facts {
		imgFront := fe.ImageFront
		imgBack := fe.ImageBack
		if len(fe.ImageFrontData) > 0 {
			name, err := saveImageData(imagesPath, fe.ImageFrontData)
			if err != nil {
				return deck, imported, err
			}
			imgFront = name
		}
		if len(fe.ImageBackData) > 0 {
			name, err := saveImageData(imagesPath, fe.ImageBackData)
			if err != nil {
				return deck, imported, err
			}
			imgBack = name
		}
		if _, err := s.AddFact(ctx, fe.Front, fe.Back, deck.ID, -1, fe.Reverse, imgFront, imgBack); err != nil {
			return deck, imported, err
		}
		imported++
	}
	return deck, imported, nil
}

// status renders a short scheduling label for a card.
func (s *Service) status(c *srs.Card, now int64) string {
	switch {
	case c.Grade == srs.GradeUnseen:
		return "unseen"
	case c.Grade < 2:
		return "learning"
	default:
		return s.scheduler.NextRepToIntervalString(c.NextRep, now)
	}
}

func newCard(fact *srs.Fact, view srs.CardView) *srs.Card {
	c := &srs.Card{
		ID:     uuid.NewString(),
		FactID: fact.ID,
		DeckID: fact.DeckID,
		View:   view,
		Active: true,
	}
	c.ResetLearningData()
	return c
}
