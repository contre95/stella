package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
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

// newTestCards builds a cards.Service backed by a temp sqlite database and
// media directory, with the local single-user account created. Split out of
// newTestSession so tests can seed fact media directly on the service —
// bypassing the MCP tool layer's http(s)-only restriction on image/audio
// sources (see mediaHTTPClient) — while still exercising get_card_image over
// a real MCP session.
func newTestCards(t *testing.T) (*cards.Service, string) {
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
	return cards.NewService(db, scheduler, cfg), mediaDir
}

// newTestSessionFor wires an MCP server backed by cardsService onto an
// in-memory client and returns a ready-to-call session.
func newTestSessionFor(t *testing.T, cardsService *cards.Service) *mcpsdk.ClientSession {
	t.Helper()
	srv := NewServer("stella-test", "0.0.0", cardsService, auth.LocalUserID)

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
	return cs
}

// newTestSession wires the MCP server onto a temp database and connects an
// in-memory client, returning a ready-to-call session and the media directory
// the server stores card media in.
func newTestSession(t *testing.T) (*mcpsdk.ClientSession, string) {
	t.Helper()
	cardsService, mediaDir := newTestCards(t)
	return newTestSessionFor(t, cardsService), mediaDir
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
		"factId": factID, "front": "q2", "back": "a2", "addReverse": true,
	})
	if out.Front != "q2" || out.Back != "a2" {
		t.Errorf("edited text = %q/%q, want q2/a2", out.Front, out.Back)
	}
	if !out.Reverse {
		t.Error("expected a reverse card after addReverse: true")
	}

	// Omitted fields keep their values; removeReverse deletes the sister card.
	_, out = editCard(t, cs, map[string]any{"factId": factID, "removeReverse": true})
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

// TestCreateCardRejectsLocalFilePaths ensures media sources must be http(s)
// URLs. The MCP endpoint is reachable over the network without per-request
// auth in single-user mode, so accepting local paths would let any caller
// read arbitrary files off the server's disk (via the allowed image/audio
// extensions) and exfiltrate them through the resulting card's media file.
func TestCreateCardRejectsLocalFilePaths(t *testing.T) {
	cs, _ := newTestSession(t)
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
	if !res.IsError {
		t.Fatalf("create_card with a local file path should have been rejected")
	}
}

// TestEditCardRejectsLocalFilePaths mirrors TestCreateCardRejectsLocalFilePaths
// for edit_card.
func TestEditCardRejectsLocalFilePaths(t *testing.T) {
	cs, _ := newTestSession(t)
	deckID := createTestDeck(t, cs)
	factID := createSimpleCard(t, cs, deckID, "q", "a")

	imgPath := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(imgPath, []byte("\x89PNG fake image"), 0o644); err != nil {
		t.Fatalf("write source image: %v", err)
	}
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "edit_card",
		Arguments: map[string]any{"factId": factID, "imageFront": imgPath},
	})
	if err != nil {
		t.Fatalf("edit_card: %v", err)
	}
	if !res.IsError {
		t.Fatalf("edit_card with a local file path should have been rejected")
	}
}

