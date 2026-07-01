// Package mcp exposes Stella's card operations over the Model Context Protocol,
// letting an MCP client (Claude, editors, etc.) create and browse cards. It is a
// transport over the cards service — the same layer the web handlers sit in.
package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/srs"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server adapts the cards service onto MCP tool handlers.
type Server struct {
	cards *cards.Service
}

// NewServer builds an MCP server exposing the card tools, backed by the given
// cards service.
func NewServer(name, version string, cardsService *cards.Service) *mcpsdk.Server {
	s := &Server{cards: cardsService}
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: name, Version: version}, nil)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "create_card",
		Description: "Create a flashcard (a \"fact\") in Stella. A forward card " +
			"(front -> back) is always made; set reverse to also make a back -> front " +
			"card. Omit grade to add it as a new, unseen card for the normal review queue.",
	}, s.createCard)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "list_recent_cards",
		Description: "List the most recently created facts, newest first, with the " +
			"scheduling status of each of their sister cards.",
	}, s.listRecentCards)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_deck",
		Description: "Create a new named deck. Returns the deck id to use when creating cards.",
	}, s.createDeck)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_decks",
		Description: "List all decks ordered by name.",
	}, s.listDecks)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_card",
		Description: "Delete a fact (and all its sister cards) by fact ID. Use list_recent_cards to find the factId.",
	}, s.deleteCard)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_deck",
		Description: "Delete a deck and all its facts (which cascade to their cards) by deck ID. Use list_decks to find the deckId.",
	}, s.deleteDeck)

	return srv
}

// CreateCardInput is the argument schema for create_card.
type CreateCardInput struct {
	Front      string `json:"front" jsonschema:"The front side / question of the card. Required."`
	Back       string `json:"back,omitempty" jsonschema:"The back side / answer of the card."`
	Reverse    bool   `json:"reverse,omitempty" jsonschema:"Also create a reverse card quizzing back -> front. Ignored when back is empty."`
	Grade      *int   `json:"grade,omitempty" jsonschema:"Initial self-grade 0-5 (0 = blackout, 5 = perfect recall), counted as a first repetition. Omit to leave the card unseen so the review process grades it."`
	DeckID     string `json:"deckId" jsonschema:"Deck id to file the card under. Required — use list_decks or create_deck first."`
	ImageFront string `json:"imageFront,omitempty" jsonschema:"URL of an image to show on the front side. Downloaded and stored locally."`
	ImageBack  string `json:"imageBack,omitempty" jsonschema:"URL of an image to show on the back side. Downloaded and stored locally."`
	AudioFront string `json:"audioFront,omitempty" jsonschema:"URL of an audio file to play on the front side. Downloaded and stored locally. Supports mp3, ogg, wav, m4a, webm."`
	AudioBack  string `json:"audioBack,omitempty" jsonschema:"URL of an audio file to play on the back side. Downloaded and stored locally. Supports mp3, ogg, wav, m4a, webm."`
}

// CreateCardOutput is the result schema for create_card.
type CreateCardOutput struct {
	FactID     string `json:"factId"`
	Front      string `json:"front"`
	Back       string `json:"back"`
	CardsAdded int    `json:"cardsAdded"`
}

func (s *Server) createCard(ctx context.Context, _ *mcpsdk.CallToolRequest, in CreateCardInput) (*mcpsdk.CallToolResult, CreateCardOutput, error) {
	front := strings.TrimSpace(in.Front)
	if front == "" {
		return nil, CreateCardOutput{}, fmt.Errorf("front is required")
	}
	if strings.TrimSpace(in.DeckID) == "" {
		return nil, CreateCardOutput{}, fmt.Errorf("deckId is required — use list_decks or create_deck first")
	}
	grade := srs.GradeUnseen
	if in.Grade != nil {
		grade = *in.Grade
		if grade < 0 || grade > 5 {
			return nil, CreateCardOutput{}, fmt.Errorf("grade must be between 0 and 5")
		}
	}
	imgFront, err := s.saveImageFromURL(in.ImageFront)
	if err != nil {
		return nil, CreateCardOutput{}, fmt.Errorf("saving front image: %w", err)
	}
	imgBack, err := s.saveImageFromURL(in.ImageBack)
	if err != nil {
		s.removeMedia(imgFront)
		return nil, CreateCardOutput{}, fmt.Errorf("saving back image: %w", err)
	}
	audFront, err := s.saveAudioFromURL(in.AudioFront)
	if err != nil {
		s.removeMedia(imgFront, imgBack)
		return nil, CreateCardOutput{}, fmt.Errorf("saving front audio: %w", err)
	}
	audBack, err := s.saveAudioFromURL(in.AudioBack)
	if err != nil {
		s.removeMedia(imgFront, imgBack, audFront)
		return nil, CreateCardOutput{}, fmt.Errorf("saving back audio: %w", err)
	}
	fact, err := s.cards.AddFact(ctx, front, in.Back, in.DeckID, grade, in.Reverse, imgFront, imgBack, audFront, audBack)
	if err != nil {
		s.removeMedia(imgFront, imgBack, audFront, audBack)
		return nil, CreateCardOutput{}, fmt.Errorf("creating card: %w", err)
	}
	added := 1
	if in.Reverse && strings.TrimSpace(in.Back) != "" {
		added = 2
	}
	return nil, CreateCardOutput{
		FactID:     fact.ID,
		Front:      fact.Front,
		Back:       fact.Back,
		CardsAdded: added,
	}, nil
}

