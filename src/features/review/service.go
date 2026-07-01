package review

import (
	"context"
	"time"

	"github.com/contre95/stella/src/srs"
	"github.com/google/uuid"
)

// Stats are the aggregate counts shown on the dashboard.
type Stats struct {
	DueToday   int
	TotalCards int
	NewCards   int
	DeckCount  int
}

// CardInSession pairs a card with its parent fact and session metadata.
type CardInSession struct {
	Card      *srs.Card
	Fact      *srs.Fact
	Remaining int
	DeckName  string
	DeckColor string
}

// Service drives the review loop: queue selection, grading, and aggregate stats.
type Service struct {
	repo      srs.Repository
	scheduler *srs.Scheduler
}

// NewService constructs the review service.
func NewService(repo srs.Repository, scheduler *srs.Scheduler) *Service {
	return &Service{repo: repo, scheduler: scheduler}
}

// NextCard returns the highest-priority card to review, or nil when the queue
// is empty. deckIDs restricts the session to those decks; nil means all decks.
// Queue priority mirrors Mnemosyne's SM2:
//  1. Relearn grade-0 (failed immediately after grading, need re-test now)
//  2. Relearn grade-1 (lapsed retention cards brought back to acquisition)
//  3. Retention reps due today
//  4. Acquisition-phase cards already in hand (grade 1, no lapses)
//  5. Unseen cards, introduced up to NonMemorisedCardsInHand
//  6. Learn-ahead (nothing else scheduled)
func (s *Service) NextCard(ctx context.Context, deckIDs []string) (*CardInSession, error) {
	now := s.scheduler.AdjustedNow(time.Now())

	card, err := s.pickNext(ctx, now, deckIDs)
	if err != nil {
		return nil, err
	}
	if card == nil {
		return nil, nil
	}

	fact, err := s.repo.GetFact(ctx, card.FactID)
	if err != nil {
		return nil, err
	}

	remaining, err := s.queueSize(ctx, now, deckIDs)
	if err != nil {
		return nil, err
	}

	dn, dc := "", ""
	if d, err := s.repo.GetDeck(ctx, card.DeckID); err == nil && d != nil {
		dn, dc = d.Name, d.Color
	}

	return &CardInSession{Card: card, Fact: fact, Remaining: remaining, DeckName: dn, DeckColor: dc}, nil
}

// Grade applies a grade to a card, persists the updated scheduling state, logs
// the review event, and shifts next_rep by one day when a sister card is
// already scheduled on the same midnight-UTC value (sister-card avoidance).
func (s *Service) Grade(ctx context.Context, cardID string, grade int) error {
	card, err := s.repo.GetCard(ctx, cardID)
	if err != nil {
		return err
	}

	scheduledInterval := card.NextRep - card.LastRep
	nowUnix := time.Now().Unix()
	actualInterval := nowUnix - card.LastRep
	if card.Grade == srs.GradeUnseen {
		actualInterval = 0
	}

	s.scheduler.GradeAnswer(card, grade, false)

	if grade >= 2 && card.NextRep > 0 {
		count, err := s.repo.SisterCardCountScheduledBetween(
			ctx, card.FactID, card.ID, card.NextRep, card.NextRep+srs.DAY)
		if err != nil {
			return err
		}
		if count > 0 {
			card.NextRep += srs.DAY
		}
	}

	if err := s.repo.UpdateCard(ctx, card); err != nil {
		return err
	}

	return s.repo.AddReview(ctx, &srs.Review{
		ID:                uuid.NewString(),
		CardID:            card.ID,
		Grade:             grade,
		ScheduledInterval: scheduledInterval,
		ActualInterval:    actualInterval,
		ThinkingTime:      0,
		Timestamp:         nowUnix,
	})
}

// Stats returns aggregate counts for the dashboard (across all decks).
func (s *Service) Stats(ctx context.Context) (*Stats, error) {
	now := s.scheduler.AdjustedNow(time.Now())
	due, err := s.repo.ScheduledCount(ctx, now, nil)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.ActiveCount(ctx)
	if err != nil {
		return nil, err
	}
	newCards, err := s.repo.NonMemorisedCount(ctx, nil)
	if err != nil {
		return nil, err
	}
	decks, err := s.repo.ListDecks(ctx)
	if err != nil {
		return nil, err
	}
	return &Stats{DueToday: due, TotalCards: total, NewCards: newCards, DeckCount: len(decks)}, nil
}

func (s *Service) pickNext(ctx context.Context, now int64, deckIDs []string) (*srs.Card, error) {
	// 1. Relearn: grade-0 cards need an immediate re-test.
	cards, err := s.repo.CardsToRelearn(ctx, 0, "interval", deckIDs)
	if err != nil {
		return nil, err
	}
	if len(cards) > 0 {
		return cards[0], nil
	}

	// 2. Relearn: lapsed retention cards now back in grade-1 acquisition.
	cards, err = s.repo.CardsToRelearn(ctx, 1, "interval", deckIDs)
	if err != nil {
		return nil, err
	}
	if len(cards) > 0 {
		return cards[0], nil
	}

	// 3. Retention reps due today.
	cards, err = s.repo.CardsDueForRetRep(ctx, now, "interval", 1, deckIDs)
	if err != nil {
		return nil, err
	}
	if len(cards) > 0 {
		return cards[0], nil
	}

	// 4. Cards already in the memorising hand (grade 1, no lapses).
	memorising1, err := s.repo.CardsNewMemorising(ctx, 1, deckIDs)
	if err != nil {
		return nil, err
	}
	if len(memorising1) > 0 {
		return memorising1[0], nil
	}

	// 5. Introduce unseen cards up to the hand limit.
	memorising0, err := s.repo.CardsNewMemorising(ctx, 0, deckIDs)
	if err != nil {
		return nil, err
	}
	inHand := len(memorising0) + len(memorising1)
	if inHand < s.scheduler.Params.NonMemorisedCardsInHand {
		unseen, err := s.repo.CardsUnseen(ctx, "", 1, deckIDs)
		if err != nil {
			return nil, err
		}
		if len(unseen) > 0 {
			return unseen[0], nil
		}
	}

	// 6. Nothing due today — offer learn-ahead.
	cards, err = s.repo.CardsLearnAhead(ctx, now, "interval", 1, deckIDs)
	if err != nil {
		return nil, err
	}
	if len(cards) > 0 {
		return cards[0], nil
	}

	return nil, nil
}

// queueSize estimates how many actionable cards remain in the session.
func (s *Service) queueSize(ctx context.Context, now int64, deckIDs []string) (int, error) {
	due, err := s.repo.ScheduledCount(ctx, now, deckIDs)
	if err != nil {
		return 0, err
	}
	nm, err := s.repo.NonMemorisedCount(ctx, deckIDs)
	if err != nil {
		return 0, err
	}
	cap := s.scheduler.Params.NonMemorisedCardsInHand
	if nm > cap {
		nm = cap
	}
	return due + nm, nil
}
