// Package devicesync implements the stella-x4 sync protocol under /api/x4.
//
// The device is offline-first: it studies from the SD card and syncs in
// explicit batch sessions. One sync session performs, in order:
//
//  1. GET  /hello                  — server time, used to fix the device RTC
//  2. POST /reviews                — upload the review log (idempotent)
//  3. POST /decks/{id}/state       — per-deck scheduling-state exchange (LWW)
//  4. GET  /decks                  — deck manifest (etags decide re-downloads)
//  5. GET  /decks/{id}/cards       — CSV export of changed decks
//
// Decks and cards are addressed by their compact 8-hex sync IDs (the device
// packs IDs into 12-byte fields), never by primary keys. Deck content, deck
// deletion and study-enablement are server-authoritative: the device only
// ever writes back review events and scheduling state.
package devicesync

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/contre95/stella/src/srs"
)

// ExportCap mirrors stella-x4's MAX_ACTIVE_CARDS: the device loads at most
// this many cards per deck, so exporting more would only waste its SD space.
const ExportCap = 1000

// maxLine bounds one NDJSON line; fact-free payloads are tiny, this is slack.
const maxLine = 64 * 1024

// Service implements the sync operations on top of the domain repository.
type Service struct {
	repo srs.Repository
}

// NewService constructs the device sync service.
func NewService(repo srs.Repository) *Service {
	return &Service{repo: repo}
}

// DeckManifest describes one deck in the sync manifest.
type DeckManifest struct {
	ID           string `json:"id"`   // deck sync ID
	Name         string `json:"name"` // display name; the device uses it as the CSV filename
	Etag         string `json:"etag"` // content hash of the export; changes iff the CSV would
	Cards        int    `json:"cards"`
	Truncated    bool   `json:"truncated"` // deck exceeds ExportCap; export is cut off
	StudyEnabled bool   `json:"studyEnabled"`
}

// Manifest lists every deck with its export etag. All decks are listed —
// including study-disabled ones — so toggling study on the server reaches the
// device as a flag flip without a re-download.
func (s *Service) Manifest(ctx context.Context, userID string) ([]DeckManifest, error) {
	decks, err := s.repo.ListDecks(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]DeckManifest, 0, len(decks))
	for _, d := range decks {
		rows, truncated, err := s.exportRows(ctx, userID, d.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, DeckManifest{
			ID:           d.SyncID,
			Name:         d.Name,
			Etag:         etag(rows),
			Cards:        len(rows),
			Truncated:    truncated,
			StudyEnabled: d.StudyEnabled,
		})
	}
	return out, nil
}

// exportRows fetches a deck's export rows, applying the device card cap.
func (s *Service) exportRows(ctx context.Context, userID, deckID string) ([]srs.DeckExportRow, bool, error) {
	rows, err := s.repo.DeckExportRows(ctx, userID, deckID, ExportCap+1)
	if err != nil {
		return nil, false, err
	}
	if len(rows) > ExportCap {
		return rows[:ExportCap], true, nil
	}
	return rows, false, nil
}

