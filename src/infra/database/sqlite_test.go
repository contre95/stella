package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/srs"
)

// testUser owns all fixtures; a second user is added where cross-user
// isolation is asserted.
const testUser = "u1"

func newTestDB(t *testing.T) *SqliteSRS {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := NewSqliteSRS(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	addUser(t, db, testUser)
	return db
}

func addUser(t *testing.T, db *SqliteSRS, id string) {
	t.Helper()
	if _, err := db.UpsertUser(context.Background(), &auth.User{ID: id, Issuer: "test", Subject: id, Created: 1}); err != nil {
		t.Fatalf("add user: %v", err)
	}
}

func addDeck(t *testing.T, db *SqliteSRS, id string) {
	t.Helper()
	if err := db.AddDeck(context.Background(), &srs.Deck{ID: id, UserID: testUser, Name: "deck-" + id, Color: "#000"}); err != nil {
		t.Fatalf("add deck: %v", err)
	}
}

func addFact(t *testing.T, db *SqliteSRS, id, deckID string) {
	t.Helper()
	ctx := context.Background()
	if err := db.AddFact(ctx, &srs.Fact{ID: id, UserID: testUser, Front: "f-" + id, Back: "b-" + id, DeckID: deckID, Created: 1, Modified: 1}); err != nil {
		t.Fatalf("add fact: %v", err)
	}
}

func addCard(t *testing.T, db *SqliteSRS, c *srs.Card) {
	t.Helper()
	if c.UserID == "" {
		c.UserID = testUser
	}
	if err := db.AddCard(context.Background(), c); err != nil {
		t.Fatalf("add card: %v", err)
	}
}

func TestFactCardRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addDeck(t, db, "d1")
	addFact(t, db, "fact1", "d1")
	card := &srs.Card{ID: "c1", FactID: "fact1", DeckID: "d1", View: srs.ViewForward, Grade: srs.GradeUnseen, Easiness: 2.5, NextRep: -1, LastRep: -1, Active: true}
	addCard(t, db, card)

	got, err := db.GetCard(ctx, testUser, "c1")
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
	got, _ = db.GetCard(ctx, testUser, "c1")
	if got.Grade != 4 || got.Easiness != 2.34 || got.NextRep != 1000 {
		t.Errorf("update not persisted: %+v", *got)
	}
}