// TestMoveCard checks that move_card reassigns a fact's deck while leaving
// its scheduling status untouched, and rejects a bad deckId.
func TestMoveCard(t *testing.T) {
	cs, _ := newTestSession(t)
	ctx := context.Background()
	deckA := createTestDeck(t, cs)

	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "create_card",
		Arguments: map[string]any{"front": "q", "back": "a", "deckId": deckA, "grade": 3},
	})
	if err != nil {
		t.Fatalf("create_card: %v", err)
	}
	var created CreateCardOutput
	decodeStructured(t, res, &created)
	factID := created.FactID

	statusBefore := factStatus(t, cs, factID)
	if statusBefore == "" {
		t.Fatal("seeded card has no status before move")
	}

	deckB, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "create_deck",
		Arguments: map[string]any{"name": "Deck B"},
	})
	if err != nil {
		t.Fatalf("create_deck: %v", err)
	}
	var deckBOut CreateDeckOutput
	decodeStructured(t, deckB, &deckBOut)

	res, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "move_card",
		Arguments: map[string]any{"factId": factID, "deckId": deckBOut.DeckID},
	})
	if err != nil {
		t.Fatalf("move_card: %v", err)
	}
	var moved MoveCardOutput
	decodeStructured(t, res, &moved)
	if moved.DeckID != deckBOut.DeckID {
		t.Errorf("moved.DeckID = %q, want %q", moved.DeckID, deckBOut.DeckID)
	}

	fact := findFact(t, cs, factID)
	if fact.DeckID != deckBOut.DeckID {
		t.Errorf("fact still shows deckId %q, want %q", fact.DeckID, deckBOut.DeckID)
	}
	if got := factStatus(t, cs, factID); got != statusBefore {
		t.Errorf("move_card changed the card's status: got %q, want unchanged %q", got, statusBefore)
	}

	res, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "move_card",
		Arguments: map[string]any{"factId": factID, "deckId": "no-such-deck"},
	})
	if err != nil {
		t.Fatalf("move_card: %v", err)
	}
	if !res.IsError {
		t.Error("move_card to a nonexistent deck should have been rejected")
	}
}

// findFact returns the FactSummary for factID from list_recent_cards.
func findFact(t *testing.T, cs *mcpsdk.ClientSession, factID string) FactSummary {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "list_recent_cards",
		Arguments: map[string]any{"limit": 100},
	})
	if err != nil {
		t.Fatalf("list_recent_cards: %v", err)
	}
	var out ListRecentCardsOutput
	decodeStructured(t, res, &out)
	for _, f := range out.Facts {
		if f.FactID == factID {
			return f
		}
	}
	t.Fatalf("fact %q not found in list_recent_cards", factID)
	return FactSummary{}
}

// factStatus returns the forward card's status string for factID.
func factStatus(t *testing.T, cs *mcpsdk.ClientSession, factID string) string {
	t.Helper()
	fact := findFact(t, cs, factID)
	if len(fact.Cards) == 0 {
		return ""
	}
	return fact.Cards[0].Status
}

