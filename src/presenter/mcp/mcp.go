// Package mcp exposes Stella's card operations over the Model Context Protocol,
// letting an MCP client (Claude, editors, etc.) create and browse cards. It is a
// transport over the cards service — the same layer the web handlers sit in.
package mcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/srs"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mediaHTTPClient fetches MCP-supplied image/audio URLs. Its dialer rejects
// connections to loopback, link-local, and private IP ranges — the create_card
// / edit_card tools are reachable over the network without auth checks beyond
// the MCP bearer (or none, in single-user mode), so an unrestricted client
// would let any caller make the server issue requests to internal services
// (cloud metadata endpoints, LAN admin panels, etc.) and exfiltrate the
// response as a "downloaded" card image. The check re-runs on every dial, so
// it also covers redirects.
var mediaHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if isDisallowedMediaIP(ip) {
					return nil, fmt.Errorf("refusing to connect to non-public address %s", ip)
				}
			}
			d := net.Dialer{Timeout: 10 * time.Second}
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

// isDisallowedMediaIP reports whether ip must not be reached by the media
// fetcher: loopback, link-local (including the cloud metadata address range),
// private, or otherwise non-global unicast.
func isDisallowedMediaIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsUnspecified() || !ip.IsGlobalUnicast()
}

// Server adapts the cards service onto MCP tool handlers. The MCP transport
// has no login, so every tool call runs as the fixed userID it was built with
// (the local account — MCP is only served in single-user mode).
type Server struct {
	cards  *cards.Service
	userID string
}

// NewServer builds an MCP server exposing the card tools, backed by the given
// cards service and operating as userID.
func NewServer(name, version string, cardsService *cards.Service, userID string) *mcpsdk.Server {
	s := &Server{cards: cardsService, userID: userID}
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: name, Version: version}, nil)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "create_card",
		Description: "Create a flashcard (a \"fact\") in Stella. A forward card " +
			"(front -> back) is always made; set reverse to also make a back -> front " +
			"card. Omit grade to add it as a new, unseen card for the normal review queue.",
	}, s.createCard)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "edit_card",
		Description: "Edit an existing fact by fact ID: change its front/back text, " +
			"replace or remove its images and audio, and add or remove the reverse card. " +
			"Use addReverse: true to add the reverse card, or removeReverse: true to delete it. " +
			"Omitted fields keep their current value. Scheduling data is not " +
			"touched. Use list_recent_cards to find the factId and see current content.",
	}, s.editCard)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "list_recent_cards",
		Description: "List the most recently created facts, newest first, with the " +
			"scheduling status of each of their sister cards and any attached media filenames.",
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

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_group",
		Description: "Create a new group that decks can be filed under. Returns the groupId to use when creating or moving decks.",
	}, s.createGroup)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_groups",
		Description: "List all groups with their member decks. Also returns ungrouped decks separately.",
	}, s.listGroups)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "rename_group",
		Description: "Rename a group by ID. Use list_groups to find the groupId.",
	}, s.renameGroup)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_group",
		Description: "Delete a group by ID. Its member decks become ungrouped (they and their cards are not deleted). Use list_groups to find the groupId.",
	}, s.deleteGroup)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_deck_group",
		Description: "Assign a deck to a group, or remove it from its current group by passing an empty groupId. Use list_decks and list_groups to find the IDs.",
	}, s.setDeckGroup)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "create_tag",
		Description: "Create a tag that decks can be labelled with. Tag names are unique; " +
			"if the tag already exists, its existing id is returned. Unlike groups (one per deck), " +
			"a deck can carry any number of tags.",
	}, s.createTag)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_tags",
		Description: "List all tags with the decks carrying each of them.",
	}, s.listTags)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "rename_tag",
		Description: "Rename a tag by ID. Use list_tags to find the tagId.",
	}, s.renameTag)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_tag",
		Description: "Delete a tag by ID. Decks carrying it just lose the label (they and their cards are not deleted). Use list_tags to find the tagId.",
	}, s.deleteTag)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "tag_deck",
		Description: "Label a deck with a tag, given by name. The tag is created if it doesn't exist yet; tagging a deck twice is a no-op.",
	}, s.tagDeck)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "untag_deck",
		Description: "Remove a tag, given by name, from a deck. The tag itself is kept. Use list_decks or list_tags to see current tags.",
	}, s.untagDeck)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "rename_deck",
		Description: "Rename a deck by ID. Use list_decks to find the deckId.",
	}, s.renameDeck)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "set_deck_reverse",
		Description: "Add or remove the reverse (back -> front) card for every fact in a deck in one call. " +
			"Pass enabled: true to add reverse cards to all facts that don't have one yet, or " +
			"enabled: false to remove reverse cards from all facts that have one. " +
			"Facts with no back text are skipped. Returns the number of facts actually changed.",
	}, s.setDeckReverse)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "create_cards",
		Description: "Create multiple flashcards in one call. All cards are filed under the " +
			"same deck. Each item follows the same rules as create_card: a forward card is " +
			"always made; set reverse on an item to also make a back -> front card. On " +
			"failure the tool reports how many cards were created before the error.",
	}, s.createCards)

	return srv
}

