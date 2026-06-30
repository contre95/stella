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
	ListDecks(ctx context.Context) ([]*Deck, error)

	// Reviews (append-only log).
	AddReview(ctx context.Context, r *Review) error

	// Aggregate counts.
	ActiveCount(ctx context.Context) (int, error)
	ScheduledCount(ctx context.Context, now int64) (int, error)
	NonMemorisedCount(ctx context.Context) (int, error)

	// Queue selection. sortKey is one of "", "interval", "-interval",
	// "next_rep", "random"; an empty limit (<=0) means no limit.
	CardsDueForRetRep(ctx context.Context, now int64, sortKey string, limit int) ([]*Card, error)
	CardsToRelearn(ctx context.Context, grade int, sortKey string) ([]*Card, error)
	CardsNewMemorising(ctx context.Context, grade int) ([]*Card, error)
	CardsUnseen(ctx context.Context, sortKey string, limit int) ([]*Card, error)
	CardsLearnAhead(ctx context.Context, now int64, sortKey string, limit int) ([]*Card, error)
	// SisterCardCountScheduledBetween counts retained sister cards (same fact,
	// excluding excludeCardID) due in [from, to).
	SisterCardCountScheduledBetween(ctx context.Context, factID, excludeCardID string, from, to int64) (int, error)
}