// ListRecentCardsInput is the argument schema for list_recent_cards.
type ListRecentCardsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Maximum number of facts to return, newest first. Defaults to 20."`
}

// FactSummary is one fact with its sister cards in list_recent_cards output.
type FactSummary struct {
	FactID string        `json:"factId"`
	Front  string        `json:"front"`
	Back   string        `json:"back"`
	DeckID string        `json:"deckId,omitempty"`
	Cards  []CardSummary `json:"cards"`
}

// CardSummary is a sister card's direction and scheduling status.
type CardSummary struct {
	View   string `json:"view"`
	Status string `json:"status"`
}

// ListRecentCardsOutput is the result schema for list_recent_cards.
type ListRecentCardsOutput struct {
	Facts []FactSummary `json:"facts"`
}

func (s *Server) listRecentCards(ctx context.Context, _ *mcpsdk.CallToolRequest, in ListRecentCardsInput) (*mcpsdk.CallToolResult, ListRecentCardsOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.cards.ListFacts(ctx)
	if err != nil {
		return nil, ListRecentCardsOutput{}, fmt.Errorf("listing cards: %w", err)
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := ListRecentCardsOutput{Facts: make([]FactSummary, 0, len(rows))}
	for _, r := range rows {
		sisters := make([]CardSummary, 0, len(r.Cards))
		for _, c := range r.Cards {
			sisters = append(sisters, CardSummary{View: string(c.Card.View), Status: c.Status})
		}
		out.Facts = append(out.Facts, FactSummary{
			FactID: r.Fact.ID,
			Front:  r.Fact.Front,
			Back:   r.Fact.Back,
			DeckID: r.Fact.DeckID,
			Cards:  sisters,
		})
	}
	return nil, out, nil
}

// CreateDeckInput is the argument schema for create_deck.
type CreateDeckInput struct {
	Name string `json:"name" jsonschema:"Name of the deck. Required."`
}

// CreateDeckOutput is the result schema for create_deck.
type CreateDeckOutput struct {
	DeckID string `json:"deckId"`
	Name   string `json:"name"`
}

func (s *Server) createDeck(ctx context.Context, _ *mcpsdk.CallToolRequest, in CreateDeckInput) (*mcpsdk.CallToolResult, CreateDeckOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, CreateDeckOutput{}, fmt.Errorf("name is required")
	}
	d, err := s.cards.AddDeck(ctx, name, "")
	if err != nil {
		return nil, CreateDeckOutput{}, fmt.Errorf("creating deck: %w", err)
	}
	return nil, CreateDeckOutput{DeckID: d.ID, Name: d.Name}, nil
}

// ListDecksInput is the argument schema for list_decks.
type ListDecksInput struct{}

// DeckSummary is one deck in list_decks output.
type DeckSummary struct {
	DeckID string `json:"deckId"`
	Name   string `json:"name"`
}

// ListDecksOutput is the result schema for list_decks.
type ListDecksOutput struct {
	Decks []DeckSummary `json:"decks"`
}

func (s *Server) listDecks(ctx context.Context, _ *mcpsdk.CallToolRequest, _ ListDecksInput) (*mcpsdk.CallToolResult, ListDecksOutput, error) {
	decks, err := s.cards.ListDecks(ctx)
	if err != nil {
		return nil, ListDecksOutput{}, fmt.Errorf("listing decks: %w", err)
	}
	out := ListDecksOutput{Decks: make([]DeckSummary, 0, len(decks))}
	for _, d := range decks {
		out.Decks = append(out.Decks, DeckSummary{DeckID: d.ID, Name: d.Name})
	}
	return nil, out, nil
}