// CreateCardInput is the argument schema for create_card.
type CreateCardInput struct {
	Front      string `json:"front" jsonschema:"The front side / question of the card. Required."`
	Back       string `json:"back,omitempty" jsonschema:"The back side / answer of the card."`
	Reverse    bool   `json:"reverse,omitempty" jsonschema:"Also create a reverse card quizzing back -> front. Ignored when back is empty."`
	Grade      *int   `json:"grade,omitempty" jsonschema:"Initial self-grade 0-5 (0 = blackout, 5 = perfect recall), counted as a first repetition. Omit to leave the card unseen so the review process grades it."`
	DeckID     string `json:"deckId" jsonschema:"Deck id to file the card under. Required — use list_decks or create_deck first."`
	ImageFront string `json:"imageFront,omitempty" jsonschema:"http(s) URL of an image to show on the front side. Copied into Stella's media store. Supports jpg, png, gif, webp."`
	ImageBack  string `json:"imageBack,omitempty" jsonschema:"http(s) URL of an image to show on the back side. Copied into Stella's media store. Supports jpg, png, gif, webp."`
	AudioFront string `json:"audioFront,omitempty" jsonschema:"http(s) URL of an audio file to play on the front side. Copied into Stella's media store. Supports mp3, ogg, wav, m4a, webm, aac, flac."`
	AudioBack  string `json:"audioBack,omitempty" jsonschema:"http(s) URL of an audio file to play on the back side. Copied into Stella's media store. Supports mp3, ogg, wav, m4a, webm, aac, flac."`
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
	imgFront, err := s.saveImageFromSource(in.ImageFront)
	if err != nil {
		return nil, CreateCardOutput{}, fmt.Errorf("saving front image: %w", err)
	}
	imgBack, err := s.saveImageFromSource(in.ImageBack)
	if err != nil {
		s.removeMedia(imgFront)
		return nil, CreateCardOutput{}, fmt.Errorf("saving back image: %w", err)
	}
	audFront, err := s.saveAudioFromSource(in.AudioFront)
	if err != nil {
		s.removeMedia(imgFront, imgBack)
		return nil, CreateCardOutput{}, fmt.Errorf("saving front audio: %w", err)
	}
	audBack, err := s.saveAudioFromSource(in.AudioBack)
	if err != nil {
		s.removeMedia(imgFront, imgBack, audFront)
		return nil, CreateCardOutput{}, fmt.Errorf("saving back audio: %w", err)
	}
	fact, err := s.cards.AddFact(ctx, s.userID, front, in.Back, in.DeckID, grade, in.Reverse, imgFront, imgBack, audFront, audBack)
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

// EditCardInput is the argument schema for edit_card.
type EditCardInput struct {
	FactID           string  `json:"factId" jsonschema:"Fact ID of the card to edit. Required — use list_recent_cards to find it."`
	Front            *string `json:"front,omitempty" jsonschema:"New front text. Omit to keep the current front."`
	Back             *string `json:"back,omitempty" jsonschema:"New back text. Omit to keep the current back; pass an empty string to clear it (which also removes any reverse card)."`
	AddReverse       bool    `json:"addReverse,omitempty" jsonschema:"Set to true to create the reverse (back -> front) card if it does not already exist."`
	RemoveReverse    bool    `json:"removeReverse,omitempty" jsonschema:"Set to true to delete the reverse (back -> front) card (its learning history is lost). Ignored when addReverse is also true."`
	ImageFront       string  `json:"imageFront,omitempty" jsonschema:"http(s) URL of a new front image; replaces the current one. Supports jpg, png, gif, webp."`
	ImageBack        string  `json:"imageBack,omitempty" jsonschema:"http(s) URL of a new back image; replaces the current one. Supports jpg, png, gif, webp."`
	AudioFront       string  `json:"audioFront,omitempty" jsonschema:"http(s) URL of a new front audio file; replaces the current one. Supports mp3, ogg, wav, m4a, webm, aac, flac."`
	AudioBack        string  `json:"audioBack,omitempty" jsonschema:"http(s) URL of a new back audio file; replaces the current one. Supports mp3, ogg, wav, m4a, webm, aac, flac."`
	RemoveImageFront bool    `json:"removeImageFront,omitempty" jsonschema:"Remove the front image."`
	RemoveImageBack  bool    `json:"removeImageBack,omitempty" jsonschema:"Remove the back image."`
	RemoveAudioFront bool    `json:"removeAudioFront,omitempty" jsonschema:"Remove the front audio."`
	RemoveAudioBack  bool    `json:"removeAudioBack,omitempty" jsonschema:"Remove the back audio."`
}

// EditCardOutput is the result schema for edit_card.
type EditCardOutput struct {
	FactID     string `json:"factId"`
	Front      string `json:"front"`
	Back       string `json:"back"`
	Reverse    bool   `json:"reverse"`
	ImageFront string `json:"imageFront,omitempty"`
	ImageBack  string `json:"imageBack,omitempty"`
	AudioFront string `json:"audioFront,omitempty"`
	AudioBack  string `json:"audioBack,omitempty"`
}

func (s *Server) editCard(ctx context.Context, _ *mcpsdk.CallToolRequest, in EditCardInput) (*mcpsdk.CallToolResult, EditCardOutput, error) {
	if strings.TrimSpace(in.FactID) == "" {
		return nil, EditCardOutput{}, fmt.Errorf("factId is required — use list_recent_cards to find it")
	}
	fact, err := s.cards.GetFact(ctx, s.userID, in.FactID)
	if err != nil {
		return nil, EditCardOutput{}, fmt.Errorf("looking up fact: %w", err)
	}
	front := fact.Front
	if in.Front != nil {
		front = *in.Front
	}
	back := fact.Back
	if in.Back != nil {
		back = *in.Back
	}
	var saved []string
	slot := func(source string, remove bool, save func(string) (string, error)) (*string, error) {
		if source != "" {
			name, err := save(source)
			if err != nil {
				return nil, err
			}
			saved = append(saved, name)
			return &name, nil
		}
		if remove {
			empty := ""
			return &empty, nil
		}
		return nil, nil
	}
	imgFront, err := slot(in.ImageFront, in.RemoveImageFront, s.saveImageFromSource)
	if err != nil {
		s.removeMedia(saved...)
		return nil, EditCardOutput{}, fmt.Errorf("saving front image: %w", err)
	}
	imgBack, err := slot(in.ImageBack, in.RemoveImageBack, s.saveImageFromSource)
	if err != nil {
		s.removeMedia(saved...)
		return nil, EditCardOutput{}, fmt.Errorf("saving back image: %w", err)
	}
	audFront, err := slot(in.AudioFront, in.RemoveAudioFront, s.saveAudioFromSource)
	if err != nil {
		s.removeMedia(saved...)
		return nil, EditCardOutput{}, fmt.Errorf("saving front audio: %w", err)
	}
	audBack, err := slot(in.AudioBack, in.RemoveAudioBack, s.saveAudioFromSource)
	if err != nil {
		s.removeMedia(saved...)
		return nil, EditCardOutput{}, fmt.Errorf("saving back audio: %w", err)
	}
	var reversePtr *bool
	if in.AddReverse {
		t := true
		reversePtr = &t
	} else if in.RemoveReverse {
		f := false
		reversePtr = &f
	}
	updated, err := s.cards.UpdateFact(ctx, s.userID, in.FactID, front, back, imgFront, imgBack, audFront, audBack, reversePtr)
	if err != nil {
		s.removeMedia(saved...)
		return nil, EditCardOutput{}, fmt.Errorf("updating card: %w", err)
	}
	hasReverse, err := s.cards.HasReverse(ctx, s.userID, in.FactID)
	if err != nil {
		return nil, EditCardOutput{}, fmt.Errorf("checking reverse card: %w", err)
	}
	return nil, EditCardOutput{
		FactID:     updated.ID,
		Front:      updated.Front,
		Back:       updated.Back,
		Reverse:    hasReverse,
		ImageFront: updated.ImageFront,
		ImageBack:  updated.ImageBack,
		AudioFront: updated.AudioFront,
		AudioBack:  updated.AudioBack,
	}, nil
}

// ListRecentCardsInput is the argument schema for list_recent_cards.
type ListRecentCardsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Maximum number of facts to return, newest first. Defaults to 20."`
}