// TestTagTools drives the whole tag lifecycle over MCP: create (idempotent),
// tag/untag decks by name, list, rename, and delete.
func TestTagTools(t *testing.T) {
	cs, _ := newTestSession(t)
	ctx := context.Background()
	deckID := createTestDeck(t, cs)

	call := func(name string, args map[string]any) *mcpsdk.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}

	// tag_deck creates the tag on the fly.
	var tagged TagDeckOutput
	decodeStructured(t, call("tag_deck", map[string]any{"deckId": deckID, "tag": "spanish"}), &tagged)
	if tagged.TagID == "" || tagged.Tag != "spanish" {
		t.Fatalf("tag_deck output: %+v", tagged)
	}

	// create_tag with the same name returns the existing tag.
	var created TagOutput
	decodeStructured(t, call("create_tag", map[string]any{"name": "spanish"}), &created)
	if created.TagID != tagged.TagID {
		t.Errorf("create_tag made a duplicate: %q vs %q", created.TagID, tagged.TagID)
	}

	var tags ListTagsOutput
	decodeStructured(t, call("list_tags", nil), &tags)
	if len(tags.Tags) != 1 || len(tags.Tags[0].Decks) != 1 || tags.Tags[0].Decks[0].DeckID != deckID {
		t.Fatalf("list_tags: %+v", tags)
	}

	var decks ListDecksOutput
	decodeStructured(t, call("list_decks", nil), &decks)
	if len(decks.Decks) != 1 || len(decks.Decks[0].Tags) != 1 || decks.Decks[0].Tags[0] != "spanish" {
		t.Errorf("list_decks should carry tag names: %+v", decks.Decks)
	}

	var renamed TagOutput
	decodeStructured(t, call("rename_tag", map[string]any{"tagId": tagged.TagID, "name": "español"}), &renamed)
	if renamed.Name != "español" {
		t.Errorf("rename_tag: %+v", renamed)
	}

	decodeStructured(t, call("untag_deck", map[string]any{"deckId": deckID, "tag": "español"}), &tagged)
	decodeStructured(t, call("list_tags", nil), &tags)
	if len(tags.Tags) != 1 || len(tags.Tags[0].Decks) != 0 {
		t.Errorf("untag_deck should keep the tag but drop the link: %+v", tags)
	}

	if res := call("untag_deck", map[string]any{"deckId": deckID, "tag": "missing"}); !res.IsError {
		t.Error("untagging an unknown tag should be a tool error")
	}

	var deleted DeleteTagOutput
	decodeStructured(t, call("delete_tag", map[string]any{"tagId": tagged.TagID}), &deleted)
	decodeStructured(t, call("list_tags", nil), &tags)
	if !deleted.Deleted || len(tags.Tags) != 0 {
		t.Errorf("delete_tag: %+v, remaining %+v", deleted, tags)
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

// onePixelPNG is a valid 1x1 transparent PNG, used as fixture image bytes.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

// TestGetCardImage seeds a fact with an image file already on disk (create_card
// only accepts http(s) sources, so the fixture is written directly via the
// cards service — see newTestCards) and checks get_card_image returns it as
// inline image content with the right bytes and MIME type, honours the side
// filter, and reports tool errors for a missing side, an unknown side value,
// and an unknown factId.
func TestGetCardImage(t *testing.T) {
	ctx := context.Background()
	cardsService, mediaDir := newTestCards(t)

	deck, err := cardsService.AddDeck(ctx, auth.LocalUserID, "Test Deck", "", "", "")
	if err != nil {
		t.Fatalf("add deck: %v", err)
	}
	pngBytes, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatalf("decode fixture png: %v", err)
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media dir: %v", err)
	}
	const imgName = "front.png"
	if err := os.WriteFile(filepath.Join(mediaDir, imgName), pngBytes, 0o644); err != nil {
		t.Fatalf("write fixture image: %v", err)
	}
	fact, err := cardsService.AddFact(ctx, auth.LocalUserID, "q", "a", deck.ID, srs.GradeUnseen, false, imgName, "", "", "")
	if err != nil {
		t.Fatalf("add fact: %v", err)
	}

	cs := newTestSessionFor(t, cardsService)
	call := func(args map[string]any) *mcpsdk.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "get_card_image", Arguments: args})
		if err != nil {
			t.Fatalf("get_card_image: %v", err)
		}
		return res
	}

	res := call(map[string]any{"factId": fact.ID, "side": "front"})
	if res.IsError {
		t.Fatalf("get_card_image returned an error: %+v", res.Content)
	}
	var out GetCardImageOutput
	decodeStructured(t, res, &out)
	if out.ImageFront != imgName || out.ImageBack != "" {
		t.Errorf("output = %+v, want ImageFront %q and empty ImageBack", out, imgName)
	}
	var img *mcpsdk.ImageContent
	for _, c := range res.Content {
		if ic, ok := c.(*mcpsdk.ImageContent); ok {
			img = ic
		}
	}
	if img == nil {
		t.Fatalf("expected an ImageContent block in the result, got %+v", res.Content)
	}
	if img.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", img.MIMEType)
	}
	if !bytes.Equal(img.Data, pngBytes) {
		t.Error("returned image bytes do not match the stored file")
	}

	if res := call(map[string]any{"factId": fact.ID}); res.IsError {
		t.Errorf("omitting side should return whichever sides have images, not error: %+v", res.Content)
	}
	if res := call(map[string]any{"factId": fact.ID, "side": "back"}); !res.IsError {
		t.Error("expected a tool error when the requested side has no image")
	}
	if res := call(map[string]any{"factId": fact.ID, "side": "sideways"}); !res.IsError {
		t.Error("expected a tool error for an invalid side value")
	}
	if res := call(map[string]any{"factId": "no-such-fact"}); !res.IsError {
		t.Error("expected a tool error for an unknown factId")
	}
}
