package cards

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/contre95/stella/src/features/hosting/respond"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

var allowedImageExts = []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
var allowedAudioExts = []string{".mp3", ".ogg", ".wav", ".m4a", ".webm", ".aac", ".flac"}

// Handler is the transport layer for the cards feature.
type Handler struct {
	service *Service
}

// NewHandler constructs the cards handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RenderSection renders the cards page (the list itself loads over HTMX).
func (h *Handler) RenderSection(c *fiber.Ctx) error {
	decks, err := h.service.ListDecks(c.Context())
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return respond.Section(c, "cards", fiber.Map{"Title": "Cards", "Decks": decks})
}

// List returns the fact/card list partial.
func (h *Handler) List(c *fiber.Ctx) error {
	rows, err := h.service.ListFacts(c.Context())
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return respond.Partial(c, "components/card_list", fiber.Map{"Facts": rows})
}

// saveImage saves the uploaded image file from the given form field and returns
// its filename, or an empty string if no image was uploaded.
func (h *Handler) saveImage(c *fiber.Ctx, field string) (string, error) {
	file, err := c.FormFile(field)
	if err != nil || file == nil {
		return "", nil
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if !slices.Contains(allowedImageExts, ext) {
		return "", fiber.NewError(fiber.StatusBadRequest, "image must be jpg, png, gif, or webp")
	}
	if err := os.MkdirAll(h.service.ImagePath(), 0o755); err != nil {
		return "", err
	}
	filename := uuid.NewString() + ext
	dest := filepath.Join(h.service.ImagePath(), filename)
	if err := c.SaveFile(file, dest); err != nil {
		return "", err
	}
	return filename, nil
}

// removeMedia deletes named media files from the media directory, logging
// any errors. Empty filenames are silently skipped.
func (h *Handler) removeMedia(filenames ...string) {
	for _, f := range filenames {
		if f == "" {
			continue
		}
		if err := os.Remove(filepath.Join(h.service.ImagePath(), f)); err != nil {
			slog.Warn("failed to remove orphaned media", "file", f, "err", err)
		}
	}
}

// saveAudio saves the uploaded audio file from the given form field and returns
// its filename, or an empty string if no audio was uploaded.
func (h *Handler) saveAudio(c *fiber.Ctx, field string) (string, error) {
	file, err := c.FormFile(field)
	if err != nil || file == nil {
		return "", nil
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if !slices.Contains(allowedAudioExts, ext) {
		return "", fiber.NewError(fiber.StatusBadRequest, "audio must be mp3, ogg, wav, m4a, webm, aac, or flac")
	}
	if err := os.MkdirAll(h.service.ImagePath(), 0o755); err != nil {
		return "", err
	}
	filename := uuid.NewString() + ext
	dest := filepath.Join(h.service.ImagePath(), filename)
	if err := c.SaveFile(file, dest); err != nil {
		return "", err
	}
	return filename, nil
}

// Add creates a fact and returns the refreshed list.
func (h *Handler) Add(c *fiber.Ctx) error {
	front := strings.TrimSpace(c.FormValue("front"))
	if front == "" {
		return respond.ToastErr(c, fiber.StatusBadRequest, "Front is required")
	}
	back := strings.TrimSpace(c.FormValue("back"))
	deckID := strings.TrimSpace(c.FormValue("deckId"))
	if deckID == "" {
		return respond.ToastErr(c, fiber.StatusBadRequest, "Deck is required")
	}
	reverse := c.FormValue("reverse") != ""
	grade := -1
	if g := c.FormValue("grade"); g != "" {
		if parsed, err := strconv.Atoi(g); err == nil && parsed >= 0 && parsed <= 5 {
			grade = parsed
		}
	}
	imgFront, err := h.saveImage(c, "image_front")
	if err != nil {
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	imgBack, err := h.saveImage(c, "image_back")
	if err != nil {
		h.removeMedia(imgFront)
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	audFront, err := h.saveAudio(c, "audio_front")
	if err != nil {
		h.removeMedia(imgFront, imgBack)
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	audBack, err := h.saveAudio(c, "audio_back")
	if err != nil {
		h.removeMedia(imgFront, imgBack, audFront)
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	if _, err := h.service.AddFact(c.Context(), front, back, deckID, grade, reverse, imgFront, imgBack, audFront, audBack); err != nil {
		h.removeMedia(imgFront, imgBack, audFront, audBack)
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return h.List(c)
}

// DeckOptions returns the <option> list for deck selects.
func (h *Handler) DeckOptions(c *fiber.Ctx) error {
	decks, err := h.service.ListDecks(c.Context())
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return respond.Partial(c, "components/deck_options", fiber.Map{"Decks": decks})
}

// deckGrid returns the card grid partial for a single deck.
func (h *Handler) deckGrid(c *fiber.Ctx, deckID string) error {
	facts, err := h.service.ListFactsByDeck(c.Context(), deckID)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	deckImage := ""
	if deck, err := h.service.GetDeck(c.Context(), deckID); err == nil {
		deckImage = deck.Image
	}
	return respond.Partial(c, "components/deck_card_grid", fiber.Map{"DeckID": deckID, "Facts": facts, "DeckImage": deckImage})
}

// Delete removes a fact and returns the refreshed list. When called from a
// deck page (?deck=<id>), it returns that deck's card grid instead.
func (h *Handler) Delete(c *fiber.Ctx) error {
	if err := h.service.DeleteFact(c.Context(), c.Params("id")); err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	if deckID := c.Query("deck"); deckID != "" {
		return h.deckGrid(c, deckID)
	}
	return h.List(c)
}

// Reset resets all cards for a fact back to unseen and returns the refreshed
// list. When called from a deck page (?deck=<id>), it returns that deck's
// card grid instead.
func (h *Handler) Reset(c *fiber.Ctx) error {
	if err := h.service.ResetFact(c.Context(), c.Params("id")); err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	if deckID := c.Query("deck"); deckID != "" {
		return h.deckGrid(c, deckID)
	}
	return h.List(c)
}

// RenderDecks renders the decks section page.
func (h *Handler) RenderDecks(c *fiber.Ctx) error {
	return respond.Section(c, "decks", fiber.Map{"Title": "Decks"})
}

// DeckList returns the deck list partial.
func (h *Handler) DeckList(c *fiber.Ctx) error {
	decks, err := h.service.ListDecks(c.Context())
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return respond.Partial(c, "components/deck_list", fiber.Map{"Decks": decks})
}

// DeleteDeck removes a deck and all its facts/cards, then returns the refreshed list.
func (h *Handler) DeleteDeck(c *fiber.Ctx) error {
	if err := h.service.DeleteDeck(c.Context(), c.Params("id")); err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return h.DeckList(c)
}

// AddDeck creates a deck and returns the refreshed list.
func (h *Handler) AddDeck(c *fiber.Ctx) error {
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		return respond.ToastErr(c, fiber.StatusBadRequest, "Name is required")
	}
	if len([]rune(name)) > 12 {
		name = string([]rune(name)[:12])
	}
	desc := strings.TrimSpace(c.FormValue("description"))
	image, err := h.saveImage(c, "image")
	if err != nil {
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	if _, err := h.service.AddDeck(c.Context(), name, desc, image); err != nil {
		h.removeMedia(image)
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return h.DeckList(c)
}

// SetDeckImage uploads a deck cover picture, replaces any previous one, and
// re-renders the deck page.
func (h *Handler) SetDeckImage(c *fiber.Ctx) error {
	image, err := h.saveImage(c, "image")
	if err != nil {
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	if image == "" {
		return respond.ToastErr(c, fiber.StatusBadRequest, "Image is required")
	}
	old, err := h.service.SetDeckImage(c.Context(), c.Params("id"), image)
	if err != nil {
		h.removeMedia(image)
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	h.removeMedia(old)
	return h.RenderDeckPage(c)
}

// DeckDetail renders the facts belonging to a deck.
func (h *Handler) DeckDetail(c *fiber.Ctx) error {
	id := c.Params("id")
	facts, err := h.service.ListFactsByDeck(c.Context(), id)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	deckImage := ""
	if deck, err := h.service.GetDeck(c.Context(), id); err == nil {
		deckImage = deck.Image
	}
	return respond.Partial(c, "components/deck_facts", fiber.Map{"DeckID": id, "Facts": facts, "DeckImage": deckImage})
}

// RenderDeckPage renders the full deck detail page.
func (h *Handler) RenderDeckPage(c *fiber.Ctx) error {
	id := c.Params("id")
	deck, err := h.service.GetDeck(c.Context(), id)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusNotFound, "Deck not found")
	}
	facts, err := h.service.ListFactsByDeck(c.Context(), id)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return respond.Section(c, "deck_page", fiber.Map{
		"Title":     deck.Name,
		"Deck":      deck,
		"DeckID":    deck.ID,
		"DeckImage": deck.Image,
		"Facts":     facts,
	})
}

// ExportDeck returns a JSON file download for the given deck.
func (h *Handler) ExportDeck(c *fiber.Ctx) error {
	exp, err := h.service.ExportDeck(c.Context(), c.Params("id"))
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	data, err := json.MarshalIndent(exp, "", "  ")
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	filename := fmt.Sprintf("%s.json", strings.ReplaceAll(exp.Name, " ", "_"))
	c.Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Set("Content-Type", "application/json")
	return c.Send(data)
}

// ImportDeck reads a JSON file upload and creates a new deck from it.
func (h *Handler) ImportDeck(c *fiber.Ctx) error {
	file, err := c.FormFile("file")
	if err != nil || file == nil {
		return respond.ToastErr(c, fiber.StatusBadRequest, "JSON file is required")
	}
	f, err := file.Open()
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	defer f.Close()

	var exp DeckExport
	if err := json.NewDecoder(f).Decode(&exp); err != nil {
		return respond.ToastErr(c, fiber.StatusBadRequest, "invalid JSON: "+err.Error())
	}
	if exp.Name == "" {
		return respond.ToastErr(c, fiber.StatusBadRequest, "deck name is required in JSON")
	}

	nameOverride := strings.TrimSpace(c.FormValue("name"))
	_, imported, err := h.service.ImportDeck(c.Context(), &exp, nameOverride)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	_ = imported
	return h.DeckList(c)
}

// AddCardToDeck creates a card assigned to a specific deck and returns the
// refreshed deck card grid partial.
func (h *Handler) AddCardToDeck(c *fiber.Ctx) error {
	id := c.Params("id")
	front := strings.TrimSpace(c.FormValue("front"))
	if front == "" {
		return respond.ToastErr(c, fiber.StatusBadRequest, "Front is required")
	}
	back := strings.TrimSpace(c.FormValue("back"))
	reverse := c.FormValue("reverse") != ""
	grade := -1
	if g := c.FormValue("grade"); g != "" {
		if parsed, err := strconv.Atoi(g); err == nil && parsed >= 0 && parsed <= 5 {
			grade = parsed
		}
	}
	imgFront, err := h.saveImage(c, "image_front")
	if err != nil {
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	imgBack, err := h.saveImage(c, "image_back")
	if err != nil {
		h.removeMedia(imgFront)
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	audFront, err := h.saveAudio(c, "audio_front")
	if err != nil {
		h.removeMedia(imgFront, imgBack)
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	audBack, err := h.saveAudio(c, "audio_back")
	if err != nil {
		h.removeMedia(imgFront, imgBack, audFront)
		return respond.ToastErr(c, fiber.StatusBadRequest, err.Error())
	}
	if _, err := h.service.AddFact(c.Context(), front, back, id, grade, reverse, imgFront, imgBack, audFront, audBack); err != nil {
		h.removeMedia(imgFront, imgBack, audFront, audBack)
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return h.deckGrid(c, id)
}