// FactSummary is one fact with its sister cards in list_recent_cards output.
type FactSummary struct {
	FactID     string        `json:"factId"`
	Front      string        `json:"front"`
	Back       string        `json:"back"`
	DeckID     string        `json:"deckId,omitempty"`
	ImageFront string        `json:"imageFront,omitempty"`
	ImageBack  string        `json:"imageBack,omitempty"`
	AudioFront string        `json:"audioFront,omitempty"`
	AudioBack  string        `json:"audioBack,omitempty"`
	Cards      []CardSummary `json:"cards"`
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
	rows, err := s.cards.ListFacts(ctx, s.userID, "", limit, 0)
	if err != nil {
		return nil, ListRecentCardsOutput{}, fmt.Errorf("listing cards: %w", err)
	}
	out := ListRecentCardsOutput{Facts: make([]FactSummary, 0, len(rows))}
	for _, r := range rows {
		sisters := make([]CardSummary, 0, len(r.Cards))
		for _, c := range r.Cards {
			sisters = append(sisters, CardSummary{View: string(c.Card.View), Status: c.Status})
		}
		out.Facts = append(out.Facts, FactSummary{
			FactID:     r.Fact.ID,
			Front:      r.Fact.Front,
			Back:       r.Fact.Back,
			DeckID:     r.Fact.DeckID,
			ImageFront: r.Fact.ImageFront,
			ImageBack:  r.Fact.ImageBack,
			AudioFront: r.Fact.AudioFront,
			AudioBack:  r.Fact.AudioBack,
			Cards:      sisters,
		})
	}
	return nil, out, nil
}

