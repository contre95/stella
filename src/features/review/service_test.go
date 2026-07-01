package review

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/contre95/stella/src/infra/database"
	"github.com/contre95/stella/src/srs"
)

func newTestService(t *testing.T, now time.Time) (*Service, *database.SqliteSRS) {
	t.Helper()
	db, err := database.NewSqliteSRS(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	sch := srs.NewScheduler(
		srs.Params{DayStartsAt: 3, NonMemorisedCardsInHand: 10},
		time.UTC,
		func() time.Time { return now },
		srs.NewMathRand(1),
	)
	return NewService(db, sch), db
}

func seedDeck(t *testing.T, db *database.SqliteSRS) string {
	t.Helper()
	if err := db.AddDeck(context.Background(), &srs.Deck{ID: "deck1", Name: "Deck", Color: "#fff"}); err != nil {
		t.Fatalf("add deck: %v", err)
	}
	return "deck1"
}

// addCard creates a fact + one forward card, applying mutate to the card.
func addCard(t *testing.T, db *database.SqliteSRS, deckID, factID, cardID string, mutate func(*srs.Card)) {
	t.Helper()
	ctx := context.Background()
	if err := db.AddFact(ctx, &srs.Fact{ID: factID, Front: factID, Back: "b", DeckID: deckID, Created: 1, Modified: 1}); err != nil {
		t.Fatalf("add fact: %v", err)
	}
	c := &srs.Card{ID: cardID, FactID: factID, DeckID: deckID, View: srs.ViewForward, Active: true}
	c.ResetLearningData()
	if mutate != nil {
		mutate(c)
	}
	if err := db.AddCard(ctx, c); err != nil {
		t.Fatalf("add card: %v", err)
	}
}

// A failed (grade-0, lapsed) card must be re-tested before a retention card
// that is merely due — this is stage 2 winning over stage 3 in the queue.
func TestNextCard_RelearnBeatsDue(t *testing.T) {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	svc, db := newTestService(t, now)
	deck := seedDeck(t, db)
	addCard(t, db, deck, "f_due", "c_due", func(c *srs.Card) {
		c.Grade = 4
		c.Easiness = 2.5
		c.LastRep = 100
		c.NextRep = 500
	})
	addCard(t, db, deck, "f_re", "c_re", func(c *srs.Card) {
		c.Grade = 0
		c.Easiness = 2.0
		c.Lapses = 1
		c.LastRep = 100
		c.NextRep = 100
	})

	ci, err := svc.NextCard(context.Background(), nil)
	if err != nil {
		t.Fatalf("next card: %v", err)
	}
	if ci == nil || ci.Card.ID != "c_re" {
		t.Fatalf("want relearn card first, got %v", ci)
	}
}

// Among due retention cards, the shortest interval goes first (being late on a
// 1-day interval is worse than on a 50-day one).
func TestNextCard_ShortestIntervalFirst(t *testing.T) {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	svc, db := newTestService(t, now)
	deck := seedDeck(t, db)
	addCard(t, db, deck, "f_long", "c_long", func(c *srs.Card) {
		c.Grade = 4
		c.Easiness = 2.5
		c.LastRep = 0
		c.NextRep = 500 // interval 500
	})
	addCard(t, db, deck, "f_short", "c_short", func(c *srs.Card) {
		c.Grade = 4
		c.Easiness = 2.5
		c.LastRep = 400
		c.NextRep = 500 // interval 100
	})

	ci, err := svc.NextCard(context.Background(), nil)
	if err != nil {
		t.Fatalf("next card: %v", err)
	}
	if ci == nil || ci.Card.ID != "c_short" {
		t.Fatalf("want shortest-interval card, got %v", ci)
	}
}

// Grading an unseen card to 5 persists the new state and schedules it into the
// future, so it is no longer due now.
func TestGrade_PersistsAndSchedulesFuture(t *testing.T) {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	svc, db := newTestService(t, now)
	deck := seedDeck(t, db)
	addCard(t, db, deck, "f1", "c1", nil) // unseen

	if err := svc.Grade(context.Background(), "c1", 5); err != nil {
		t.Fatalf("grade: %v", err)
	}
	card, err := db.GetCard(context.Background(), "c1")
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if card.Grade != 5 {
		t.Errorf("grade got %d want 5", card.Grade)
	}
	if card.NextRep <= now.Unix() {
		t.Errorf("card should be scheduled in the future, next_rep=%d now=%d", card.NextRep, now.Unix())
	}
}

func TestNextCard_EmptyQueue(t *testing.T) {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, now)
	ci, err := svc.NextCard(context.Background(), nil)
	if err != nil {
		t.Fatalf("next card: %v", err)
	}
	if ci != nil {
		t.Fatalf("want nil on empty queue, got %v", ci)
	}
}

func TestForecast(t *testing.T) {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	svc, db := newTestService(t, now)
	deck := seedDeck(t, db)
	// The service derives "now" from the wall clock, so seed relative to that.
	adjNow := svc.scheduler.AdjustedNow(time.Now())

	// One card overdue (due today), one scheduled a couple of days out.
	addCard(t, db, deck, "f_today", "c_today", func(c *srs.Card) {
		c.Grade = 4
		c.Easiness = 2.5
		c.LastRep = adjNow - 2*srs.DAY
		c.NextRep = adjNow - srs.DAY
	})
	addCard(t, db, deck, "f_later", "c_later", func(c *srs.Card) {
		c.Grade = 4
		c.Easiness = 2.5
		c.LastRep = adjNow
		c.NextRep = adjNow + 2*srs.DAY
	})

	fc, err := svc.Forecast(context.Background(), 7)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	if len(fc) != 7 {
		t.Fatalf("forecast len got %d want 7", len(fc))
	}
	if fc[0].Count != 1 {
		t.Errorf("today count got %d want 1", fc[0].Count)
	}
	future := 0
	for _, d := range fc[1:] {
		future += d.Count
	}
	if future != 1 {
		t.Errorf("future count got %d want 1", future)
	}
	if fc[0].Label != "Today" {
		t.Errorf("first label got %q want Today", fc[0].Label)
	}
}
