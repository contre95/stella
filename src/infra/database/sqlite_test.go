package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/contre95/stella/src/srs"
)

func newTestDB(t *testing.T) *SqliteSRS {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := NewSqliteSRS(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func addFact(t *testing.T, db *SqliteSRS, id string) {
	t.Helper()
	ctx := context.Background()
	if err := db.AddFact(ctx, &srs.Fact{ID: id, Front: "f-" + id, Back: "b-" + id, Created: 1, Modified: 1}); err != nil {
		t.Fatalf("add fact: %v", err)
	}
}

func addCard(t *testing.T, db *SqliteSRS, c *srs.Card) {
	t.Helper()
	if err := db.AddCard(context.Background(), c); err != nil {
		t.Fatalf("add card: %v", err)
	}
}

func TestFactCardRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addFact(t, db, "fact1")
	card := &srs.Card{ID: "c1", FactID: "fact1", View: srs.ViewForward, Grade: srs.GradeUnseen, Easiness: 2.5, NextRep: -1, LastRep: -1, Active: true}
	addCard(t, db, card)

	got, err := db.GetCard(ctx, "c1")
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if *got != *card {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", *got, *card)
	}

	card.Grade = 4
	card.Easiness = 2.34
	card.NextRep = 1000
	if err := db.UpdateCard(ctx, card); err != nil {
		t.Fatalf("update card: %v", err)
	}
	got, _ = db.GetCard(ctx, "c1")
	if got.Grade != 4 || got.Easiness != 2.34 || got.NextRep != 1000 {
		t.Errorf("update not persisted: %+v", *got)
	}
}

func TestGetCardNotFound(t *testing.T) {
	db := newTestDB(t)
	_, err := db.GetCard(context.Background(), "missing")
	if !errors.Is(err, srs.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestQueueQueries(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addFact(t, db, "f1")
	addFact(t, db, "f2")
	addFact(t, db, "f3")

	// Unseen card.
	addCard(t, db, &srs.Card{ID: "unseen", FactID: "f1", View: srs.ViewForward, Grade: srs.GradeUnseen, Easiness: 2.5, Active: true})
	// Retained card due in the past.
	addCard(t, db, &srs.Card{ID: "due", FactID: "f2", View: srs.ViewForward, Grade: 4, Easiness: 2.5, NextRep: 500, LastRep: 100, Active: true})
	// Retained card due in the future.
	addCard(t, db, &srs.Card{ID: "future", FactID: "f3", View: srs.ViewForward, Grade: 4, Easiness: 2.5, NextRep: 5000, LastRep: 100, Active: true})

	if n, _ := db.ActiveCount(ctx); n != 3 {
		t.Errorf("ActiveCount got %d want 3", n)
	}
	if n, _ := db.NonMemorisedCount(ctx, nil); n != 1 {
		t.Errorf("NonMemorisedCount got %d want 1", n)
	}
	if n, _ := db.ScheduledCount(ctx, 1000, nil); n != 1 {
		t.Errorf("ScheduledCount got %d want 1", n)
	}

	unseen, _ := db.CardsUnseen(ctx, "", -1, nil)
	if len(unseen) != 1 || unseen[0].ID != "unseen" {
		t.Errorf("CardsUnseen got %v", ids(unseen))
	}
	due, _ := db.CardsDueForRetRep(ctx, 1000, "interval", -1, nil)
	if len(due) != 1 || due[0].ID != "due" {
		t.Errorf("CardsDueForRetRep got %v", ids(due))
	}
	ahead, _ := db.CardsLearnAhead(ctx, 1000, "next_rep", -1, nil)
	if len(ahead) != 1 || ahead[0].ID != "future" {
		t.Errorf("CardsLearnAhead got %v", ids(ahead))
	}
}

func TestRelearnVsNewMemorising(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addFact(t, db, "f1")
	addFact(t, db, "f2")
	// grade 1, lapses>0 => relearn.
	addCard(t, db, &srs.Card{ID: "relearn", FactID: "f1", View: srs.ViewForward, Grade: 1, Easiness: 2.5, Lapses: 2, Active: true})
	// grade 1, lapses=0 => new memorising.
	addCard(t, db, &srs.Card{ID: "newmem", FactID: "f2", View: srs.ViewForward, Grade: 1, Easiness: 2.5, Lapses: 0, Active: true})

	relearn, _ := db.CardsToRelearn(ctx, 1, "-interval", nil)
	if len(relearn) != 1 || relearn[0].ID != "relearn" {
		t.Errorf("CardsToRelearn got %v", ids(relearn))
	}
	newmem, _ := db.CardsNewMemorising(ctx, 1, nil)
	if len(newmem) != 1 || newmem[0].ID != "newmem" {
		t.Errorf("CardsNewMemorising got %v", ids(newmem))
	}
}

func TestSisterCardCountAndCascade(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addFact(t, db, "f1")
	// Two sister cards of the same fact, both retained and due the same day.
	addCard(t, db, &srs.Card{ID: "fwd", FactID: "f1", View: srs.ViewForward, Grade: 4, Easiness: 2.5, NextRep: 1000, Active: true})
	addCard(t, db, &srs.Card{ID: "rev", FactID: "f1", View: srs.ViewReverse, Grade: 4, Easiness: 2.5, NextRep: 1500, Active: true})

	// From fwd's perspective, the reverse sister is scheduled in [1000, 2000).
	n, err := db.SisterCardCountScheduledBetween(ctx, "f1", "fwd", 1000, 2000)
	if err != nil {
		t.Fatalf("sister count: %v", err)
	}
	if n != 1 {
		t.Errorf("sister count got %d want 1", n)
	}

	// Deleting the fact cascades to its cards.
	if err := db.DeleteFact(ctx, "f1"); err != nil {
		t.Fatalf("delete fact: %v", err)
	}
	if n, _ := db.ActiveCount(ctx); n != 0 {
		t.Errorf("cards not cascaded on fact delete: %d remain", n)
	}
}

func ids(cards []*srs.Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}
