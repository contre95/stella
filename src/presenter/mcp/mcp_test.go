package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/features/config"
	"github.com/contre95/stella/src/infra/database"
	"github.com/contre95/stella/src/srs"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestSession wires the MCP server onto a temp database and connects an
// in-memory client, returning a ready-to-call session and the media directory
// the server stores card media in.
func newTestSession(t *testing.T) (*mcpsdk.ClientSession, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.NewSqliteSRS(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Point the media dir into the temp dir so media-saving tests stay hermetic.
	mediaDir := filepath.Join(dir, "media")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := "dbPath: " + filepath.Join(dir, "test.db") + "\nimagePath: " + mediaDir + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.NewManager(cfgPath)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	scheduler := srs.NewScheduler(
		srs.Params{DayStartsAt: 3, NonMemorisedCardsInHand: 10},
		time.UTC,
		time.Now,
		srs.NewMathRand(),
	)
	// The default config is single-user, so this creates the local account the
	// MCP server operates as — the same wiring main.go uses.
	if _, err := auth.NewService(context.Background(), db, cfg); err != nil {
		t.Fatalf("auth service: %v", err)
	}
	srv := NewServer("stella-test", "0.0.0", cards.NewService(db, scheduler, cfg), auth.LocalUserID)

	ctx := context.Background()
	serverT, clientT := mcpsdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, mediaDir
}

// decodeStructured re-decodes a tool's structured output into v.
func decodeStructured(t *testing.T, res *mcpsdk.CallToolResult, v any) {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool returned an error result: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
}

// createTestDeck creates a deck via MCP and returns its ID.
func createTestDeck(t *testing.T, cs *mcpsdk.ClientSession) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "create_deck",
		Arguments: map[string]any{"name": "Test Deck"},
	})
	if err != nil {
		t.Fatalf("create_deck: %v", err)
	}
	var out CreateDeckOutput
	decodeStructured(t, res, &out)
	return out.DeckID
}

func TestListTools(t *testing.T) {
	cs, _ := newTestSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"create_card", "list_recent_cards"} {
		if !got[want] {
			t.Errorf("tool %q not advertised; have %v", want, got)
		}
	}
}

func TestCreateCardForwardAndReverse(t *testing.T) {
	cs, _ := newTestSession(t)
	ctx := context.Background()
	deckID := createTestDeck(t, cs)

	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "create_card",
		Arguments: map[string]any{"front": "Capital of France?", "back": "Paris", "reverse": true, "deckId": deckID},
	})
	if err != nil {
		t.Fatalf("call create_card: %v", err)
	}
	var out CreateCardOutput
	decodeStructured(t, res, &out)
	if out.FactID == "" {
		t.Error("expected a fact id")
	}
	if out.CardsAdded != 2 {
		t.Errorf("CardsAdded = %d, want 2 (forward + reverse)", out.CardsAdded)
	}
	if out.Front != "Capital of France?" || out.Back != "Paris" {
		t.Errorf("unexpected echoed content: %+v", out)
	}
}

func TestCreateCardForwardOnly(t *testing.T) {
	cs, _ := newTestSession(t)
	deckID := createTestDeck(t, cs)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "create_card",
		Arguments: map[string]any{"front": "2 + 2", "back": "4", "deckId": deckID},
	})
	if err != nil {
		t.Fatalf("call create_card: %v", err)
	}
	var out CreateCardOutput
	decodeStructured(t, res, &out)
	if out.CardsAdded != 1 {
		t.Errorf("CardsAdded = %d, want 1 (no reverse requested)", out.CardsAdded)
	}
}

func TestCreateCardEmptyFrontIsToolError(t *testing.T) {
	cs, _ := newTestSession(t)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "create_card",
		Arguments: map[string]any{"front": "   "},
	})
	if err != nil {
		t.Fatalf("call create_card: %v", err)
	}
	if !res.IsError {
		t.Error("expected a tool error for a blank front")
	}
}

func TestCreateCardRejectsBadGrade(t *testing.T) {
	cs, _ := newTestSession(t)
	deckID := createTestDeck(t, cs)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "create_card",
		Arguments: map[string]any{"front": "q", "back": "a", "grade": 9, "deckId": deckID},
	})
	if err != nil {
		t.Fatalf("call create_card: %v", err)
	}
	if !res.IsError {
		t.Error("expected a tool error for grade out of range")
	}
}

