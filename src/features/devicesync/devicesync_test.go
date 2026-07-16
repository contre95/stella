package devicesync

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/infra/database"
	"github.com/contre95/stella/src/srs"
)

const testUser = "u1"

func newTestService(t *testing.T) (*Service, *database.SqliteSRS) {
	t.Helper()
	db, err := database.NewSqliteSRS(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.UpsertUser(context.Background(), &auth.User{ID: testUser, Issuer: "test", Subject: testUser, Created: 1}); err != nil {
		t.Fatalf("add user: %v", err)
	}
	return NewService(db), db
}

// fixture creates a deck with one fact that has forward + reverse cards,
// returning the deck and both cards (sync IDs populated by the repo).
func fixture(t *testing.T, db *database.SqliteSRS) (*srs.Deck, *srs.Card, *srs.Card) {
	t.Helper()
	ctx := context.Background()
	deck := &srs.Deck{ID: "d1", UserID: testUser, Name: "Capitals", Color: "#000", StudyEnabled: true}
	if err := db.AddDeck(ctx, deck); err != nil {
		t.Fatalf("add deck: %v", err)
	}
	fact := &srs.Fact{ID: "f1", UserID: testUser, Front: "la manzana", Back: "apple", DeckID: "d1", Created: 1, Modified: 1}
	if err := db.AddFact(ctx, fact); err != nil {
		t.Fatalf("add fact: %v", err)
	}
	fwd := &srs.Card{ID: "c1", UserID: testUser, FactID: "f1", DeckID: "d1", View: srs.ViewForward, Grade: srs.GradeUnseen, Easiness: 2.5, LastRep: -1, NextRep: -1, Active: true}
	rev := &srs.Card{ID: "c2", UserID: testUser, FactID: "f1", DeckID: "d1", View: srs.ViewReverse, Grade: srs.GradeUnseen, Easiness: 2.5, LastRep: -1, NextRep: -1, Active: true}
	for _, c := range []*srs.Card{fwd, rev} {
		if err := db.AddCard(ctx, c); err != nil {
			t.Fatalf("add card: %v", err)
		}
	}
	return deck, fwd, rev
}

func TestDeckCSVSwapsReverseCards(t *testing.T) {
	svc, db := newTestService(t)
	deck, fwd, rev := fixture(t, db)

	var buf bytes.Buffer
	if err := svc.WriteDeckCSV(context.Background(), testUser, deck.SyncID, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	want := fwd.SyncID + ",la manzana,apple\n" + rev.SyncID + ",apple,la manzana\n"
	if buf.String() != want {
		t.Errorf("csv mismatch:\n got %q\nwant %q", buf.String(), want)
	}
}

func TestManifestEtagTracksContent(t *testing.T) {
	svc, db := newTestService(t)
	_, _, _ = fixture(t, db)
	ctx := context.Background()

	m1, err := svc.Manifest(ctx, testUser)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(m1) != 1 || m1[0].Cards != 2 || !m1[0].StudyEnabled || m1[0].Truncated {
		t.Fatalf("unexpected manifest: %+v", m1)
	}

	m2, _ := svc.Manifest(ctx, testUser)
	if m1[0].Etag != m2[0].Etag {
		t.Errorf("etag not stable: %s vs %s", m1[0].Etag, m2[0].Etag)
	}

	fact, _ := db.GetFact(ctx, testUser, "f1")
	fact.Back = "apfel"
	if err := db.UpdateFact(ctx, fact); err != nil {
		t.Fatalf("update fact: %v", err)
	}
	m3, _ := svc.Manifest(ctx, testUser)
	if m3[0].Etag == m1[0].Etag {
		t.Error("etag unchanged after fact edit")
	}
}

func TestIngestReviewsIsIdempotent(t *testing.T) {
	svc, db := newTestService(t)
	_, fwd, _ := fixture(t, db)
	ctx := context.Background()

	line, _ := json.Marshal(DeviceReview{Card: fwd.SyncID, Deck: "x", Grade: 4, RetReps: 1, Sched: 100, Actual: 120, Ts: 1000})
	body := string(line) + "\n" + `{"card":"deadbeef","grade":2,"ts":900}` + "\n"

	accepted, received, err := svc.IngestReviews(ctx, testUser, "dev1", "bedroom-x4", strings.NewReader(body))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if accepted != 2 || received != 2 {
		t.Errorf("first upload: accepted=%d received=%d, want 2/2", accepted, received)
	}

	// Retrying the identical range must not duplicate anything.
	accepted, received, err = svc.IngestReviews(ctx, testUser, "dev1", "bedroom-x4", strings.NewReader(body))
	if err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	if accepted != 0 || received != 2 {
		t.Errorf("second upload: accepted=%d received=%d, want 0/2", accepted, received)
	}

	// Both reviews landed: the resolved one under the card's primary key, the
	// unknown one under its raw device ID.
	ts, err := db.ReviewTimestampsSince(ctx, testUser, 0)
	if err != nil {
		t.Fatalf("timestamps: %v", err)
	}
	if len(ts) != 2 {
		t.Errorf("stored reviews = %d, want 2", len(ts))
	}
}

func TestIngestReviewsRejectsBadGrade(t *testing.T) {
	svc, _ := newTestService(t)
	_, _, err := svc.IngestReviews(context.Background(), testUser, "dev1", "x4",
		strings.NewReader(`{"card":"abcd1234","grade":7,"ts":1000}`+"\n"))
	if err == nil {
		t.Fatal("expected error for grade 7")
	}
}

func TestExchangeStateLastWriterWins(t *testing.T) {
	svc, db := newTestService(t)
	deck, fwd, rev := fixture(t, db)
	ctx := context.Background()

	// Server side: fwd was reviewed at t=2000.
	fwd.Grade = 3
	fwd.Easiness = 2.4
	fwd.RetReps = 1
	fwd.LastRep = 2000
	fwd.NextRep = 100000
	if err := db.UpdateCard(ctx, fwd); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Device side: fwd reviewed earlier (t=1500, loses), rev reviewed at
	// t=3000 (wins), plus a record for a card the server deleted.
	in := strings.Join([]string{
		`{"id":"` + fwd.SyncID + `","g":2,"e":2.2,"ar":1,"rr":1,"l":0,"arl":1,"rrl":1,"lr":1500,"nr":90000}`,
		`{"id":"` + rev.SyncID + `","g":5,"e":2.7,"ar":1,"rr":2,"l":0,"arl":1,"rrl":2,"lr":3000,"nr":200000}`,
		`{"id":"00000000","g":1,"e":2.5,"ar":1,"rr":0,"l":0,"arl":1,"rrl":0,"lr":100,"nr":200}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := svc.ExchangeState(ctx, testUser, deck.SyncID, strings.NewReader(in), &out); err != nil {
		t.Fatalf("exchange: %v", err)
	}

	// Device's rev state was applied.
	got, _ := db.GetCard(ctx, testUser, rev.ID)
	if got.Grade != 5 || got.RetReps != 2 || got.LastRep != 3000 || got.NextRep != 200000 {
		t.Errorf("device state not applied: %+v", got)
	}
	// Device's stale fwd state was not.
	got, _ = db.GetCard(ctx, testUser, fwd.ID)
	if got.LastRep != 2000 || got.Grade != 3 {
		t.Errorf("server state clobbered by stale device state: %+v", got)
	}

	// Response contains exactly the server-won fwd record.
	var resp []CardState
	dec := json.NewDecoder(&out)
	for dec.More() {
		var st CardState
		if err := dec.Decode(&st); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		resp = append(resp, st)
	}
	if len(resp) != 1 || resp[0].ID != fwd.SyncID || resp[0].LastRep != 2000 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestBatchedStatePushAndDownload(t *testing.T) {
	svc, db := newTestService(t)
	deck, fwd, rev := fixture(t, db)
	ctx := context.Background()

	// Server side: fwd was reviewed at t=2000.
	fwd.Grade = 3
	fwd.LastRep = 2000
	fwd.NextRep = 100000
	if err := db.UpdateCard(ctx, fwd); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Device pushes two one-record batches: a stale fwd (loses) and a fresh
	// rev (wins). Repeating a batch must be a no-op (retries are idempotent).
	batches := []string{
		`{"id":"` + fwd.SyncID + `","g":2,"e":2.2,"ar":1,"rr":1,"l":0,"arl":1,"rrl":1,"lr":1500,"nr":90000}` + "\n",
		`{"id":"` + rev.SyncID + `","g":5,"e":2.7,"ar":1,"rr":2,"l":0,"arl":1,"rrl":2,"lr":3000,"nr":200000}` + "\n",
	}
	for _, b := range append(batches, batches[1]) {
		if _, _, err := svc.ApplyState(ctx, testUser, deck.SyncID, strings.NewReader(b)); err != nil {
			t.Fatalf("push: %v", err)
		}
	}

	got, _ := db.GetCard(ctx, testUser, rev.ID)
	if got.Grade != 5 || got.LastRep != 3000 {
		t.Errorf("device state not applied: %+v", got)
	}
	got, _ = db.GetCard(ctx, testUser, fwd.ID)
	if got.LastRep != 2000 || got.Grade != 3 {
		t.Errorf("server state clobbered by stale device state: %+v", got)
	}

	// Download returns both reviewed cards, nothing for unseen ones.
	seen := downloadState(t, svc, deck.SyncID, 0)
	if len(seen) != 2 || seen[fwd.SyncID] != 2000 || seen[rev.SyncID] != 3000 {
		t.Errorf("unexpected download: %+v", seen)
	}

	// A since filter keeps cards last graded before it out of the stream.
	seen = downloadState(t, svc, deck.SyncID, 2500)
	if len(seen) != 1 || seen[rev.SyncID] != 3000 {
		t.Errorf("unexpected since-filtered download: %+v", seen)
	}
}

// downloadState collects a WriteState stream as id -> lastRep.
func downloadState(t *testing.T, svc *Service, deckSyncID string, since int64) map[string]int64 {
	t.Helper()
	var out bytes.Buffer
	if err := svc.WriteState(context.Background(), testUser, deckSyncID, since, &out); err != nil {
		t.Fatalf("download: %v", err)
	}
	seen := map[string]int64{}
	dec := json.NewDecoder(&out)
	for dec.More() {
		var st CardState
		if err := dec.Decode(&st); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		seen[st.ID] = st.LastRep
	}
	return seen
}

func TestManifestStateHashTracksSchedulingState(t *testing.T) {
	svc, db := newTestService(t)
	_, fwd, _ := fixture(t, db)
	ctx := context.Background()

	// No reviews anywhere: the hash must equal the well-known empty-state
	// value the device computes for a missing/empty state file.
	m1, err := svc.Manifest(ctx, testUser)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m1[0].StateHash != "cbf29ce484222325" { // FNV-1a 64 offset basis
		t.Errorf("empty-state hash = %s, want FNV-1a basis", m1[0].StateHash)
	}

	// Grading a card must change the hash; an identical re-read must not.
	fwd.Grade = 3
	fwd.LastRep = 2000
	fwd.NextRep = 100000
	if err := db.UpdateCard(ctx, fwd); err != nil {
		t.Fatalf("update: %v", err)
	}
	m2, _ := svc.Manifest(ctx, testUser)
	if m2[0].StateHash == m1[0].StateHash {
		t.Error("state hash unchanged after grading")
	}
	m3, _ := svc.Manifest(ctx, testUser)
	if m3[0].StateHash != m2[0].StateHash {
		t.Errorf("state hash not stable: %s vs %s", m2[0].StateHash, m3[0].StateHash)
	}

	// The device computes hash(deck.SyncID entries): verify the exact digest
	// so the firmware implementation can be checked against the same vector.
	want := stateHash([]*srs.Card{fwd}, map[string]struct{}{fwd.SyncID: {}})
	if m2[0].StateHash != want {
		t.Errorf("manifest hash %s != direct hash %s", m2[0].StateHash, want)
	}
}

func TestExchangeStateSendsCardsUnknownToDevice(t *testing.T) {
	svc, db := newTestService(t)
	deck, fwd, _ := fixture(t, db)
	ctx := context.Background()

	// fwd was reviewed on the web; the device has never pulled this deck.
	fwd.Grade = 4
	fwd.LastRep = 5000
	fwd.NextRep = 300000
	if err := db.UpdateCard(ctx, fwd); err != nil {
		t.Fatalf("update: %v", err)
	}

	var out bytes.Buffer
	if err := svc.ExchangeState(ctx, testUser, deck.SyncID, strings.NewReader(""), &out); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	// Only the reviewed card comes back; the unseen reverse card carries no
	// information beyond what the CSV pull delivers.
	var st CardState
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("expected exactly one response record, got %q", out.String())
	}
	if st.ID != fwd.SyncID || st.LastRep != 5000 {
		t.Errorf("unexpected record: %+v", st)
	}
}