// CreateDeckInput is the argument schema for create_deck.
type CreateDeckInput struct {
	Name        string `json:"name" jsonschema:"Name of the deck. Required."`
	Description string `json:"description,omitempty" jsonschema:"Optional description for the deck."`
	GroupID     string `json:"groupId,omitempty" jsonschema:"Optional group ID to file the deck under. Use list_groups or create_group first."`
}

// CreateDeckOutput is the result schema for create_deck.
type CreateDeckOutput struct {
	DeckID  string `json:"deckId"`
	Name    string `json:"name"`
	GroupID string `json:"groupId,omitempty"`
}

func (s *Server) createDeck(ctx context.Context, _ *mcpsdk.CallToolRequest, in CreateDeckInput) (*mcpsdk.CallToolResult, CreateDeckOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, CreateDeckOutput{}, fmt.Errorf("name is required")
	}
	d, err := s.cards.AddDeck(ctx, s.userID, name, in.Description, "", strings.TrimSpace(in.GroupID))
	if err != nil {
		return nil, CreateDeckOutput{}, fmt.Errorf("creating deck: %w", err)
	}
	return nil, CreateDeckOutput{DeckID: d.ID, Name: d.Name, GroupID: d.GroupID}, nil
}

// ListDecksInput is the argument schema for list_decks.
type ListDecksInput struct{}

