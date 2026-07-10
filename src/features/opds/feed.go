package opds

import (
	"context"
	"fmt"
	"time"

	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/srs"
)

// Service builds OPDS feeds from the cards repository.
type Service struct {
	cards *cards.Service
}

// NewService constructs the OPDS service.
func NewService(cards *cards.Service) *Service {
	return &Service{cards: cards}
}

// CatalogFeed builds the root OPDS catalog feed listing all groups and
// ungrouped decks.
func (s *Service) CatalogFeed(ctx context.Context, userID, baseURL string) (*Feed, error) {
	views, err := s.cards.ListGroupViews(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing group views: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	feed := &Feed{
		Title:   "Stella Deck Catalog",
		ID:      baseURL + "/opds",
		Updated: now,
		Links: []Link{
			{Rel: "self", Href: baseURL + "/opds", Type: "application/atom+xml;profile=opds-catalog"},
			{Rel: "start", Href: baseURL + "/opds", Type: "application/atom+xml;profile=opds-catalog"},
		},
	}

	for _, view := range views {
		if view.Group != nil {
			feed.Entries = append(feed.Entries, s.groupEntry(view.Group, baseURL, now))
		}
		for _, deck := range view.Decks {
			feed.Entries = append(feed.Entries, s.deckEntry(deck, baseURL, now))
		}
	}

	return feed, nil
}

// GroupFeed builds an OPDS feed for a single group, listing its decks.
func (s *Service) GroupFeed(ctx context.Context, userID, groupID, baseURL string) (*Feed, error) {
	group, err := s.cards.GetGroup(ctx, userID, groupID)
	if err != nil {
		return nil, fmt.Errorf("getting group: %w", err)
	}

	facts, err := s.cards.ListFactsByDeck(ctx, userID, groupID)
	if err != nil {
		return nil, fmt.Errorf("listing facts: %w", err)
	}
	_ = facts // facts available if needed for entry summaries

	decks, err := s.cards.ListDecks(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing decks: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	feed := &Feed{
		Title:   group.Name,
		ID:      baseURL + "/opds/groups/" + groupID,
		Updated: now,
		Links: []Link{
			{Rel: "self", Href: baseURL + "/opds/groups/" + groupID, Type: "application/atom+xml;profile=opds-catalog"},
			{Rel: "start", Href: baseURL + "/opds", Type: "application/atom+xml;profile=opds-catalog"},
		},
	}

	for _, deck := range decks {
		if deck.GroupID == groupID {
			feed.Entries = append(feed.Entries, s.deckEntry(deck, baseURL, now))
		}
	}

	return feed, nil
}

// groupEntry creates an OPDS navigation entry for a group.
func (s *Service) groupEntry(group *srs.Group, baseURL, now string) Entry {
	href := baseURL + "/opds/groups/" + group.ID
	return Entry{
		Title:   group.Name,
		ID:      "urn:stella:group:" + group.ID,
		Updated: now,
		Summary: fmt.Sprintf("Group with decks"),
		Links: []Link{
			{Rel: "subsection", Href: href, Type: "application/atom+xml;profile=opds-catalog"},
		},
	}
}

// deckEntry creates an OPDS acquisition entry for a deck.
func (s *Service) deckEntry(deck *srs.Deck, baseURL, now string) Entry {
	exportHref := baseURL + "/decks/" + deck.ID + "/export"
	entry := Entry{
		Title:   deck.Name,
		ID:      "urn:stella:deck:" + deck.ID,
		Updated: now,
		Links: []Link{
			{
				Rel:  "http://opds-spec.org/acquisition/open-access",
				Href: exportHref,
				Type: "application/zip",
			},
		},
	}

	if deck.Description != "" {
		entry.Summary = deck.Description
	}

	if deck.Image != "" {
		coverHref := baseURL + "/images/" + deck.Image
		entry.Links = append(entry.Links, Link{
			Rel:  "http://opds-spec.org/cover/thumbnail",
			Href: coverHref,
			Type: "image/jpeg",
		})
	}

	return entry
}