// DeleteCardInput is the argument schema for delete_card.
type DeleteCardInput struct {
	FactID string `json:"factId" jsonschema:"Fact ID to delete (along with all its sister cards). Required."`
}

// DeleteCardOutput is the result schema for delete_card.
type DeleteCardOutput struct {
	Deleted bool   `json:"deleted"`
	FactID  string `json:"factId"`
}

func (s *Server) deleteCard(ctx context.Context, _ *mcpsdk.CallToolRequest, in DeleteCardInput) (*mcpsdk.CallToolResult, DeleteCardOutput, error) {
	if strings.TrimSpace(in.FactID) == "" {
		return nil, DeleteCardOutput{}, fmt.Errorf("factId is required")
	}
	if err := s.cards.DeleteFact(ctx, in.FactID); err != nil {
		return nil, DeleteCardOutput{}, fmt.Errorf("deleting card: %w", err)
	}
	return nil, DeleteCardOutput{Deleted: true, FactID: in.FactID}, nil
}

// DeleteDeckInput is the argument schema for delete_deck.
type DeleteDeckInput struct {
	DeckID string `json:"deckId" jsonschema:"Deck ID to delete (along with all its facts and cards). Required."`
}

// DeleteDeckOutput is the result schema for delete_deck.
type DeleteDeckOutput struct {
	Deleted bool   `json:"deleted"`
	DeckID  string `json:"deckId"`
}

func (s *Server) deleteDeck(ctx context.Context, _ *mcpsdk.CallToolRequest, in DeleteDeckInput) (*mcpsdk.CallToolResult, DeleteDeckOutput, error) {
	if strings.TrimSpace(in.DeckID) == "" {
		return nil, DeleteDeckOutput{}, fmt.Errorf("deckId is required")
	}
	if err := s.cards.DeleteDeck(ctx, in.DeckID); err != nil {
		return nil, DeleteDeckOutput{}, fmt.Errorf("deleting deck: %w", err)
	}
	return nil, DeleteDeckOutput{Deleted: true, DeckID: in.DeckID}, nil
}

// removeMedia deletes named media files from the media directory, silently
// ignoring missing files and empty names.
func (s *Server) removeMedia(filenames ...string) {
	for _, f := range filenames {
		if f != "" {
			os.Remove(filepath.Join(s.cards.ImagePath(), f))
		}
	}
}

// saveAudioFromURL downloads a URL and saves it as a local audio file.
// Returns the filename, or empty string if url is empty.
func (s *Server) saveAudioFromURL(url string) (string, error) {
	if url == "" {
		return "", nil
	}
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("downloading audio: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading audio: HTTP %s", resp.Status)
	}
	var ext string
	switch resp.Header.Get("Content-Type") {
	case "audio/mpeg":
		ext = ".mp3"
	case "audio/ogg":
		ext = ".ogg"
	case "audio/wav", "audio/x-wav":
		ext = ".wav"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		ext = ".m4a"
	case "audio/webm":
		ext = ".webm"
	case "audio/aac":
		ext = ".aac"
	case "audio/flac":
		ext = ".flac"
	default:
		return "", fmt.Errorf("unsupported audio type: %s", resp.Header.Get("Content-Type"))
	}
	imagesPath := s.cards.ImagePath()
	if err := os.MkdirAll(imagesPath, 0o755); err != nil {
		return "", err
	}
	filename := uuid.NewString() + ext
	dest := filepath.Join(imagesPath, filename)
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(dest)
		return "", err
	}
	return filename, nil
}

// saveImageFromURL downloads a URL and saves it as a local image file.
// Returns the filename, or empty string if url is empty.
func (s *Server) saveImageFromURL(url string) (string, error) {
	if url == "" {
		return "", nil
	}
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("downloading image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading image: HTTP %s", resp.Status)
	}
	var ext string
	switch resp.Header.Get("Content-Type") {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		return "", fmt.Errorf("unsupported image type: %s", resp.Header.Get("Content-Type"))
	}
	imagesPath := s.cards.ImagePath()
	if err := os.MkdirAll(imagesPath, 0o755); err != nil {
		return "", err
	}
	filename := uuid.NewString() + ext
	dest := filepath.Join(imagesPath, filename)
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(dest)
		return "", err
	}
	return filename, nil
}