// DeckSummary is one deck in list_decks output.
type DeckSummary struct {
	DeckID  string   `json:"deckId"`
	Name    string   `json:"name"`
	GroupID string   `json:"groupId,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// ListDecksOutput is the result schema for list_decks.
type ListDecksOutput struct {
	Decks []DeckSummary `json:"decks"`
}

// tagNamesByDeck maps every tagged deck ID to its tag names, name-ordered.
func (s *Server) tagNamesByDeck(ctx context.Context) (map[string][]string, error) {
	byDeck, err := s.cards.TagsByDeck(ctx, s.userID)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(byDeck))
	for deckID, tags := range byDeck {
		names := make([]string, 0, len(tags))
		for _, t := range tags {
			names = append(names, t.Name)
		}
		out[deckID] = names
	}
	return out, nil
}

func (s *Server) listDecks(ctx context.Context, _ *mcpsdk.CallToolRequest, _ ListDecksInput) (*mcpsdk.CallToolResult, ListDecksOutput, error) {
	decks, err := s.cards.ListDecks(ctx, s.userID)
	if err != nil {
		return nil, ListDecksOutput{}, fmt.Errorf("listing decks: %w", err)
	}
	tagNames, err := s.tagNamesByDeck(ctx)
	if err != nil {
		return nil, ListDecksOutput{}, fmt.Errorf("listing deck tags: %w", err)
	}
	out := ListDecksOutput{Decks: make([]DeckSummary, 0, len(decks))}
	for _, d := range decks {
		out.Decks = append(out.Decks, DeckSummary{DeckID: d.ID, Name: d.Name, GroupID: d.GroupID, Tags: tagNames[d.ID]})
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
	if err := s.cards.DeleteFact(ctx, s.userID, in.FactID); err != nil {
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
	if err := s.cards.DeleteDeck(ctx, s.userID, in.DeckID); err != nil {
		return nil, DeleteDeckOutput{}, fmt.Errorf("deleting deck: %w", err)
	}
	return nil, DeleteDeckOutput{Deleted: true, DeckID: in.DeckID}, nil
}

// CreateGroupInput is the argument schema for create_group.
type CreateGroupInput struct {
	Name string `json:"name" jsonschema:"Name of the group. Required."`
}

// CreateGroupOutput is the result schema for create_group.
type CreateGroupOutput struct {
	GroupID string `json:"groupId"`
	Name    string `json:"name"`
}

func (s *Server) createGroup(ctx context.Context, _ *mcpsdk.CallToolRequest, in CreateGroupInput) (*mcpsdk.CallToolResult, CreateGroupOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, CreateGroupOutput{}, fmt.Errorf("name is required")
	}
	g, err := s.cards.AddGroup(ctx, s.userID, name)
	if err != nil {
		return nil, CreateGroupOutput{}, fmt.Errorf("creating group: %w", err)
	}
	return nil, CreateGroupOutput{GroupID: g.ID, Name: g.Name}, nil
}

// ListGroupsInput is the argument schema for list_groups.
type ListGroupsInput struct{}

// GroupSummary is one group with its decks in list_groups output.
type GroupSummary struct {
	GroupID string        `json:"groupId"`
	Name    string        `json:"name"`
	Decks   []DeckSummary `json:"decks"`
}

// ListGroupsOutput is the result schema for list_groups.
type ListGroupsOutput struct {
	Groups    []GroupSummary `json:"groups"`
	Ungrouped []DeckSummary  `json:"ungrouped"`
}

func (s *Server) listGroups(ctx context.Context, _ *mcpsdk.CallToolRequest, _ ListGroupsInput) (*mcpsdk.CallToolResult, ListGroupsOutput, error) {
	views, err := s.cards.ListGroupViews(ctx, s.userID, "")
	if err != nil {
		return nil, ListGroupsOutput{}, fmt.Errorf("listing groups: %w", err)
	}
	tagNames, err := s.tagNamesByDeck(ctx)
	if err != nil {
		return nil, ListGroupsOutput{}, fmt.Errorf("listing deck tags: %w", err)
	}
	out := ListGroupsOutput{Groups: make([]GroupSummary, 0), Ungrouped: make([]DeckSummary, 0)}
	for _, v := range views {
		decks := make([]DeckSummary, 0, len(v.Decks))
		for _, d := range v.Decks {
			decks = append(decks, DeckSummary{DeckID: d.ID, Name: d.Name, GroupID: d.GroupID, Tags: tagNames[d.ID]})
		}
		if v.Group == nil {
			out.Ungrouped = decks
		} else {
			out.Groups = append(out.Groups, GroupSummary{GroupID: v.Group.ID, Name: v.Group.Name, Decks: decks})
		}
	}
	return nil, out, nil
}

// RenameGroupInput is the argument schema for rename_group.
type RenameGroupInput struct {
	GroupID string `json:"groupId" jsonschema:"Group ID to rename. Required — use list_groups to find it."`
	Name    string `json:"name" jsonschema:"New name for the group. Required."`
}

// RenameGroupOutput is the result schema for rename_group.
type RenameGroupOutput struct {
	GroupID string `json:"groupId"`
	Name    string `json:"name"`
}

func (s *Server) renameGroup(ctx context.Context, _ *mcpsdk.CallToolRequest, in RenameGroupInput) (*mcpsdk.CallToolResult, RenameGroupOutput, error) {
	if strings.TrimSpace(in.GroupID) == "" {
		return nil, RenameGroupOutput{}, fmt.Errorf("groupId is required")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, RenameGroupOutput{}, fmt.Errorf("name is required")
	}
	g, err := s.cards.UpdateGroup(ctx, s.userID, in.GroupID, name)
	if err != nil {
		return nil, RenameGroupOutput{}, fmt.Errorf("renaming group: %w", err)
	}
	return nil, RenameGroupOutput{GroupID: g.ID, Name: g.Name}, nil
}

// DeleteGroupInput is the argument schema for delete_group.
type DeleteGroupInput struct {
	GroupID string `json:"groupId" jsonschema:"Group ID to delete. Its decks become ungrouped. Required."`
}

// DeleteGroupOutput is the result schema for delete_group.
type DeleteGroupOutput struct {
	Deleted bool   `json:"deleted"`
	GroupID string `json:"groupId"`
}

func (s *Server) deleteGroup(ctx context.Context, _ *mcpsdk.CallToolRequest, in DeleteGroupInput) (*mcpsdk.CallToolResult, DeleteGroupOutput, error) {
	if strings.TrimSpace(in.GroupID) == "" {
		return nil, DeleteGroupOutput{}, fmt.Errorf("groupId is required")
	}
	if err := s.cards.DeleteGroup(ctx, s.userID, in.GroupID); err != nil {
		return nil, DeleteGroupOutput{}, fmt.Errorf("deleting group: %w", err)
	}
	return nil, DeleteGroupOutput{Deleted: true, GroupID: in.GroupID}, nil
}

// RenameDeckInput is the argument schema for rename_deck.
type RenameDeckInput struct {
	DeckID      string  `json:"deckId" jsonschema:"Deck ID to rename. Required — use list_decks to find it."`
	Name        string  `json:"name" jsonschema:"New name for the deck. Required. Maximum 32 characters."`
	Description *string `json:"description,omitempty" jsonschema:"Optional new description for the deck. Omit to leave unchanged; pass an empty string to clear it."`
}

// RenameDeckOutput is the result schema for rename_deck.
type RenameDeckOutput struct {
	DeckID      string `json:"deckId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (s *Server) renameDeck(ctx context.Context, _ *mcpsdk.CallToolRequest, in RenameDeckInput) (*mcpsdk.CallToolResult, RenameDeckOutput, error) {
	if strings.TrimSpace(in.DeckID) == "" {
		return nil, RenameDeckOutput{}, fmt.Errorf("deckId is required")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, RenameDeckOutput{}, fmt.Errorf("name is required")
	}
	if len([]rune(name)) > 32 {
		name = string([]rune(name)[:32])
	}
	d, err := s.cards.RenameDeck(ctx, s.userID, in.DeckID, name)
	if err != nil {
		return nil, RenameDeckOutput{}, fmt.Errorf("renaming deck: %w", err)
	}
	if in.Description != nil {
		d, err = s.cards.UpdateDeckDescription(ctx, s.userID, in.DeckID, strings.TrimSpace(*in.Description))
		if err != nil {
			return nil, RenameDeckOutput{}, fmt.Errorf("updating deck description: %w", err)
		}
	}
	return nil, RenameDeckOutput{DeckID: d.ID, Name: d.Name, Description: d.Description}, nil
}

// SetDeckGroupInput is the argument schema for set_deck_group.
type SetDeckGroupInput struct {
	DeckID  string `json:"deckId" jsonschema:"Deck ID to move. Required — use list_decks to find it."`
	GroupID string `json:"groupId,omitempty" jsonschema:"Group ID to assign the deck to. Omit or pass empty to remove the deck from its current group."`
}

