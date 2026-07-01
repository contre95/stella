package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/features/config"
	"github.com/contre95/stella/src/infra/database"
	"github.com/contre95/stella/src/srs"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestSession wires the MCP server onto a temp database and connects an
// in-memory client, returning a ready-to-call session.
func newTestSession(t *testing.T) *mcpsdk.ClientSession {
	t.Helper()
	dir := t.TempDir()
	db, err := database.NewSqliteSRS(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg, err := config.NewManager(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	scheduler := srs.NewScheduler(
		srs.Params{DayStartsAt: 3, NonMemorisedCardsInHand: 10},
		time.UTC,
		time.Now,
		srs.NewMathRand(1),
	)
	srv := NewServer("stella-test", "0.0.0", cards.NewService(db, scheduler, cfg))

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
	cs := newTestSession(t)
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
	cs := newTestSession(t)
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
	cs := newTestSession(t)
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
	cs := newTestSession(t)
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
	cs := newTestSession(t)
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
	cs := newTestSession(t)
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