func TestGetCardNotFound(t *testing.T) {
	db := newTestDB(t)
	_, err := db.GetCard(context.Background(), testUser, "missing")
	if !errors.Is(err, srs.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// TestUserScoping: one user's data must be invisible to another, for reads,
// lists, counts, and deletes alike.
func TestUserScoping(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addUser(t, db, "u2")
	addDeck(t, db, "d1")
	addFact(t, db, "f1", "d1")
	addCard(t, db, &srs.Card{ID: "c1", FactID: "f1", DeckID: "d1", View: srs.ViewForward, Grade: srs.GradeUnseen, Easiness: 2.5, NextRep: -1, LastRep: -1, Active: true})

	if _, err := db.GetDeck(ctx, "u2", "d1"); !errors.Is(err, srs.ErrNotFound) {
		t.Errorf("GetDeck across users: want ErrNotFound, got %v", err)
	}
	if _, err := db.GetCard(ctx, "u2", "c1"); !errors.Is(err, srs.ErrNotFound) {
		t.Errorf("GetCard across users: want ErrNotFound, got %v", err)
	}
	if facts, _ := db.ListFacts(ctx, "u2"); len(facts) != 0 {
		t.Errorf("ListFacts across users: got %d facts, want 0", len(facts))
	}
	if n, _ := db.ActiveCount(ctx, "u2"); n != 0 {
		t.Errorf("ActiveCount across users: got %d, want 0", n)
	}
	if err := db.DeleteFact(ctx, "u2", "f1"); err != nil {
		t.Fatalf("delete fact: %v", err)
	}
	if _, err := db.GetFact(ctx, testUser, "f1"); err != nil {
		t.Errorf("cross-user delete removed the fact: %v", err)
	}
}

func TestQueueQueries(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addDeck(t, db, "d1")
	addFact(t, db, "f1", "d1")
	addFact(t, db, "f2", "d1")
	addFact(t, db, "f3", "d1")

	// Unseen card.
	addCard(t, db, &srs.Card{ID: "unseen", FactID: "f1", DeckID: "d1", View: srs.ViewForward, Grade: srs.GradeUnseen, Easiness: 2.5, Active: true})
	// Retained card due in the past.
	addCard(t, db, &srs.Card{ID: "due", FactID: "f2", DeckID: "d1", View: srs.ViewForward, Grade: 4, Easiness: 2.5, NextRep: 500, LastRep: 100, Active: true})
	// Retained card due in the future.
	addCard(t, db, &srs.Card{ID: "future", FactID: "f3", DeckID: "d1", View: srs.ViewForward, Grade: 4, Easiness: 2.5, NextRep: 5000, LastRep: 100, Active: true})

	if n, _ := db.ActiveCount(ctx, testUser); n != 3 {
		t.Errorf("ActiveCount got %d want 3", n)
	}
	if n, _ := db.NonMemorisedCount(ctx, testUser, nil); n != 1 {
		t.Errorf("NonMemorisedCount got %d want 1", n)
	}
	if n, _ := db.ScheduledCount(ctx, testUser, 1000, nil); n != 1 {
		t.Errorf("ScheduledCount got %d want 1", n)
	}

	unseen, _ := db.CardsUnseen(ctx, testUser, "", -1, nil)
	if len(unseen) != 1 || unseen[0].ID != "unseen" {
		t.Errorf("CardsUnseen got %v", ids(unseen))
	}
	due, _ := db.CardsDueForRetRep(ctx, testUser, 1000, "interval", -1, nil)
	if len(due) != 1 || due[0].ID != "due" {
		t.Errorf("CardsDueForRetRep got %v", ids(due))
	}
	ahead, _ := db.CardsLearnAhead(ctx, testUser, 1000, "next_rep", -1, nil)
	if len(ahead) != 1 || ahead[0].ID != "future" {
		t.Errorf("CardsLearnAhead got %v", ids(ahead))
	}
}

func TestRelearnVsNewMemorising(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addDeck(t, db, "d1")
	addFact(t, db, "f1", "d1")
	addFact(t, db, "f2", "d1")
	// grade 1, lapses>0 => relearn.
	addCard(t, db, &srs.Card{ID: "relearn", FactID: "f1", DeckID: "d1", View: srs.ViewForward, Grade: 1, Easiness: 2.5, Lapses: 2, Active: true})
	// grade 1, lapses=0 => new memorising.
	addCard(t, db, &srs.Card{ID: "newmem", FactID: "f2", DeckID: "d1", View: srs.ViewForward, Grade: 1, Easiness: 2.5, Lapses: 0, Active: true})

	relearn, _ := db.CardsToRelearn(ctx, testUser, 1, "-interval", nil)
	if len(relearn) != 1 || relearn[0].ID != "relearn" {
		t.Errorf("CardsToRelearn got %v", ids(relearn))
	}
	newmem, _ := db.CardsNewMemorising(ctx, testUser, 1, nil)
	if len(newmem) != 1 || newmem[0].ID != "newmem" {
		t.Errorf("CardsNewMemorising got %v", ids(newmem))
	}
}

func TestSisterCardCountAndCascade(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addDeck(t, db, "d1")
	addFact(t, db, "f1", "d1")
	// Two sister cards of the same fact, both retained and due the same day.
	addCard(t, db, &srs.Card{ID: "fwd", FactID: "f1", DeckID: "d1", View: srs.ViewForward, Grade: 4, Easiness: 2.5, NextRep: 1000, Active: true})
	addCard(t, db, &srs.Card{ID: "rev", FactID: "f1", DeckID: "d1", View: srs.ViewReverse, Grade: 4, Easiness: 2.5, NextRep: 1500, Active: true})

	// From fwd's perspective, the reverse sister is scheduled in [1000, 2000).
	n, err := db.SisterCardCountScheduledBetween(ctx, testUser, "f1", "fwd", 1000, 2000)
	if err != nil {
		t.Fatalf("sister count: %v", err)
	}
	if n != 1 {
		t.Errorf("sister count got %d want 1", n)
	}

	// Deleting the fact cascades to its cards.
	if err := db.DeleteFact(ctx, testUser, "f1"); err != nil {
		t.Fatalf("delete fact: %v", err)
	}
	if n, _ := db.ActiveCount(ctx, testUser); n != 0 {
		t.Errorf("cards not cascaded on fact delete: %d remain", n)
	}
}

// TestMoveFactToDeck asserts that moving a fact to another deck updates the
// fact's and its cards' deck_id, leaves every scheduling field untouched, and
// is scoped to the owning user.
func TestMoveFactToDeck(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addDeck(t, db, "d1")
	addDeck(t, db, "d2")
	addFact(t, db, "f1", "d1")
	card := &srs.Card{ID: "c1", FactID: "f1", DeckID: "d1", View: srs.ViewForward,
		Grade: 4, Easiness: 2.34, AcqReps: 2, RetReps: 5, Lapses: 1,
		AcqRepsSinceLapse: 1, RetRepsSinceLapse: 3, LastRep: 100, NextRep: 1000, Active: true}
	addCard(t, db, card)

	if err := db.MoveFactToDeck(ctx, testUser, "f1", "d2"); err != nil {
		t.Fatalf("move fact: %v", err)
	}

	fact, err := db.GetFact(ctx, testUser, "f1")
	if err != nil {
		t.Fatalf("get fact: %v", err)
	}
	if fact.DeckID != "d2" {
		t.Errorf("fact deck_id = %q, want d2", fact.DeckID)
	}

	got, err := db.GetCard(ctx, testUser, "c1")
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if got.DeckID != "d2" {
		t.Errorf("card deck_id = %q, want d2", got.DeckID)
	}
	want := *card
	want.DeckID = "d2"
	if *got != want {
		t.Errorf("scheduling state changed by move:\n got %+v\nwant %+v", *got, want)
	}

	if facts, _ := db.ListFactsByDeck(ctx, testUser, "d1"); len(facts) != 0 {
		t.Errorf("fact still listed under old deck: %d", len(facts))
	}
	if facts, _ := db.ListFactsByDeck(ctx, testUser, "d2"); len(facts) != 1 {
		t.Errorf("fact not listed under new deck: %d", len(facts))
	}

	// Cross-user: another user's ID must not be able to move it.
	addUser(t, db, "u2")
	if err := db.MoveFactToDeck(ctx, "u2", "f1", "d2"); !errors.Is(err, srs.ErrNotFound) {
		t.Errorf("cross-user move: want ErrNotFound, got %v", err)
	}

	if err := db.MoveFactToDeck(ctx, testUser, "missing", "d2"); !errors.Is(err, srs.ErrNotFound) {
		t.Errorf("moving missing fact: want ErrNotFound, got %v", err)
	}
}

// TestTags covers the tag CRUD, deck linking, the per-user name uniqueness,
// and the cascades that clean up deck_tags rows.
func TestTags(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	addDeck(t, db, "d1")
	addDeck(t, db, "d2")

	if err := db.AddTag(ctx, &srs.Tag{ID: "t1", UserID: testUser, Name: "spanish"}); err != nil {
		t.Fatalf("add tag: %v", err)
	}
	if err := db.AddTag(ctx, &srs.Tag{ID: "t2", UserID: testUser, Name: "spanish"}); err == nil {
		t.Error("duplicate tag name for the same user should fail")
	}
	got, err := db.GetTagByName(ctx, testUser, "spanish")
	if err != nil || got.ID != "t1" {
		t.Fatalf("get tag by name: got %v, %v", got, err)
	}
	if _, err := db.GetTag(ctx, testUser, "missing"); !errors.Is(err, srs.ErrNotFound) {
		t.Errorf("missing tag: want ErrNotFound, got %v", err)
	}

	// Link both decks; re-linking must be a no-op.
	for _, deckID := range []string{"d1", "d2", "d1"} {
		if err := db.TagDeck(ctx, testUser, deckID, "t1"); err != nil {
			t.Fatalf("tag deck %s: %v", deckID, err)
		}
	}
	ids, err := db.DeckIDsByTag(ctx, testUser, "t1")
	if err != nil || len(ids) != 2 {
		t.Fatalf("deck ids by tag: got %v, %v; want 2 decks", ids, err)
	}
	tags, err := db.ListTagsForDeck(ctx, testUser, "d1")
	if err != nil || len(tags) != 1 || tags[0].Name != "spanish" {
		t.Fatalf("tags for deck: got %v, %v", tags, err)
	}
	byDeck, err := db.TagsByDeck(ctx, testUser)
	if err != nil || len(byDeck) != 2 {
		t.Fatalf("tags by deck: got %v, %v; want 2 entries", byDeck, err)
	}

	// Another user must neither see the tag nor be able to link against it.
	addUser(t, db, "u2")
	if tags, _ := db.ListTags(ctx, "u2"); len(tags) != 0 {
		t.Errorf("cross-user tag visible: %v", tags)
	}
	if err := db.TagDeck(ctx, "u2", "d1", "t1"); err != nil {
		t.Fatalf("cross-user tag deck: %v", err)
	}
	if ids, _ := db.DeckIDsByTag(ctx, testUser, "t1"); len(ids) != 2 {
		t.Errorf("cross-user TagDeck inserted a link: %v", ids)
	}

	if err := db.UntagDeck(ctx, testUser, "d2", "t1"); err != nil {
		t.Fatalf("untag deck: %v", err)
	}
	if ids, _ := db.DeckIDsByTag(ctx, testUser, "t1"); len(ids) != 1 || ids[0] != "d1" {
		t.Errorf("after untag: got %v, want [d1]", ids)
	}

	// Deleting a deck removes its links (FK cascade) but keeps the tag.
	if err := db.DeleteDeck(ctx, testUser, "d1"); err != nil {
		t.Fatalf("delete deck: %v", err)
	}
	if ids, _ := db.DeckIDsByTag(ctx, testUser, "t1"); len(ids) != 0 {
		t.Errorf("deck delete left links behind: %v", ids)
	}
	if _, err := db.GetTag(ctx, testUser, "t1"); err != nil {
		t.Errorf("tag should survive deck deletion: %v", err)
	}

	// Deleting the tag frees its name for reuse.
	if err := db.DeleteTag(ctx, testUser, "t1"); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
	if err := db.AddTag(ctx, &srs.Tag{ID: "t3", UserID: testUser, Name: "spanish"}); err != nil {
		t.Errorf("re-adding deleted tag name: %v", err)
	}
}

// TestSessions covers the auth store: session round trip, expiry, and delete.
func TestSessions(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.AddSession(ctx, "hash1", testUser, 1000); err != nil {
		t.Fatalf("add session: %v", err)
	}
	u, err := db.UserBySession(ctx, "hash1", 500)
	if err != nil || u.ID != testUser {
		t.Fatalf("session lookup: got %v, %v", u, err)
	}
	if _, err := db.UserBySession(ctx, "hash1", 1000); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("expired session: want ErrNoSession, got %v", err)
	}
	if _, err := db.UserBySession(ctx, "unknown", 500); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("unknown session: want ErrNoSession, got %v", err)
	}
	if err := db.DeleteSession(ctx, "hash1"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := db.UserBySession(ctx, "hash1", 500); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("deleted session: want ErrNoSession, got %v", err)
	}
}

// TestUpsertUser: the same (issuer, subject) identity must map onto one user,
// refreshing profile fields on later logins.
func TestUpsertUser(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	first, err := db.UpsertUser(ctx, &auth.User{ID: "id-a", Issuer: "https://idp", Subject: "sub", Email: "a@x", Name: "A", Created: 1})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second, err := db.UpsertUser(ctx, &auth.User{ID: "id-b", Issuer: "https://idp", Subject: "sub", Email: "b@x", Name: "B", Created: 2})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("same identity produced two users: %q vs %q", first.ID, second.ID)
	}
	if second.Email != "b@x" || second.Name != "B" {
		t.Errorf("profile not refreshed: %+v", second)
	}
}

func ids(cards []*srs.Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}