// SetDeckGroupOutput is the result schema for set_deck_group.
type SetDeckGroupOutput struct {
	DeckID  string `json:"deckId"`
	GroupID string `json:"groupId,omitempty"`
}

func (s *Server) setDeckGroup(ctx context.Context, _ *mcpsdk.CallToolRequest, in SetDeckGroupInput) (*mcpsdk.CallToolResult, SetDeckGroupOutput, error) {
	if strings.TrimSpace(in.DeckID) == "" {
		return nil, SetDeckGroupOutput{}, fmt.Errorf("deckId is required")
	}
	if err := s.cards.SetDeckGroup(ctx, s.userID, in.DeckID, strings.TrimSpace(in.GroupID)); err != nil {
		return nil, SetDeckGroupOutput{}, fmt.Errorf("setting deck group: %w", err)
	}
	return nil, SetDeckGroupOutput{DeckID: in.DeckID, GroupID: strings.TrimSpace(in.GroupID)}, nil
}

// CreateTagInput is the argument schema for create_tag.
type CreateTagInput struct {
	Name string `json:"name" jsonschema:"Name of the tag. Required. If a tag with this name already exists, it is returned instead of creating a duplicate."`
}

// TagOutput echoes a tag's identity; the result schema for create_tag and rename_tag.
type TagOutput struct {
	TagID string `json:"tagId"`
	Name  string `json:"name"`
}

func (s *Server) createTag(ctx context.Context, _ *mcpsdk.CallToolRequest, in CreateTagInput) (*mcpsdk.CallToolResult, TagOutput, error) {
	tag, err := s.cards.EnsureTag(ctx, s.userID, in.Name)
	if err != nil {
		return nil, TagOutput{}, fmt.Errorf("creating tag: %w", err)
	}
	return nil, TagOutput{TagID: tag.ID, Name: tag.Name}, nil
}

// ListTagsInput is the argument schema for list_tags.
type ListTagsInput struct{}

// TagSummary is one tag with its decks in list_tags output.
type TagSummary struct {
	TagID string        `json:"tagId"`
	Name  string        `json:"name"`
	Decks []DeckSummary `json:"decks"`
}

// ListTagsOutput is the result schema for list_tags.
type ListTagsOutput struct {
	Tags []TagSummary `json:"tags"`
}

func (s *Server) listTags(ctx context.Context, _ *mcpsdk.CallToolRequest, _ ListTagsInput) (*mcpsdk.CallToolResult, ListTagsOutput, error) {
	tags, err := s.cards.ListTags(ctx, s.userID)
	if err != nil {
		return nil, ListTagsOutput{}, fmt.Errorf("listing tags: %w", err)
	}
	decks, err := s.cards.ListDecks(ctx, s.userID)
	if err != nil {
		return nil, ListTagsOutput{}, fmt.Errorf("listing decks: %w", err)
	}
	byDeck, err := s.cards.TagsByDeck(ctx, s.userID)
	if err != nil {
		return nil, ListTagsOutput{}, fmt.Errorf("listing deck tags: %w", err)
	}
	decksByTag := map[string][]DeckSummary{}
	for _, d := range decks {
		for _, t := range byDeck[d.ID] {
			decksByTag[t.ID] = append(decksByTag[t.ID], DeckSummary{DeckID: d.ID, Name: d.Name, GroupID: d.GroupID})
		}
	}
	out := ListTagsOutput{Tags: make([]TagSummary, 0, len(tags))}
	for _, t := range tags {
		ds := decksByTag[t.ID]
		if ds == nil {
			ds = make([]DeckSummary, 0)
		}
		out.Tags = append(out.Tags, TagSummary{TagID: t.ID, Name: t.Name, Decks: ds})
	}
	return nil, out, nil
}

// RenameTagInput is the argument schema for rename_tag.
type RenameTagInput struct {
	TagID string `json:"tagId" jsonschema:"Tag ID to rename. Required — use list_tags to find it."`
	Name  string `json:"name" jsonschema:"New name for the tag. Required."`
}

func (s *Server) renameTag(ctx context.Context, _ *mcpsdk.CallToolRequest, in RenameTagInput) (*mcpsdk.CallToolResult, TagOutput, error) {
	if strings.TrimSpace(in.TagID) == "" {
		return nil, TagOutput{}, fmt.Errorf("tagId is required")
	}
	tag, err := s.cards.RenameTag(ctx, s.userID, in.TagID, in.Name)
	if err != nil {
		return nil, TagOutput{}, fmt.Errorf("renaming tag: %w", err)
	}
	return nil, TagOutput{TagID: tag.ID, Name: tag.Name}, nil
}

// DeleteTagInput is the argument schema for delete_tag.
type DeleteTagInput struct {
	TagID string `json:"tagId" jsonschema:"Tag ID to delete. Decks carrying it just lose the label. Required."`
}

// DeleteTagOutput is the result schema for delete_tag.
type DeleteTagOutput struct {
	Deleted bool   `json:"deleted"`
	TagID   string `json:"tagId"`
}