func TestListRecentCards(t *testing.T) {
	cs, _ := newTestSession(t)
	ctx := context.Background()

	deckID := createTestDeck(t, cs)
	for _, q := range []string{"a", "b", "c"} {
		if _, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "create_card",
			Arguments: map[string]any{"front": q, "back": q + "!", "deckId": deckID},
		}); err != nil {
			t.Fatalf("seed create_card %q: %v", q, err)
		}
	}

	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "list_recent_cards",
		Arguments: map[string]any{"limit": 2},
	})
	if err != nil {
		t.Fatalf("call list_recent_cards: %v", err)
	}
	var out ListRecentCardsOutput
	decodeStructured(t, res, &out)
	if len(out.Facts) != 2 {
		t.Fatalf("got %d facts, want 2 (limit honoured)", len(out.Facts))
	}
	// The created timestamp is second-granularity, so same-second inserts can't
	// be strictly ordered; just assert the rows are well-formed seeded facts.
	for _, f := range out.Facts {
		if f.Front == "" || f.FactID == "" {
			t.Errorf("malformed fact in listing: %+v", f)
		}
		if len(f.Cards) == 0 || f.Cards[0].Status == "" {
			t.Errorf("expected sister cards with a status, got %+v", f.Cards)
		}
	}
}

// createSimpleCard seeds a forward-only card and returns its fact ID.
func createSimpleCard(t *testing.T, cs *mcpsdk.ClientSession, deckID, front, back string) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "create_card",
		Arguments: map[string]any{"front": front, "back": back, "deckId": deckID},
	})
	if err != nil {
		t.Fatalf("create_card: %v", err)
	}
	var out CreateCardOutput
	decodeStructured(t, res, &out)
	return out.FactID
}

// editCard calls edit_card and returns the raw result plus decoded output when
// the call succeeded.
func editCard(t *testing.T, cs *mcpsdk.ClientSession, args map[string]any) (*mcpsdk.CallToolResult, EditCardOutput) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "edit_card",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("edit_card: %v", err)
	}
	var out EditCardOutput
	if !res.IsError {
		decodeStructured(t, res, &out)
	}
	return res, out
}

func TestEditCardTextAndReverse(t *testing.T) {
	cs, _ := newTestSession(t)
	deckID := createTestDeck(t, cs)
	factID := createSimpleCard(t, cs, deckID, "q", "a")

	_, out := editCard(t, cs, map[string]any{
		"factId": factID, "front": "q2", "back": "a2", "reverse": true,
	})
	if out.Front != "q2" || out.Back != "a2" {
		t.Errorf("edited text = %q/%q, want q2/a2", out.Front, out.Back)
	}
	if !out.Reverse {
		t.Error("expected a reverse card after reverse: true")
	}

	// Omitted fields keep their values; reverse: false removes the sister card.
	_, out = editCard(t, cs, map[string]any{"factId": factID, "reverse": false})
	if out.Front != "q2" || out.Back != "a2" {
		t.Errorf("text changed on a reverse-only edit: %q/%q", out.Front, out.Back)
	}
	if out.Reverse {
		t.Error("expected the reverse card to be removed")
	}
}

func TestEditCardClearingBackDropsReverse(t *testing.T) {
	cs, _ := newTestSession(t)
	deckID := createTestDeck(t, cs)
	if _, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "create_card",
		Arguments: map[string]any{"front": "q", "back": "a", "reverse": true, "deckId": deckID},
	}); err != nil {
		t.Fatalf("create_card: %v", err)
	}
	var list ListRecentCardsOutput
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "list_recent_cards"})
	if err != nil {
		t.Fatalf("list_recent_cards: %v", err)
	}
	decodeStructured(t, res, &list)
	factID := list.Facts[0].FactID

	_, out := editCard(t, cs, map[string]any{"factId": factID, "back": ""})
	if out.Back != "" {
		t.Errorf("back = %q, want empty", out.Back)
	}
	if out.Reverse {
		t.Error("clearing the back must drop the reverse card")
	}
}

