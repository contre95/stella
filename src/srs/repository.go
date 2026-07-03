package srs

import (
	"context"
	"errors"
)

// ErrNotFound is returned by repository lookups when no row matches.
var ErrNotFound = errors.New("not found")

// Repository is the persistence contract for the domain. The queue-selection
// queries at the bottom mirror the database methods Mnemosyne's scheduler
// depends on; they are the SQL-backed half of the engine.
type Repository interface {
	// Facts.
	AddFact(ctx context.Context, f *Fact) error
	GetFact(ctx context.Context, id string) (*Fact, error)
	ListFacts(ctx context.Context) ([]*Fact, error)
	UpdateFact(ctx context.Context, f *Fact) error
	DeleteFact(ctx context.Context, id string) error // cascades to its cards

	// Cards.
	AddCard(ctx context.Context, c *Card) error
	GetCard(ctx context.Context, id string) (*Card, error)
	UpdateCard(ctx context.Context, c *Card) error
	DeleteCard(ctx context.Context, id string) error
	CardsForFact(ctx context.Context, factID string) ([]*Card, error)

	// Decks.
	AddDeck(ctx context.Context, d *Deck) error
	GetDeck(ctx context.Context, id string) (*Deck, error)
	UpdateDeck(ctx context.Context, d *Deck) error
	ListDecks(ctx context.Context) ([]*Deck, error)
	DeleteDeck(ctx context.Context, id string) error // deletes deck and all its facts (cascades to cards)
	ListFactsByDeck(ctx context.Context, deckID string) ([]*Fact, error)

	// Reviews (append-only log).
	AddReview(ctx context.Context, r *Review) error
	// ReviewTimestampsSince returns POSIX timestamps of reviews at or after
	// `from`, ascending, for the study-activity heatmap.
	ReviewTimestampsSince(ctx context.Context, from int64) ([]int64, error)
	// FactIDsLearnedToday returns the fact ids memorised during [from, to):
	// facts newly committed to memory plus facts forgotten and relearned. It is
	// the union of Mnemosyne's fact_ids_newly_learned_today and
	// fact_ids_forgotten_and_learned_today log queries.
	FactIDsLearnedToday(ctx context.Context, from, to int64) ([]string, error)

	// Aggregate counts.
	ActiveCount(ctx context.Context) (int, error)
	// deckIDs filters to specific decks; nil or empty means all decks.
	ScheduledCount(ctx context.Context, now int64, deckIDs []string) (int, error)
	NonMemorisedCount(ctx context.Context, deckIDs []string) (int, error)

	// Queue selection. sortKey is one of "", "interval", "-interval",
	// "next_rep", "random"; an empty limit (<=0) means no limit.
	// deckIDs filters to specific decks; nil or empty means all decks.
	CardsDueForRetRep(ctx context.Context, now int64, sortKey string, limit int, deckIDs []string) ([]*Card, error)
	CardsToRelearn(ctx context.Context, grade int, sortKey string, deckIDs []string) ([]*Card, error)
	CardsNewMemorising(ctx context.Context, grade int, deckIDs []string) ([]*Card, error)
	CardsUnseen(ctx context.Context, sortKey string, limit int, deckIDs []string) ([]*Card, error)
	CardsLearnAhead(ctx context.Context, now int64, sortKey string, limit int, deckIDs []string) ([]*Card, error)
	// SisterCardCountScheduledBetween counts retained sister cards (same fact,
	// excluding excludeCardID) due in [from, to).
	SisterCardCountScheduledBetween(ctx context.Context, factID, excludeCardID string, from, to int64) (int, error)
}