func (s *Server) deleteTag(ctx context.Context, _ *mcpsdk.CallToolRequest, in DeleteTagInput) (*mcpsdk.CallToolResult, DeleteTagOutput, error) {
	if strings.TrimSpace(in.TagID) == "" {
		return nil, DeleteTagOutput{}, fmt.Errorf("tagId is required")
	}
	if err := s.cards.DeleteTag(ctx, s.userID, in.TagID); err != nil {
		return nil, DeleteTagOutput{}, fmt.Errorf("deleting tag: %w", err)
	}
	return nil, DeleteTagOutput{Deleted: true, TagID: in.TagID}, nil
}

// TagDeckInput is the argument schema for tag_deck.
type TagDeckInput struct {
	DeckID string `json:"deckId" jsonschema:"Deck ID to label. Required — use list_decks to find it."`
	Tag    string `json:"tag" jsonschema:"Tag name to apply. Created if it doesn't exist yet. Required."`
}

// TagDeckOutput is the result schema for tag_deck and untag_deck.
type TagDeckOutput struct {
	DeckID string `json:"deckId"`
	TagID  string `json:"tagId"`
	Tag    string `json:"tag"`
}

func (s *Server) tagDeck(ctx context.Context, _ *mcpsdk.CallToolRequest, in TagDeckInput) (*mcpsdk.CallToolResult, TagDeckOutput, error) {
	if strings.TrimSpace(in.DeckID) == "" {
		return nil, TagDeckOutput{}, fmt.Errorf("deckId is required — use list_decks to find it")
	}
	tag, err := s.cards.TagDeck(ctx, s.userID, in.DeckID, in.Tag)
	if err != nil {
		return nil, TagDeckOutput{}, fmt.Errorf("tagging deck: %w", err)
	}
	return nil, TagDeckOutput{DeckID: in.DeckID, TagID: tag.ID, Tag: tag.Name}, nil
}

// UntagDeckInput is the argument schema for untag_deck.
type UntagDeckInput struct {
	DeckID string `json:"deckId" jsonschema:"Deck ID to remove the tag from. Required — use list_decks to find it."`
	Tag    string `json:"tag" jsonschema:"Name of the tag to remove. Required."`
}

func (s *Server) untagDeck(ctx context.Context, _ *mcpsdk.CallToolRequest, in UntagDeckInput) (*mcpsdk.CallToolResult, TagDeckOutput, error) {
	if strings.TrimSpace(in.DeckID) == "" {
		return nil, TagDeckOutput{}, fmt.Errorf("deckId is required — use list_decks to find it")
	}
	tag, err := s.cards.TagByName(ctx, s.userID, in.Tag)
	if err != nil {
		return nil, TagDeckOutput{}, fmt.Errorf("looking up tag %q: %w", in.Tag, err)
	}
	if err := s.cards.UntagDeck(ctx, s.userID, in.DeckID, tag.ID); err != nil {
		return nil, TagDeckOutput{}, fmt.Errorf("untagging deck: %w", err)
	}
	return nil, TagDeckOutput{DeckID: in.DeckID, TagID: tag.ID, Tag: tag.Name}, nil
}

// BulkCardItem is one card in a create_cards request.
type BulkCardItem struct {
	Front      string `json:"front" jsonschema:"The front side / question of the card. Required."`
	Back       string `json:"back,omitempty" jsonschema:"The back side / answer of the card."`
	Reverse    bool   `json:"reverse,omitempty" jsonschema:"Also create a reverse card quizzing back -> front."`
	Grade      *int   `json:"grade,omitempty" jsonschema:"Initial self-grade 0-5. Omit to leave the card unseen."`
	ImageFront string `json:"imageFront,omitempty" jsonschema:"http(s) URL of an image for the front side."`
	ImageBack  string `json:"imageBack,omitempty" jsonschema:"http(s) URL of an image for the back side."`
	AudioFront string `json:"audioFront,omitempty" jsonschema:"http(s) URL of audio for the front side."`
	AudioBack  string `json:"audioBack,omitempty" jsonschema:"http(s) URL of audio for the back side."`
}

// CreateCardsInput is the argument schema for create_cards.
type CreateCardsInput struct {
	DeckID string         `json:"deckId" jsonschema:"Deck id to file all cards under. Required — use list_decks or create_deck first."`
	Cards  []BulkCardItem `json:"cards" jsonschema:"List of cards to create. Required — must not be empty."`
}

// CreateCardsOutput is the result schema for create_cards.
type CreateCardsOutput struct {
	Created    int                `json:"created"`
	CardsAdded int                `json:"cardsAdded"`
	Facts      []CreateCardOutput `json:"facts"`
}