func TestEditCardMediaFromLocalFiles(t *testing.T) {
	cs, mediaDir := newTestSession(t)
	deckID := createTestDeck(t, cs)
	factID := createSimpleCard(t, cs, deckID, "q", "a")

	srcDir := t.TempDir()
	imgPath := filepath.Join(srcDir, "pic.png")
	if err := os.WriteFile(imgPath, []byte("\x89PNG fake image"), 0o644); err != nil {
		t.Fatalf("write source image: %v", err)
	}
	audPath := filepath.Join(srcDir, "sound.mp3")
	if err := os.WriteFile(audPath, []byte("fake mp3 bytes"), 0o644); err != nil {
		t.Fatalf("write source audio: %v", err)
	}

	// Attach an image and an audio file from local paths.
	_, out := editCard(t, cs, map[string]any{
		"factId": factID, "imageFront": imgPath, "audioBack": audPath,
	})
	if out.ImageFront == "" || out.AudioBack == "" {
		t.Fatalf("expected stored media filenames, got %+v", out)
	}
	firstImage := out.ImageFront
	if _, err := os.Stat(filepath.Join(mediaDir, firstImage)); err != nil {
		t.Errorf("stored image missing from media dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mediaDir, out.AudioBack)); err != nil {
		t.Errorf("stored audio missing from media dir: %v", err)
	}

	// Replacing the image deletes the previous file.
	_, out = editCard(t, cs, map[string]any{"factId": factID, "imageFront": imgPath})
	if out.ImageFront == "" || out.ImageFront == firstImage {
		t.Errorf("expected a fresh image filename, got %q (was %q)", out.ImageFront, firstImage)
	}
	if _, err := os.Stat(filepath.Join(mediaDir, firstImage)); !os.IsNotExist(err) {
		t.Errorf("replaced image should be deleted, stat err = %v", err)
	}

	// Removing clears the field and deletes the file.
	secondImage := out.ImageFront
	_, out = editCard(t, cs, map[string]any{"factId": factID, "removeImageFront": true})
	if out.ImageFront != "" {
		t.Errorf("imageFront = %q after removal, want empty", out.ImageFront)
	}
	if _, err := os.Stat(filepath.Join(mediaDir, secondImage)); !os.IsNotExist(err) {
		t.Errorf("removed image should be deleted, stat err = %v", err)
	}
	if out.AudioBack == "" {
		t.Error("audioBack should be untouched by the image removal")
	}
}

func TestCreateCardMediaFromLocalFile(t *testing.T) {
	cs, mediaDir := newTestSession(t)
	deckID := createTestDeck(t, cs)

	imgPath := filepath.Join(t.TempDir(), "pic.webp")
	if err := os.WriteFile(imgPath, []byte("fake webp"), 0o644); err != nil {
		t.Fatalf("write source image: %v", err)
	}
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "create_card",
		Arguments: map[string]any{"front": "q", "back": "a", "deckId": deckID, "imageBack": imgPath},
	})
	if err != nil {
		t.Fatalf("create_card: %v", err)
	}
	if res.IsError {
		t.Fatalf("create_card with local image errored: %+v", res.Content)
	}

	list, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "list_recent_cards"})
	if err != nil {
		t.Fatalf("list_recent_cards: %v", err)
	}
	var out ListRecentCardsOutput
	decodeStructured(t, list, &out)
	if len(out.Facts) != 1 || out.Facts[0].ImageBack == "" {
		t.Fatalf("expected the fact to carry a back image, got %+v", out.Facts)
	}
	if _, err := os.Stat(filepath.Join(mediaDir, out.Facts[0].ImageBack)); err != nil {
		t.Errorf("stored image missing from media dir: %v", err)
	}
}

func TestEditCardRejectsBadInput(t *testing.T) {
	cs, _ := newTestSession(t)
	deckID := createTestDeck(t, cs)
	factID := createSimpleCard(t, cs, deckID, "q", "a")

	res, _ := editCard(t, cs, map[string]any{"factId": "nope", "front": "x"})
	if !res.IsError {
		t.Error("expected a tool error for an unknown factId")
	}
	res, _ = editCard(t, cs, map[string]any{"factId": factID, "front": "  "})
	if !res.IsError {
		t.Error("expected a tool error for a blank front")
	}
	res, _ = editCard(t, cs, map[string]any{"factId": factID, "audioFront": "/tmp/notes.txt"})
	if !res.IsError {
		t.Error("expected a tool error for an unsupported audio extension")
	}
}
