package cards

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/contre95/stella/src/features/config"
	"github.com/contre95/stella/src/srs"
	"github.com/google/uuid"
)

// Service holds the cards business logic. Every method operates on behalf of
// one user: userID scopes all repository access.
type Service struct {
	repo      srs.Repository
	scheduler *srs.Scheduler
	cfg       *config.Manager
}

// NewService constructs the cards service.
func NewService(repo srs.Repository, scheduler *srs.Scheduler, cfg *config.Manager) *Service {
	return &Service{repo: repo, scheduler: scheduler, cfg: cfg}
}

// ImagePath returns the directory where card images are stored.
func (s *Service) ImagePath() string {
	return s.cfg.Get().ImagePath
}

// AddFact creates a fact and its sister cards. A forward card is always made; a
// reverse card is added when reverse is true and a back exists. When grade >= 0
// the cards are given that initial grade (counted as a first repetition);
// grade < 0 leaves them unseen for the review process to grade.
// imageFront/imageBack and audioFront/audioBack are filenames of already-saved
// media files; empty means none.
func (s *Service) AddFact(ctx context.Context, userID, front, back, deckID string, grade int, reverse bool, imageFront, imageBack, audioFront, audioBack string) (*srs.Fact, error) {
	front = strings.TrimSpace(front)
	back = strings.TrimSpace(back)
	deckID = strings.TrimSpace(deckID)
	if deckID == "" {
		return nil, fmt.Errorf("deckID is required: every card must belong to a deck")
	}
	if _, err := s.repo.GetDeck(ctx, userID, deckID); err != nil {
		if errors.Is(err, srs.ErrNotFound) {
			return nil, fmt.Errorf("deck %q not found", deckID)
		}
		return nil, fmt.Errorf("looking up deck: %w", err)
	}
	now := time.Now().Unix()
	fact := &srs.Fact{
		ID:         uuid.NewString(),
		UserID:     userID,
		Front:      front,
		Back:       back,
		ImageFront: imageFront,
		ImageBack:  imageBack,
		AudioFront: audioFront,
		AudioBack:  audioBack,
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
		// Mnemosyne counts initial grading as a repetition and logs it, so the
		// fact registers as "memorised today" for the queue's sister avoidance.
		if grade >= 0 {
			if err := s.repo.AddReview(ctx, &srs.Review{
				ID:        uuid.NewString(),
				UserID:    userID,
				CardID:    c.ID,
				Grade:     grade,
				RetReps:   c.RetReps,
				Timestamp: now,
			}); err != nil {
				return nil, err
			}
		}
	}
	return fact, nil
}

// GetFact retrieves a single fact by ID.
func (s *Service) GetFact(ctx context.Context, userID, id string) (*srs.Fact, error) {
	return s.repo.GetFact(ctx, userID, id)
}

// HasReverse reports whether the fact has a reverse sister card.
func (s *Service) HasReverse(ctx context.Context, userID, factID string) (bool, error) {
	cards, err := s.repo.CardsForFact(ctx, userID, factID)
	if err != nil {
		return false, err
	}
	for _, c := range cards {
		if c.View == srs.ViewReverse {
			return true, nil
		}
	}
	return false, nil
}

// UpdateFact edits a fact's content without touching its cards' learning data.
// front and back replace the current text (front must be non-empty). Each media
// argument is nil to keep the current file, a pointer to "" to remove it, or a
// pointer to an already-saved filename to replace it; replaced and removed
// files are deleted from disk. reverse is nil to leave the sister cards alone,
// otherwise the reverse card is created (unseen) or deleted to match it. A
// fact whose back is empty never keeps a reverse card.
func (s *Service) UpdateFact(ctx context.Context, userID, factID, front, back string, imageFront, imageBack, audioFront, audioBack *string, reverse *bool) (*srs.Fact, error) {
	front = strings.TrimSpace(front)
	back = strings.TrimSpace(back)
	if front == "" {
		return nil, fmt.Errorf("front is required")
	}
	fact, err := s.repo.GetFact(ctx, userID, factID)
	if err != nil {
		if errors.Is(err, srs.ErrNotFound) {
			return nil, fmt.Errorf("fact %q not found", factID)
		}
		return nil, err
	}
	var stale []string
	apply := func(field *string, update *string) {
		if update == nil {
			return
		}
		if *field != "" && *field != *update {
			stale = append(stale, *field)
		}
		*field = *update
	}
	apply(&fact.ImageFront, imageFront)
	apply(&fact.ImageBack, imageBack)
	apply(&fact.AudioFront, audioFront)
	apply(&fact.AudioBack, audioBack)
	fact.Front = front
	fact.Back = back
	fact.Modified = time.Now().Unix()
	if err := s.repo.UpdateFact(ctx, fact); err != nil {
		return nil, err
	}

	cards, err := s.repo.CardsForFact(ctx, userID, factID)
	if err != nil {
		return nil, err
	}
	var revCard *srs.Card
	for _, c := range cards {
		if c.View == srs.ViewReverse {
			revCard = c
			break
		}
	}
	wantReverse := revCard != nil
	if reverse != nil {
		wantReverse = *reverse
	}
	if back == "" {
		wantReverse = false
	}
	switch {
	case wantReverse && revCard == nil:
		if err := s.repo.AddCard(ctx, newCard(fact, srs.ViewReverse)); err != nil {
			return nil, err
		}
	case !wantReverse && revCard != nil:
		if err := s.repo.DeleteCard(ctx, userID, revCard.ID); err != nil {
			return nil, err
		}
	}

	for _, f := range stale {
		os.Remove(filepath.Join(s.cfg.Get().ImagePath, f))
	}
	return fact, nil
}