func (s *Server) createCards(ctx context.Context, _ *mcpsdk.CallToolRequest, in CreateCardsInput) (*mcpsdk.CallToolResult, CreateCardsOutput, error) {
	if strings.TrimSpace(in.DeckID) == "" {
		return nil, CreateCardsOutput{}, fmt.Errorf("deckId is required — use list_decks or create_deck first")
	}
	if len(in.Cards) == 0 {
		return nil, CreateCardsOutput{}, fmt.Errorf("cards must not be empty")
	}
	out := CreateCardsOutput{Facts: make([]CreateCardOutput, 0, len(in.Cards))}
	for i, item := range in.Cards {
		front := strings.TrimSpace(item.Front)
		if front == "" {
			return nil, out, fmt.Errorf("card[%d]: front is required", i)
		}
		grade := srs.GradeUnseen
		if item.Grade != nil {
			grade = *item.Grade
			if grade < 0 || grade > 5 {
				return nil, out, fmt.Errorf("card[%d]: grade must be between 0 and 5", i)
			}
		}
		imgFront, err := s.saveImageFromSource(item.ImageFront)
		if err != nil {
			return nil, out, fmt.Errorf("card[%d]: saving front image: %w", i, err)
		}
		imgBack, err := s.saveImageFromSource(item.ImageBack)
		if err != nil {
			s.removeMedia(imgFront)
			return nil, out, fmt.Errorf("card[%d]: saving back image: %w", i, err)
		}
		audFront, err := s.saveAudioFromSource(item.AudioFront)
		if err != nil {
			s.removeMedia(imgFront, imgBack)
			return nil, out, fmt.Errorf("card[%d]: saving front audio: %w", i, err)
		}
		audBack, err := s.saveAudioFromSource(item.AudioBack)
		if err != nil {
			s.removeMedia(imgFront, imgBack, audFront)
			return nil, out, fmt.Errorf("card[%d]: saving back audio: %w", i, err)
		}
		fact, err := s.cards.AddFact(ctx, s.userID, front, item.Back, in.DeckID, grade, item.Reverse, imgFront, imgBack, audFront, audBack)
		if err != nil {
			s.removeMedia(imgFront, imgBack, audFront, audBack)
			return nil, out, fmt.Errorf("card[%d]: creating card: %w", i, err)
		}
		added := 1
		if item.Reverse && strings.TrimSpace(item.Back) != "" {
			added = 2
		}
		out.Created++
		out.CardsAdded += added
		out.Facts = append(out.Facts, CreateCardOutput{
			FactID:     fact.ID,
			Front:      fact.Front,
			Back:       fact.Back,
			CardsAdded: added,
		})
	}
	return nil, out, nil
}

// SetDeckReverseInput is the argument schema for set_deck_reverse.
type SetDeckReverseInput struct {
	DeckID  string `json:"deckId" jsonschema:"Deck ID whose facts should be updated. Required — use list_decks to find it."`
	Enabled bool   `json:"enabled" jsonschema:"true adds a reverse card to every fact that lacks one; false removes it from every fact that has one."`
}

// SetDeckReverseOutput is the result schema for set_deck_reverse.
type SetDeckReverseOutput struct {
	DeckID  string `json:"deckId"`
	Enabled bool   `json:"enabled"`
	Changed int    `json:"changed"`
}

func (s *Server) setDeckReverse(ctx context.Context, _ *mcpsdk.CallToolRequest, in SetDeckReverseInput) (*mcpsdk.CallToolResult, SetDeckReverseOutput, error) {
	if strings.TrimSpace(in.DeckID) == "" {
		return nil, SetDeckReverseOutput{}, fmt.Errorf("deckId is required — use list_decks to find it")
	}
	changed, err := s.cards.SetDeckReverse(ctx, s.userID, in.DeckID, in.Enabled)
	if err != nil {
		return nil, SetDeckReverseOutput{}, fmt.Errorf("setting deck reverse: %w", err)
	}
	return nil, SetDeckReverseOutput{DeckID: in.DeckID, Enabled: in.Enabled, Changed: changed}, nil
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

// saveImageFromSource stores an image downloaded from an http(s) URL,
// returning the stored filename. Empty source returns "". Only remote URLs
// are accepted — this server is reachable over the network, so a "local
// file path" would mean the server's own filesystem, not the MCP caller's,
// which would turn this into an arbitrary-file-read primitive.
func (s *Server) saveImageFromSource(src string) (string, error) {
	if src != "" && !isURL(src) {
		return "", fmt.Errorf("imageFront/imageBack must be an http(s) URL")
	}
	return s.saveImageFromURL(src)
}

// saveAudioFromSource stores an audio file downloaded from an http(s) URL,
// returning the stored filename. Empty source returns "". See
// saveImageFromSource for why local file paths are not accepted.
func (s *Server) saveAudioFromSource(src string) (string, error) {
	if src != "" && !isURL(src) {
		return "", fmt.Errorf("audioFront/audioBack must be an http(s) URL")
	}
	return s.saveAudioFromURL(src)
}

// isURL reports whether the media source is an http(s) URL.
func isURL(src string) bool {
	return strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")
}

// saveAudioFromURL downloads a URL and saves it as a local audio file.
// Returns the filename, or empty string if url is empty.
func (s *Server) saveAudioFromURL(url string) (string, error) {
	if url == "" {
		return "", nil
	}
	resp, err := mediaHTTPClient.Get(url)
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
	resp, err := mediaHTTPClient.Get(url)
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