// etag hashes the export rows so the device can skip unchanged decks. Fields
// are length-prefixed so shifting text between fields cannot collide.
func etag(rows []srs.DeckExportRow) string {
	h := sha256.New()
	for _, r := range rows {
		for _, f := range []string{r.SyncID, r.Front, r.Back} {
			fmt.Fprintf(h, "%d:", len(f))
			io.WriteString(h, f)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// WriteDeckCSV streams the deck export as RFC 4180 CSV rows of
// "sync_id,front,back" — the same format the device already parses, plus the
// leading ID column.
func (s *Service) WriteDeckCSV(ctx context.Context, userID, deckSyncID string, w io.Writer) error {
	deck, err := s.repo.GetDeckBySyncID(ctx, userID, deckSyncID)
	if err != nil {
		return err
	}
	rows, _, err := s.exportRows(ctx, userID, deck.ID)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	for _, r := range rows {
		if err := cw.Write([]string{r.SyncID, r.Front, r.Back}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// DeviceReview is one uploaded line of the device's review log.
type DeviceReview struct {
	Card    string `json:"card"` // card sync ID, or a local content-hash ID
	Deck    string `json:"deck"`
	Grade   int    `json:"grade"`
	RetReps int    `json:"retReps"`
	Sched   int64  `json:"sched"`  // scheduled interval, seconds
	Actual  int64  `json:"actual"` // actual interval, seconds
	Ts      int64  `json:"ts"`     // POSIX seconds when graded
}

// IngestReviews reads NDJSON reviews and inserts them idempotently. Review
// IDs are derived from (device, card, timestamp, grade), so re-uploading the
// same range after a lost response cannot duplicate rows. Reviews whose card
// sync ID is unknown (decks that exist only on the device's SD card) are
// stored under the raw device ID: they still count for streaks and heatmaps,
// they just never join to a server card.
func (s *Service) IngestReviews(ctx context.Context, userID, deviceID, source string, r io.Reader) (accepted, received int, err error) {
	idBysync, err := s.repo.CardIDsBySyncID(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rev DeviceReview
		if err := json.Unmarshal([]byte(line), &rev); err != nil {
			return accepted, received, fmt.Errorf("review line %d: %w", received+1, err)
		}
		if rev.Card == "" || rev.Grade < 0 || rev.Grade > 5 || rev.Ts <= 0 {
			return accepted, received, fmt.Errorf("review line %d: invalid card/grade/ts", received+1)
		}
		received++
		cardID, ok := idBysync[rev.Card]
		if !ok {
			cardID = rev.Card
		}
		sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d|%d", deviceID, rev.Card, rev.Ts, rev.Grade))
		inserted, err := s.repo.AddReviewIfNew(ctx, &srs.Review{
			ID:                hex.EncodeToString(sum[:16]),
			UserID:            userID,
			CardID:            cardID,
			Grade:             rev.Grade,
			RetReps:           rev.RetReps,
			ScheduledInterval: rev.Sched,
			ActualInterval:    rev.Actual,
			Timestamp:         rev.Ts,
			Source:            source,
		})
		if err != nil {
			return accepted, received, err
		}
		if inserted {
			accepted++
		}
	}
	return accepted, received, sc.Err()
}

// CardState is the wire form of one card's scheduling state. Keys are terse
// because the device streams these through a small buffer.
type CardState struct {
	ID                string  `json:"id"` // card sync ID
	Grade             int     `json:"g"`
	Easiness          float64 `json:"e"`
	AcqReps           int     `json:"ar"`
	RetReps           int     `json:"rr"`
	Lapses            int     `json:"l"`
	AcqRepsSinceLapse int     `json:"arl"`
	RetRepsSinceLapse int     `json:"rrl"`
	LastRep           int64   `json:"lr"`
	NextRep           int64   `json:"nr"`
}

func stateOf(c *srs.Card) CardState {
	return CardState{
		ID:                c.SyncID,
		Grade:             c.Grade,
		Easiness:          c.Easiness,
		AcqReps:           c.AcqReps,
		RetReps:           c.RetReps,
		Lapses:            c.Lapses,
		AcqRepsSinceLapse: c.AcqRepsSinceLapse,
		RetRepsSinceLapse: c.RetRepsSinceLapse,
		LastRep:           c.LastRep,
		NextRep:           c.NextRep,
	}
}

func applyState(c *srs.Card, st CardState) {
	c.Grade = st.Grade
	c.Easiness = st.Easiness
	c.AcqReps = st.AcqReps
	c.RetReps = st.RetReps
	c.Lapses = st.Lapses
	c.AcqRepsSinceLapse = st.AcqRepsSinceLapse
	c.RetRepsSinceLapse = st.RetRepsSinceLapse
	c.LastRep = st.LastRep
	c.NextRep = st.NextRep
}

// ExchangeState merges the device's scheduling state for one deck and streams
// back every record where the server is ahead. Conflict rule is per-card
// last-writer-wins on lastRep: whichever side graded the card most recently
// holds the truth (SM2 state is a pure function of the latest grading).
// Incoming records for cards the server no longer has are dropped — the
// following deck pull removes them from the device too.
func (s *Service) ExchangeState(ctx context.Context, userID, deckSyncID string, in io.Reader, out io.Writer) error {
	deck, err := s.repo.GetDeckBySyncID(ctx, userID, deckSyncID)
	if err != nil {
		return err
	}
	cards, err := s.repo.CardsByDeck(ctx, userID, deck.ID)
	if err != nil {
		return err
	}
	bySync := make(map[string]*srs.Card, len(cards))
	for _, c := range cards {
		bySync[c.SyncID] = c
	}

	enc := json.NewEncoder(out)
	seen := make(map[string]struct{})
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	line := 0
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		line++
		var st CardState
		if err := json.Unmarshal([]byte(text), &st); err != nil {
			return fmt.Errorf("state line %d: %w", line, err)
		}
		if st.Grade < srs.GradeUnseen || st.Grade > 5 || (st.Grade != srs.GradeUnseen && st.Easiness < 1.0) {
			return fmt.Errorf("state line %d: implausible grade/easiness", line)
		}
		card, ok := bySync[st.ID]
		if !ok {
			continue
		}
		seen[st.ID] = struct{}{}
		switch {
		case st.LastRep > card.LastRep: // device is ahead: apply
			applyState(card, st)
			if err := s.repo.UpdateCard(ctx, card); err != nil {
				return err
			}
		case card.LastRep > st.LastRep: // server is ahead: send back
			if err := enc.Encode(stateOf(card)); err != nil {
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// Cards the device did not mention: new since its last pull, or state the
	// device lost. Unseen cards carry no information — the deck CSV already
	// delivers them as unseen — so only reviewed ones are sent.
	for _, c := range cards {
		if _, ok := seen[c.SyncID]; ok || c.LastRep < 0 {
			continue
		}
		if err := enc.Encode(stateOf(c)); err != nil {
			return err
		}
	}
	return nil
}
