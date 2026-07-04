package review

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/infra/database"
	"github.com/contre95/stella/src/srs"
	"github.com/google/uuid"
)

// testUser owns every fixture in these tests.
const testUser = "u1"

// detRand makes scheduling deterministic: random.choice picks the first option
// and random.uniform returns 0, so interval noise is always 0.
type detRand struct{}

func (detRand) Choice(opts []int) int             { return opts[0] }
func (detRand) Uniform(low, high float64) float64 { return 0 }

// testNow is a fixed "wall clock" for the whole session: midday UTC so the
// study day (day_starts_at=3) is unambiguous.
var testNow = time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

func newTestEnv(t *testing.T) (*database.SqliteSRS, *Service) {
	t.Helper()
	db, err := database.NewSqliteSRS(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	sched := srs.NewScheduler(
		srs.Params{DayStartsAt: 3, NonMemorisedCardsInHand: 10},
		time.UTC,
		func() time.Time { return testNow },
		detRand{},
	)
	svc := NewService(db, sched)
	svc.shuffle = func(n int, swap func(i, j int)) {} // keep queue order deterministic
	if _, err := db.UpsertUser(context.Background(), &auth.User{ID: testUser, Issuer: "test", Subject: testUser, Created: 1}); err != nil {
		t.Fatalf("add user: %v", err)
	}
	if err := db.AddDeck(context.Background(), &srs.Deck{ID: "d1", UserID: testUser, Name: "deck"}); err != nil {
		t.Fatalf("add deck: %v", err)
	}
	return db, svc
}

// addCard inserts a fact with a single forward card in the given scheduling
// state and returns the card.
func addCard(t *testing.T, db *database.SqliteSRS, c srs.Card) *srs.Card {
	t.Helper()
	ctx := context.Background()
	factID := uuid.NewString()
	if err := db.AddFact(ctx, &srs.Fact{ID: factID, UserID: testUser, Front: "q-" + factID, Back: "a", DeckID: "d1", Created: 1, Modified: 1}); err != nil {
		t.Fatalf("add fact: %v", err)
	}
	c.ID = uuid.NewString()
	c.UserID = testUser
	c.FactID = factID
	c.DeckID = "d1"
	c.View = srs.ViewForward
	c.Active = true
	if c.Easiness == 0 {
		c.Easiness = 2.5
	}
	if err := db.AddCard(ctx, &c); err != nil {
		t.Fatalf("add card: %v", err)
	}
	return &c
}

func nextCardID(t *testing.T, svc *Service, learnAhead bool) string {
	t.Helper()
	ci, err := svc.NextCard(context.Background(), testUser, nil, learnAhead)
	if err != nil {
		t.Fatalf("next card: %v", err)
	}
	if ci == nil {
		t.Fatalf("next card: queue unexpectedly empty")
	}
	return ci.Card.ID
}

func grade(t *testing.T, svc *Service, cardID string, g int) {
	t.Helper()
	if err := svc.Grade(context.Background(), testUser, cardID, g); err != nil {
		t.Fatalf("grade: %v", err)
	}
}

// TestLearnAheadCyclesThroughAllCards reproduces the reported bug: learning
// ahead and grading 2 gives the card a shorter next_rep than every other
// future card, so a stateless top-1 requery would serve it forever and never
// show the rest. The queue must cycle through every future card instead.
func TestLearnAheadCyclesThroughAllCards(t *testing.T) {
	db, svc := newTestEnv(t)
	sched := svc.scheduler

	ids := map[string]bool{}
	for k := 1; k <= 5; k++ {
		c := addCard(t, db, srs.Card{
			Grade: 4, AcqReps: 1, RetReps: 2, AcqRepsSinceLapse: 1, RetRepsSinceLapse: 2,
			LastRep: testNow.Unix() - srs.DAY,
			NextRep: sched.MidnightUTC(testNow.Unix() + int64(10+k)*srs.DAY),
		})
		ids[c.ID] = true
	}

	seen := map[string]bool{}
	for range 5 {
		id := nextCardID(t, svc, true)
		if !ids[id] {
			t.Fatalf("served unknown card %s", id)
		}
		if seen[id] {
			t.Fatalf("card %s repeated before the other future cards were shown", id)
		}
		seen[id] = true
		grade(t, svc, id, 2)
	}
	if len(seen) != 5 {
		t.Fatalf("expected all 5 cards served, got %d", len(seen))
	}
}

// TestNoSameCardTwiceInARow: with several relearn cards in play, the card just
// shown must never come straight back, even across queue rebuilds.
func TestNoSameCardTwiceInARow(t *testing.T) {
	db, svc := newTestEnv(t)

	for range 2 {
		addCard(t, db, srs.Card{
			Grade: 1, AcqReps: 2, RetReps: 1, Lapses: 1,
			LastRep: testNow.Unix() - 100, NextRep: testNow.Unix() - 100,
		})
	}

	last := ""
	for i := range 6 {
		id := nextCardID(t, svc, false)
		if id == last {
			t.Fatalf("iteration %d: card %s shown twice in a row", i, id)
		}
		last = id
		grade(t, svc, id, 1) // keep failing so both cards stay in relearn
	}
}

// TestGrade0DoubleCopyRemovedOnGraduation: a grade-0 card is queued twice for
// a same-session re-test, and the leftover copy must disappear once the card
// is graded >= 2.
func TestGrade0DoubleCopyRemovedOnGraduation(t *testing.T) {
	db, svc := newTestEnv(t)

	b := addCard(t, db, srs.Card{
		Grade: 1, AcqReps: 1,
		LastRep: testNow.Unix() - 100, NextRep: testNow.Unix() - 100,
	})
	a := addCard(t, db, srs.Card{
		Grade: 0, AcqReps: 1,
		LastRep: testNow.Unix() - 100, NextRep: testNow.Unix() - 100,
	})

	// Stage 3 queues [b, a, a] (grade-1 first, grade-0 twice, shuffle no-op).
	if id := nextCardID(t, svc, false); id != b.ID {
		t.Fatalf("expected memorising grade-1 card first, got %s", id)
	}
	grade(t, svc, b.ID, 1)
	if id := nextCardID(t, svc, false); id != a.ID {
		t.Fatalf("expected grade-0 card second, got %s", id)
	}
	grade(t, svc, a.ID, 4) // graduates: the duplicate copy must be dropped

	sess := svc.sessions[testUser]
	if got := len(sess.cardIDsInQueue); got != 0 {
		t.Fatalf("expected empty queue after graduation, %d cards left: %v", got, sess.cardIDsInQueue)
	}
	if _, ok := sess.factIDsMemorised[a.FactID]; !ok {
		t.Fatalf("graduated card's fact not tracked as memorised")
	}
	// The rebuilt queue serves the still-memorising b, not a's stale copy.
	if id := nextCardID(t, svc, false); id != b.ID {
		t.Fatalf("expected %s after graduation, got %s", b.ID, id)
	}
}

// TestDueRetentionServedBeforeUnseen: stage 1 (scheduled cards) outranks
// stage 4 (unseen cards), matching Mnemosyne's priorities.
func TestDueRetentionServedBeforeUnseen(t *testing.T) {
	db, svc := newTestEnv(t)
	sched := svc.scheduler

	unseen := addCard(t, db, srs.Card{Grade: srs.GradeUnseen, LastRep: -1, NextRep: -1})
	due := addCard(t, db, srs.Card{
		Grade: 3, AcqReps: 1, RetReps: 1, AcqRepsSinceLapse: 1, RetRepsSinceLapse: 1,
		LastRep: testNow.Unix() - 3*srs.DAY,
		NextRep: sched.MidnightUTC(testNow.Unix() - srs.DAY),
	})

	if id := nextCardID(t, svc, false); id != due.ID {
		t.Fatalf("expected due retention card first, got %s", id)
	}
	grade(t, svc, due.ID, 4)
	if id := nextCardID(t, svc, false); id != unseen.ID {
		t.Fatalf("expected unseen card after due one, got %s", id)
	}
}

// TestMemorisedFactsSeededFromLog: facts memorised today survive a queue
// reset (e.g. a server restart) via the review log, like Mnemosyne's
// _fact_ids_learned_today.
func TestMemorisedFactsSeededFromLog(t *testing.T) {
	db, svc := newTestEnv(t)

	u := addCard(t, db, srs.Card{Grade: srs.GradeUnseen, LastRep: -1, NextRep: -1})
	if id := nextCardID(t, svc, false); id != u.ID {
		t.Fatalf("expected the unseen card, got %s", id)
	}
	grade(t, svc, u.ID, 4)

	// A fresh service (fresh queue state) must re-learn the fact ids from the
	// log when it seeds its session.
	svc2 := NewService(db, svc.scheduler)
	svc2.shuffle = func(n int, swap func(i, j int)) {}
	if ci, err := svc2.NextCard(context.Background(), testUser, nil, false); err != nil || ci != nil {
		t.Fatalf("expected empty queue on fresh service, got %v, %v", ci, err)
	}
	if _, ok := svc2.sessions[testUser].factIDsMemorised[u.FactID]; !ok {
		t.Fatalf("fact memorised today not seeded from the review log")
	}
}

// TestSisterCardAvoidance: grading a card onto the same day as its sister
// must push next_rep to the following day.
func TestSisterCardAvoidance(t *testing.T) {
	db, svc := newTestEnv(t)
	sched := svc.scheduler
	ctx := context.Background()

	// Two cards of the same fact: the reverse is parked exactly where the
	// forward's next interval (2 days actual * 2.5 = 5 days) will land.
	collision := sched.MidnightUTC(testNow.Unix() + 5*srs.DAY)
	fwd := addCard(t, db, srs.Card{
		Grade: 4, AcqReps: 1, RetReps: 2, AcqRepsSinceLapse: 1, RetRepsSinceLapse: 2,
		LastRep: testNow.Unix() - 2*srs.DAY,
		NextRep: sched.MidnightUTC(testNow.Unix() - srs.DAY),
	})
	rev := srs.Card{
		ID: uuid.NewString(), UserID: testUser, FactID: fwd.FactID, DeckID: "d1", View: srs.ViewReverse,
		Grade: 4, Easiness: 2.5, AcqReps: 1, RetReps: 2, AcqRepsSinceLapse: 1, RetRepsSinceLapse: 2,
		LastRep: testNow.Unix() - 2*srs.DAY, NextRep: collision, Active: true,
	}
	if err := db.AddCard(ctx, &rev); err != nil {
		t.Fatalf("add reverse card: %v", err)
	}

	grade(t, svc, fwd.ID, 4)

	got, err := db.GetCard(ctx, testUser, fwd.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if got.NextRep == collision {
		t.Fatalf("card scheduled on the same day as its sister")
	}
	if want := collision + srs.DAY; got.NextRep != want {
		t.Fatalf("expected next_rep pushed to %d, got %d", want, got.NextRep)
	}
}