// ListFacts returns every fact with its sister cards and their statuses,
// newest first.
func (s *Service) ListFacts(ctx context.Context, userID string) ([]*FactRow, error) {
	facts, err := s.repo.ListFacts(ctx, userID)
	if err != nil {
		return nil, err
	}
	decks, err := s.repo.ListDecks(ctx, userID)
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
		cards, err := s.repo.CardsForFact(ctx, userID, f.ID)
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
func (s *Service) DeleteFact(ctx context.Context, userID, id string) error {
	return s.repo.DeleteFact(ctx, userID, id)
}

// ResetFact resets all sister cards of a fact back to unseen.
func (s *Service) ResetFact(ctx context.Context, userID, factID string) error {
	cards, err := s.repo.CardsForFact(ctx, userID, factID)
	if err != nil {
		return err
	}
	for _, c := range cards {
		c.ResetLearningData()
		if err := s.repo.UpdateCard(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// ResetDeck resets all cards in every fact belonging to the deck back to unseen.
func (s *Service) ResetDeck(ctx context.Context, userID, deckID string) error {
	facts, err := s.repo.ListFactsByDeck(ctx, userID, deckID)
	if err != nil {
		return err
	}
	for _, f := range facts {
		if err := s.ResetFact(ctx, userID, f.ID); err != nil {
			return err
		}
	}
	return nil
}

// ListDecks returns all decks ordered by name.
func (s *Service) ListDecks(ctx context.Context, userID string) ([]*srs.Deck, error) {
	return s.repo.ListDecks(ctx, userID)
}

// GetDeck retrieves a single deck by ID.
func (s *Service) GetDeck(ctx context.Context, userID, id string) (*srs.Deck, error) {
	return s.repo.GetDeck(ctx, userID, id)
}

// DeleteDeck removes a deck and all its facts (which cascade to cards), along
// with its cover picture file if it has one.
func (s *Service) DeleteDeck(ctx context.Context, userID, id string) error {
	deck, _ := s.repo.GetDeck(ctx, userID, id)
	if err := s.repo.DeleteDeck(ctx, userID, id); err != nil {
		return err
	}
	if deck != nil && deck.Image != "" {
		os.Remove(filepath.Join(s.cfg.Get().ImagePath, deck.Image))
	}
	return nil
}

// RenameDeck updates the name of a deck.
func (s *Service) RenameDeck(ctx context.Context, userID, id, name string) (*srs.Deck, error) {
	deck, err := s.repo.GetDeck(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	deck.Name = name
	if err := s.repo.UpdateDeck(ctx, deck); err != nil {
		return nil, err
	}
	return deck, nil
}

// UpdateDeckDescription updates the description of a deck.
func (s *Service) UpdateDeckDescription(ctx context.Context, userID, id, description string) (*srs.Deck, error) {
	deck, err := s.repo.GetDeck(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	deck.Description = description
	if err := s.repo.UpdateDeck(ctx, deck); err != nil {
		return nil, err
	}
	return deck, nil
}

// SetDeckImage assigns an already-saved media filename as the deck's cover
// picture and returns the previous filename so callers can clean it up.
func (s *Service) SetDeckImage(ctx context.Context, userID, id, filename string) (string, error) {
	deck, err := s.repo.GetDeck(ctx, userID, id)
	if err != nil {
		return "", err
	}
	old := deck.Image
	deck.Image = filename
	if err := s.repo.UpdateDeck(ctx, deck); err != nil {
		return "", err
	}
	return old, nil
}

var deckColors = []string{
	"#f472b6", "#a78bfa", "#60a5fa", "#34d399", "#fbbf24",
	"#fb923c", "#f87171", "#2dd4bf", "#818cf8", "#e879f9",
	"#6ee7b7", "#38bdf8", "#f97316", "#a855f7", "#14b8a6",
}

// AddDeck creates a new named deck with a random colour. image is the filename
// of an already-saved cover picture; empty means none. groupID assigns the
// deck to a group; empty means no group.
func (s *Service) AddDeck(ctx context.Context, userID, name, description, image, groupID string) (*srs.Deck, error) {
	all, _ := s.repo.ListDecks(ctx, userID)
	c := deckColors[len(all)%len(deckColors)]
	d := &srs.Deck{ID: uuid.NewString(), UserID: userID, Name: name, Color: c, Description: description, Image: image, GroupID: groupID, StudyEnabled: true}
	if err := s.repo.AddDeck(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// AddGroup creates a new group with the given name.
func (s *Service) AddGroup(ctx context.Context, userID, name string) (*srs.Group, error) {
	g := &srs.Group{ID: uuid.NewString(), UserID: userID, Name: name}
	if err := s.repo.AddGroup(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

// GetGroup retrieves a single group by ID.
func (s *Service) GetGroup(ctx context.Context, userID, id string) (*srs.Group, error) {
	return s.repo.GetGroup(ctx, userID, id)
}

// ListGroups returns all groups ordered by name.
func (s *Service) ListGroups(ctx context.Context, userID string) ([]*srs.Group, error) {
	return s.repo.ListGroups(ctx, userID)
}

// UpdateGroup renames a group.
func (s *Service) UpdateGroup(ctx context.Context, userID, id, name string) (*srs.Group, error) {
	g, err := s.repo.GetGroup(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	g.Name = name
	if err := s.repo.UpdateGroup(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

// DeleteGroup removes a group and unassigns all its decks (they become ungrouped).
func (s *Service) DeleteGroup(ctx context.Context, userID, id string) error {
	if err := s.repo.UnsetGroupForDecks(ctx, userID, id); err != nil {
		return err
	}
	return s.repo.DeleteGroup(ctx, userID, id)
}

// SetDeckGroup assigns a deck to a group (or clears its group when groupID is empty).
func (s *Service) SetDeckGroup(ctx context.Context, userID, deckID, groupID string) error {
	deck, err := s.repo.GetDeck(ctx, userID, deckID)
	if err != nil {
		return err
	}
	deck.GroupID = groupID
	return s.repo.UpdateDeck(ctx, deck)
}

// ToggleDeckStudy flips a deck's StudyEnabled flag and returns the updated deck.
func (s *Service) ToggleDeckStudy(ctx context.Context, userID, deckID string) (*srs.Deck, error) {
	deck, err := s.repo.GetDeck(ctx, userID, deckID)
	if err != nil {
		return nil, err
	}
	deck.StudyEnabled = !deck.StudyEnabled
	return deck, s.repo.UpdateDeck(ctx, deck)
}

// ListGroupViews returns decks organised into groups for the deck list page.
// Named groups come first (sorted by name), ungrouped decks are last with a
// nil Group field.
func (s *Service) ListGroupViews(ctx context.Context, userID string) ([]GroupView, error) {
	groups, err := s.repo.ListGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	decks, err := s.repo.ListDecks(ctx, userID)
	if err != nil {
		return nil, err
	}
	byGroup := make(map[string][]*srs.Deck, len(groups))
	var ungrouped []*srs.Deck
	for _, d := range decks {
		if d.GroupID == "" {
			ungrouped = append(ungrouped, d)
		} else {
			byGroup[d.GroupID] = append(byGroup[d.GroupID], d)
		}
	}
	views := make([]GroupView, 0, len(groups)+1)
	for _, g := range groups {
		views = append(views, GroupView{Group: g, Decks: byGroup[g.ID]})
	}
	views = append(views, GroupView{Group: nil, Decks: ungrouped})
	return views, nil
}

// ListFactsByDeck returns facts belonging to deckID with their cards and statuses.
func (s *Service) ListFactsByDeck(ctx context.Context, userID, deckID string) ([]*FactRow, error) {
	facts, err := s.repo.ListFactsByDeck(ctx, userID, deckID)
	if err != nil {
		return nil, err
	}
	deck, _ := s.repo.GetDeck(ctx, userID, deckID)
	dn, dc := "", ""
	if deck != nil {
		dn, dc = deck.Name, deck.Color
	}
	now := s.scheduler.AdjustedNow(time.Now())
	rows := make([]*FactRow, 0, len(facts))
	for _, f := range facts {
		cards, err := s.repo.CardsForFact(ctx, userID, f.ID)
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
		UserID: fact.UserID,
		FactID: fact.ID,
		DeckID: fact.DeckID,
		View:   view,
		Active: true,
	}
	c.ResetLearningData()
	return c
}
