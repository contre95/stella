package srs

import (
	"context"
	"errors"
)

// ErrNotFound is returned by repository lookups when no row matches.
var ErrNotFound = errors.New("not found")

// Repository is the persistence contract for the domain. Every entity belongs
// to a user: Add/Update calls carry the owner in the entity's UserID field,
// and every other method is scoped by an explicit userID so no query can
// cross user boundaries. The queue-selection queries at the bottom mirror the
// database methods Mnemosyne's scheduler depends on; they are the SQL-backed
// half of the engine.
type Repository interface {
	// Facts.
	AddFact(ctx context.Context, f *Fact) error
	GetFact(ctx context.Context, userID, id string) (*Fact, error)
	ListFacts(ctx context.Context, userID string) ([]*Fact, error)
	UpdateFact(ctx context.Context, f *Fact) error
	DeleteFact(ctx context.Context, userID, id string) error // cascades to its cards

	// Cards.
	AddCard(ctx context.Context, c *Card) error
	GetCard(ctx context.Context, userID, id string) (*Card, error)
	UpdateCard(ctx context.Context, c *Card) error
	DeleteCard(ctx context.Context, userID, id string) error
	CardsForFact(ctx context.Context, userID, factID string) ([]*Card, error)

	// Decks.
	AddDeck(ctx context.Context, d *Deck) error
	GetDeck(ctx context.Context, userID, id string) (*Deck, error)
	UpdateDeck(ctx context.Context, d *Deck) error
	ListDecks(ctx context.Context, userID string) ([]*Deck, error)
	DeleteDeck(ctx context.Context, userID, id string) error // deletes deck and all its facts (cascades to cards)
	ListFactsByDeck(ctx context.Context, userID, deckID string) ([]*Fact, error)
	// ListStudyEnabledDeckIDs returns the IDs of all decks with study_enabled=true.
	// Used by the review service to populate the default study queue.
	ListStudyEnabledDeckIDs(ctx context.Context, userID string) ([]string, error)

	// Groups.
	AddGroup(ctx context.Context, g *Group) error
	GetGroup(ctx context.Context, userID, id string) (*Group, error)
	UpdateGroup(ctx context.Context, g *Group) error
	ListGroups(ctx context.Context, userID string) ([]*Group, error)
	DeleteGroup(ctx context.Context, userID, id string) error
	UnsetGroupForDecks(ctx context.Context, userID, groupID string) error

	// Device sync (stella-x4). Devices address decks and cards by their
	// compact sync IDs, never by primary keys.
	GetDeckBySyncID(ctx context.Context, userID, syncID string) (*Deck, error)
	// CardsByDeck returns every card in the deck (active and suspended),
	// used to key the sync state exchange by card sync ID.
	CardsByDeck(ctx context.Context, userID, deckID string) ([]*Card, error)
	// DeckExportRows returns the deck's active cards joined with their fact
	// text, view direction applied, in insertion order, capped at limit.
	DeckExportRows(ctx context.Context, userID, deckID string, limit int) ([]DeckExportRow, error)
	// CardIDsBySyncID maps every card sync ID of the user to its primary key.
	CardIDsBySyncID(ctx context.Context, userID string) (map[string]string, error)
	// AddReviewIfNew inserts a review unless its ID already exists; reports
	// whether a row was inserted. Device uploads retry idempotently.
	AddReviewIfNew(ctx context.Context, r *Review) (bool, error)

	// Reviews (append-only log).
	AddReview(ctx context.Context, r *Review) error
	// ReviewTimestampsSince returns POSIX timestamps of reviews at or after
	// `from`, ascending, for the study-activity heatmap.
	ReviewTimestampsSince(ctx context.Context, userID string, from int64) ([]int64, error)
	// FactIDsLearnedToday returns the fact ids memorised during [from, to):
	// facts newly committed to memory plus facts forgotten and relearned. It is
	// the union of Mnemosyne's fact_ids_newly_learned_today and
	// fact_ids_forgotten_and_learned_today log queries.
	FactIDsLearnedToday(ctx context.Context, userID string, from, to int64) ([]string, error)

	// Aggregate counts.
	ActiveCount(ctx context.Context, userID string) (int, error)
	// deckIDs filters to specific decks; nil or empty means all decks.
	ActiveCountForDecks(ctx context.Context, userID string, deckIDs []string) (int, error)
	// FactCountForDecks counts distinct facts (not cards) for the given decks,
	// so reverse-card pairs are not double-counted.
	FactCountForDecks(ctx context.Context, userID string, deckIDs []string) (int, error)
	ScheduledCount(ctx context.Context, userID string, now int64, deckIDs []string) (int, error)
	NonMemorisedCount(ctx context.Context, userID string, deckIDs []string) (int, error)
	// UnseenCount counts distinct facts with at least one never-reviewed
	// (grade = -1) card; a subset of NonMemorisedCount.
	UnseenCount(ctx context.Context, userID string, deckIDs []string) (int, error)

	// Queue selection. sortKey is one of "", "interval", "-interval",
	// "next_rep", "random"; an empty limit (<=0) means no limit.
	// deckIDs filters to specific decks; nil or empty means all decks.
	CardsDueForRetRep(ctx context.Context, userID string, now int64, sortKey string, limit int, deckIDs []string) ([]*Card, error)
	CardsToRelearn(ctx context.Context, userID string, grade int, sortKey string, deckIDs []string) ([]*Card, error)
	CardsNewMemorising(ctx context.Context, userID string, grade int, deckIDs []string) ([]*Card, error)
	CardsUnseen(ctx context.Context, userID string, sortKey string, limit int, deckIDs []string) ([]*Card, error)
	CardsLearnAhead(ctx context.Context, userID string, now int64, sortKey string, limit int, deckIDs []string) ([]*Card, error)
	// SisterCardCountScheduledBetween counts retained sister cards (same fact,
	// excluding excludeCardID) due in [from, to).
	SisterCardCountScheduledBetween(ctx context.Context, userID, factID, excludeCardID string, from, to int64) (int, error)
}
