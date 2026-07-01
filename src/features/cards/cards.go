// Package cards is the feature for creating and browsing facts and their
// generated sister cards.
package cards

import "github.com/contre95/stella/src/srs"

// FactRow bundles a fact with its sister cards for display in the UI.
type FactRow struct {
	Fact     *srs.Fact
	DeckName string
	Cards    []*CardRow
}

// CardRow is a card plus a human-readable scheduling status.
type CardRow struct {
	Card   *srs.Card
	Status string // "unseen", "learning", or an interval string like "in 3 days"
}
