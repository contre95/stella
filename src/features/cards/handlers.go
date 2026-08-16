package cards

import (
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/features/hosting/respond"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// AllowedImageExts and AllowedAudioExts are the accepted media file extensions,
// shared with the MCP transport for validating local file sources.
var AllowedImageExts = []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
var AllowedAudioExts = []string{".mp3", ".ogg", ".wav", ".m4a", ".webm", ".aac", ".flac"}

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
	data, err := h.addFormData(c, addFactRequest{}, nil)
	if err != nil {
		return err
	}
	data["Title"] = "Cards"
	return respond.Section(c, "cards", data)
}

// cardsPageSize is how many facts the /cards list shows per page.
const cardsPageSize = 25

// listData builds the template data for the card list partial, shared by the
// direct list endpoint and by any handler that needs to refresh the list
// out-of-band (e.g. after adding a card).
func (h *Handler) listData(c *fiber.Ctx) (fiber.Map, error) {
	userID := auth.UserID(c)
	q := strings.TrimSpace(c.Query("q"))
	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * cardsPageSize

	rows, err := h.service.ListFacts(c.Context(), userID, q, cardsPageSize, offset)
	if err != nil {
		return nil, err
	}
	total, err := h.service.CountFacts(c.Context(), userID, q)
	if err != nil {
		return nil, err
	}
	totalPages := (total + cardsPageSize - 1) / cardsPageSize
	return fiber.Map{
		"Facts":      rows,
		"Query":      q,
		"Page":       page,
		"TotalPages": totalPages,
	}, nil
}

// List returns the fact/card list partial, optionally filtered by ?q= and
// paged by ?page= (1-based, defaults to 1).
func (h *Handler) List(c *fiber.Ctx) error {
	data, err := h.listData(c)
	if err != nil {
		return err
	}
	return respond.Partial(c, "components/card_list", data)
}

// saveImage saves the uploaded image file from the given form field and returns
// its filename, or an empty string if no image was uploaded.
func (h *Handler) saveImage(c *fiber.Ctx, field string) (string, error) {
	file, err := c.FormFile(field)
	if err != nil || file == nil {
		return "", nil
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if !slices.Contains(AllowedImageExts, ext) {
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
	if !slices.Contains(AllowedAudioExts, ext) {
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

// addFormData builds the template data for the add-card form partial,
// shared by the initial page render and both the success/failure re-renders.
func (h *Handler) addFormData(c *fiber.Ctx, values addFactRequest, errs respond.FieldErrors) (fiber.Map, error) {
	decks, err := h.service.ListDecks(c.Context(), auth.UserID(c))
	if err != nil {
		return nil, err
	}
	return fiber.Map{"Decks": decks, "Values": values, "Errors": errs}, nil
}

// Add creates a fact. On success it re-renders the (reset, deck-kept) add
// form plus an OOB refresh of the card list; on validation failure it
// re-renders the same form with the submitted values and inline field
// errors (HTTP 422) instead of touching the list.
func (h *Handler) Add(c *fiber.Ctx) error {
	var req addFactRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not read form")
	}
	req.Front = strings.TrimSpace(req.Front)
	req.Back = strings.TrimSpace(req.Back)
	req.DeckID = strings.TrimSpace(req.DeckID)
	errs := respond.Validate(req)

	grade := -1
	if parsed, err := strconv.Atoi(req.Grade); err == nil && parsed >= 0 && parsed <= 5 {
		grade = parsed
	}

	imgFront, e1 := h.saveImage(c, "image_front")
	imgBack, e2 := h.saveImage(c, "image_back")
	audFront, e3 := h.saveAudio(c, "audio_front")
	audBack, e4 := h.saveAudio(c, "audio_back")
	var err error
	if errs, err = mergeFileErr(errs, "image_front", e1); err != nil {
		h.removeMedia(imgFront, imgBack, audFront, audBack)
		return err
	}
	if errs, err = mergeFileErr(errs, "image_back", e2); err != nil {
		h.removeMedia(imgFront, imgBack, audFront, audBack)
		return err
	}
	if errs, err = mergeFileErr(errs, "audio_front", e3); err != nil {
		h.removeMedia(imgFront, imgBack, audFront, audBack)
		return err
	}
	if errs, err = mergeFileErr(errs, "audio_back", e4); err != nil {
		h.removeMedia(imgFront, imgBack, audFront, audBack)
		return err
	}

	if len(errs) > 0 {
		h.removeMedia(imgFront, imgBack, audFront, audBack)
		data, err := h.addFormData(c, req, errs)
		if err != nil {
			return err
		}
		return respond.FormInvalid(c, "components/card_add_form", data, errs)
	}

	if _, err := h.service.AddFact(c.Context(), auth.UserID(c), req.Front, req.Back, req.DeckID, grade, req.Reverse, imgFront, imgBack, audFront, audBack); err != nil {
		h.removeMedia(imgFront, imgBack, audFront, audBack)
		return err
	}

	data, err := h.addFormData(c, addFactRequest{DeckID: req.DeckID}, nil)
	if err != nil {
		return err
	}
	list, err := h.listData(c)
	if err != nil {
		return err
	}
	maps.Copy(data, list)
	data["ListOOB"] = true
	return c.Render("components/card_add_form", data)
}

// DeckOptions returns the <option> list for deck selects.
func (h *Handler) DeckOptions(c *fiber.Ctx) error {
	decks, err := h.service.ListDecks(c.Context(), auth.UserID(c))
	if err != nil {
		return err
	}
	return respond.Partial(c, "components/deck_options", fiber.Map{"Decks": decks})
}

// deckGrid returns the card grid partial for a single deck.
func (h *Handler) deckGrid(c *fiber.Ctx, deckID string) error {
	facts, err := h.service.ListFactsByDeck(c.Context(), auth.UserID(c), deckID)
	if err != nil {
		return err
	}
	deckImage := ""
	if deck, err := h.service.GetDeck(c.Context(), auth.UserID(c), deckID); err == nil {
		deckImage = deck.Image
	}
	return respond.Partial(c, "components/deck_card_grid", fiber.Map{"DeckID": deckID, "Facts": facts, "DeckImage": deckImage})
}

// Delete removes a fact and returns the refreshed list. When called from a
// deck page (?deck=<id>), it returns that deck's card grid instead.
func (h *Handler) Delete(c *fiber.Ctx) error {
	if err := h.service.DeleteFact(c.Context(), auth.UserID(c), c.Params("id")); err != nil {
		return err
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
	if err := h.service.ResetFact(c.Context(), auth.UserID(c), c.Params("id")); err != nil {
		return err
	}
	if deckID := c.Query("deck"); deckID != "" {
		return h.deckGrid(c, deckID)
	}
	return h.List(c)
}

// editFormData builds the template data shared by the edit-card modal's
// initial render (EditForm) and the failure re-render of just its form
// (Edit, on validation errors). overrideValues, when non-nil, wins over the
// fact's own content — that's the user's just-submitted (and rejected) input.
func (h *Handler) editFormData(c *fiber.Ctx, overrideValues *editFactRequest, errs respond.FieldErrors) (fiber.Map, error) {
	fact, err := h.service.GetFact(c.Context(), auth.UserID(c), c.Params("id"))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusNotFound, "Card not found")
	}
	values := editFactRequest{Front: fact.Front, Back: fact.Back}
	if overrideValues != nil {
		values = *overrideValues
	} else {
		hasReverse, err := h.service.HasReverse(c.Context(), auth.UserID(c), fact.ID)
		if err != nil {
			return nil, err
		}
		values.Reverse = hasReverse
	}
	decks, err := h.service.ListDecks(c.Context(), auth.UserID(c))
	if err != nil {
		return nil, err
	}
	return fiber.Map{
		"Fact":             fact,
		"Decks":            decks,
		"Values":           values,
		"Errors":           errs,
		"DeckID":           c.Query("deck"),
		"ReviewCardID":     c.Query("cardId"),
		"ReviewDecks":      c.Query("decks"),
		"ReviewLearnAhead": c.Query("learnahead") == "1",
	}, nil
}

// EditForm returns the edit-card modal for a fact. A ?deck=<id> query keeps
// the eventual save targeting that deck's card grid instead of the full list.
// When opened from the review screen, ?cardId=&decks=&learnahead= carry the
// session context so the modal can refresh that same card after saving
// instead of targeting the (absent, on that screen) card list/grid.
func (h *Handler) EditForm(c *fiber.Ctx) error {
	data, err := h.editFormData(c, nil, nil)
	if err != nil {
		return err
	}
	return respond.Partial(c, "components/card_edit", data)
}

// Preview serves GET /cards/:id/preview — a read-only modal showing the fact
// rendered the way it looks during review (full-size, images, audio,
// markdown). It has no grade buttons and never touches scheduling; it's
// purely a "what does this card look like" view from the deck grid.
func (h *Handler) Preview(c *fiber.Ctx) error {
	fact, err := h.service.GetFact(c.Context(), auth.UserID(c), c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Card not found")
	}
	return respond.Partial(c, "components/card_preview", fiber.Map{
		"Fact": fact,
	})
}

// Move reassigns a fact to a different deck without touching its cards'
// scheduling state, and returns the refreshed list. When called from a deck
// page (?deck=<id>), it returns that deck's card grid instead — the fact
// drops out of it once moved elsewhere.
func (h *Handler) Move(c *fiber.Ctx) error {
	deckID := strings.TrimSpace(c.FormValue("deckId"))
	if deckID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Deck is required")
	}
	if _, err := h.service.MoveFact(c.Context(), auth.UserID(c), c.Params("id"), deckID); err != nil {
		return err
	}
	if src := c.Query("deck"); src != "" {
		return h.deckGrid(c, src)
	}
	return h.List(c)
}

// Edit updates a fact's content and returns the refreshed list. When called
// from a deck page (?deck=<id>) or the review screen (?cardId=...), a
// validation failure falls back to a toast (those contexts' sibling-refresh
// routing isn't wired for inline errors yet — see phase C). Otherwise
// (opened from the plain card list) a validation failure re-renders the
// modal's form in place with inline field errors (HTTP 422), values
// preserved, alongside the untouched card list. Each media slot is replaced
// by an uploaded file, cleared by its remove_<field> checkbox, or otherwise
// left untouched.
func (h *Handler) Edit(c *fiber.Ctx) error {
	var req editFactRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not read form")
	}
	req.Front = strings.TrimSpace(req.Front)
	req.Back = strings.TrimSpace(req.Back)
	errs := respond.Validate(req)

	var saved []string
	slot := func(field string, save func(*fiber.Ctx, string) (string, error)) (*string, error) {
		name, err := save(c, field)
		if err != nil {
			return nil, err
		}
		if name != "" {
			saved = append(saved, name)
			return &name, nil
		}
		if c.FormValue("remove_"+field) != "" {
			empty := ""
			return &empty, nil
		}
		return nil, nil
	}
	imgFront, e1 := slot("image_front", h.saveImage)
	imgBack, e2 := slot("image_back", h.saveImage)
	audFront, e3 := slot("audio_front", h.saveAudio)
	audBack, e4 := slot("audio_back", h.saveAudio)
	var err error
	if errs, err = mergeFileErr(errs, "image_front", e1); err != nil {
		h.removeMedia(saved...)
		return err
	}
	if errs, err = mergeFileErr(errs, "image_back", e2); err != nil {
		h.removeMedia(saved...)
		return err
	}
	if errs, err = mergeFileErr(errs, "audio_front", e3); err != nil {
		h.removeMedia(saved...)
		return err
	}
	if errs, err = mergeFileErr(errs, "audio_back", e4); err != nil {
		h.removeMedia(saved...)
		return err
	}

	if len(errs) > 0 {
		h.removeMedia(saved...)
		if c.Query("deck") != "" || c.Query("cardId") != "" {
			return fiber.NewError(fiber.StatusBadRequest, firstFieldError(errs))
		}
		data, err := h.editFormData(c, &req, errs)
		if err != nil {
			return err
		}
		list, err := h.listData(c)
		if err != nil {
			return err
		}
		maps.Copy(data, list)
		data["EditFormOOB"] = true
		return respond.FormInvalid(c, "components/card_list", data, errs)
	}

	if _, err := h.service.UpdateFact(c.Context(), auth.UserID(c), c.Params("id"), req.Front, req.Back, imgFront, imgBack, audFront, audBack, &req.Reverse); err != nil {
		h.removeMedia(saved...)
		return err
	}
	if deckID := c.Query("deck"); deckID != "" {
		return h.deckGrid(c, deckID)
	}
	return h.List(c)
}

// RenderDecks renders the decks section page.
func (h *Handler) RenderDecks(c *fiber.Ctx) error {
	groups, err := h.service.ListGroups(c.Context(), auth.UserID(c))
	if err != nil {
		return err
	}
	return respond.Section(c, "decks", fiber.Map{
		"Title":       "Decks",
		"Groups":      groups,
		"BodyLimit":   h.service.BodyLimit(),
		"DeckValues":  addDeckRequest{},
		"DeckErrors":  respond.FieldErrors(nil),
		"GroupValues": nameRequest{},
		"GroupErrors": respond.FieldErrors(nil),
	})
}

// deckListData builds the template data for the deck list partial, shared by
// the direct list endpoint and by any handler that needs to refresh the list
// out-of-band (e.g. after adding a deck or group).
func (h *Handler) deckListData(c *fiber.Ctx) (fiber.Map, error) {
	tagID := c.Query("tag")
	views, err := h.service.ListGroupViews(c.Context(), auth.UserID(c), tagID)
	if err != nil {
		return nil, err
	}
	tags, err := h.service.ListTags(c.Context(), auth.UserID(c))
	if err != nil {
		return nil, err
	}
	hasGroups := false
	for _, v := range views {
		if v.Group != nil {
			hasGroups = true
			break
		}
	}
	return fiber.Map{
		"GroupViews": views,
		"HasGroups":  hasGroups,
		"Tags":       tags,
		"ActiveTag":  tagID,
	}, nil
}

// DeckList returns the deck list partial. An optional ?tag=<id> query narrows
// it to decks carrying that tag.
func (h *Handler) DeckList(c *fiber.Ctx) error {
	data, err := h.deckListData(c)
	if err != nil {
		return err
	}
	return respond.Partial(c, "components/deck_list", data)
}

// AddDeckTag labels a deck with the named tag (creating the tag when needed)
// and re-renders the deck page it was called from. A validation failure
// re-renders the same page with the add-tag field's inline error and
// submitted value (422) — see deckPageData.
func (h *Handler) AddDeckTag(c *fiber.Ctx) error {
	var req nameRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not read form")
	}
	req.Name = strings.TrimSpace(req.Name)
	if errs := respond.Validate(req); len(errs) > 0 {
		data, err := h.deckPageData(c, fiber.Map{"TagErrors": errs, "TagValue": req.Name})
		if err != nil {
			return err
		}
		return respond.FormInvalid(c, "sections/deck_page", data, errs)
	}
	if _, err := h.service.TagDeck(c.Context(), auth.UserID(c), c.Params("id"), req.Name); err != nil {
		return err
	}
	return h.RenderDeckPage(c)
}

// RemoveDeckTag removes a tag from a deck and re-renders the deck page.
func (h *Handler) RemoveDeckTag(c *fiber.Ctx) error {
	if err := h.service.UntagDeck(c.Context(), auth.UserID(c), c.Params("id"), c.Params("tagId")); err != nil {
		return err
	}
	return h.RenderDeckPage(c)
}

// DeleteTag removes a tag entirely (decks keep their other tags) and returns
// the refreshed deck list.
func (h *Handler) DeleteTag(c *fiber.Ctx) error {
	if err := h.service.DeleteTag(c.Context(), auth.UserID(c), c.Params("id")); err != nil {
		return err
	}
	return h.DeckList(c)
}

// AddGroup creates a new group. On success it re-renders the (reset) create
// form plus an OOB refresh of the deck list; on validation failure it
// re-renders the same form with inline field errors (422).
func (h *Handler) AddGroup(c *fiber.Ctx) error {
	var req nameRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not read form")
	}
	req.Name = strings.TrimSpace(req.Name)
	errs := respond.Validate(req)

	if len(errs) > 0 {
		return respond.FormInvalid(c, "components/group_add_form", fiber.Map{"Values": req}, errs)
	}

	if _, err := h.service.AddGroup(c.Context(), auth.UserID(c), req.Name); err != nil {
		return err
	}

	list, err := h.deckListData(c)
	if err != nil {
		return err
	}
	list["ListOOB"] = true
	list["Values"] = nameRequest{}
	list["Errors"] = respond.FieldErrors(nil)
	return c.Render("components/group_add_form", list)
}

// DeleteGroup removes a group (ungrouping its decks) and returns the refreshed list.
func (h *Handler) DeleteGroup(c *fiber.Ctx) error {
	if err := h.service.DeleteGroup(c.Context(), auth.UserID(c), c.Params("id")); err != nil {
		return err
	}
	return h.DeckList(c)
}

// UpdateGroup renames a group and returns the refreshed list, or the group
// page when called from there.
func (h *Handler) UpdateGroup(c *fiber.Ctx) error {
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Group name is required")
	}
	if _, err := h.service.UpdateGroup(c.Context(), auth.UserID(c), c.Params("id"), name); err != nil {
		return err
	}
	if c.Query("source") == "group_page" {
		return h.RenderGroupPage(c)
	}
	return h.DeckList(c)
}

// ToggleDeckStudy flips a deck's study-enabled flag and returns the refreshed
// deck list, or the deck/group page when called from there.
func (h *Handler) ToggleDeckStudy(c *fiber.Ctx) error {
	if _, err := h.service.ToggleDeckStudy(c.Context(), auth.UserID(c), c.Params("id")); err != nil {
		return err
	}
	if c.Query("source") == "deck_page" {
		return h.RenderDeckPage(c)
	}
	if c.Query("source") == "group_page" {
		return h.groupGrid(c, c.Query("group"))
	}
	return h.DeckList(c)
}

// SetDeckGroup assigns a deck to a group (or clears it) and returns the refreshed list.
func (h *Handler) SetDeckGroup(c *fiber.Ctx) error {
	groupID := strings.TrimSpace(c.FormValue("group_id"))
	if err := h.service.SetDeckGroup(c.Context(), auth.UserID(c), c.Params("id"), groupID); err != nil {
		return err
	}
	// If called from the deck page, re-render the deck page.
	if c.Query("source") == "deck_page" {
		return h.RenderDeckPage(c)
	}
	return h.DeckList(c)
}

// DeleteDeck removes a deck and all its facts/cards, then returns the refreshed
// deck list, or the group grid when called from the group page.
func (h *Handler) DeleteDeck(c *fiber.Ctx) error {
	if err := h.service.DeleteDeck(c.Context(), auth.UserID(c), c.Params("id")); err != nil {
		return err
	}
	if c.Query("source") == "group_page" {
		return h.groupGrid(c, c.Query("group"))
	}
	return h.DeckList(c)
}

// ResetDeck resets all cards in the deck back to unseen and re-renders the deck page.
func (h *Handler) ResetDeck(c *fiber.Ctx) error {
	if err := h.service.ResetDeck(c.Context(), auth.UserID(c), c.Params("id")); err != nil {
		return err
	}
	return h.RenderDeckPage(c)
}

// deckAddFormData builds the template data for the create-deck form partial,
// shared by the initial page render and both the success/failure re-renders.
func (h *Handler) deckAddFormData(c *fiber.Ctx, values addDeckRequest, errs respond.FieldErrors) (fiber.Map, error) {
	groups, err := h.service.ListGroups(c.Context(), auth.UserID(c))
	if err != nil {
		return nil, err
	}
	return fiber.Map{"Groups": groups, "Values": values, "Errors": errs}, nil
}

// AddDeck creates a deck. On success it re-renders the (reset) create form
// plus an OOB refresh of the deck list; on validation failure it re-renders
// the same form with the submitted values and inline field errors (422).
func (h *Handler) AddDeck(c *fiber.Ctx) error {
	var req addDeckRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not read form")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.GroupID = strings.TrimSpace(req.GroupID)
	errs := respond.Validate(req)

	image, e1 := h.saveImage(c, "image")
	var err error
	if errs, err = mergeFileErr(errs, "image", e1); err != nil {
		h.removeMedia(image)
		return err
	}

	if len(errs) > 0 {
		h.removeMedia(image)
		data, err := h.deckAddFormData(c, req, errs)
		if err != nil {
			return err
		}
		return respond.FormInvalid(c, "components/deck_add_form", data, errs)
	}

	if _, err := h.service.AddDeck(c.Context(), auth.UserID(c), req.Name, req.Description, image, req.GroupID); err != nil {
		h.removeMedia(image)
		return err
	}

	data, err := h.deckAddFormData(c, addDeckRequest{}, nil)
	if err != nil {
		return err
	}
	list, err := h.deckListData(c)
	if err != nil {
		return err
	}
	maps.Copy(data, list)
	data["ListOOB"] = true
	return c.Render("components/deck_add_form", data)
}

// UpdateDeckDescription updates the deck's description and re-renders the deck page.
func (h *Handler) UpdateDeckDescription(c *fiber.Ctx) error {
	desc := strings.TrimSpace(c.FormValue("description"))
	if _, err := h.service.UpdateDeckDescription(c.Context(), auth.UserID(c), c.Params("id"), desc); err != nil {
		return err
	}
	return h.RenderDeckPage(c)
}

// RenameDeck updates the deck's name and re-renders the deck page. A
// validation failure re-renders the same page with the rename field's
// inline error and submitted value (422) — see deckPageData.
func (h *Handler) RenameDeck(c *fiber.Ctx) error {
	var req deckNameRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not read form")
	}
	req.Name = strings.TrimSpace(req.Name)
	if errs := respond.Validate(req); len(errs) > 0 {
		data, err := h.deckPageData(c, fiber.Map{"RenameErrors": errs, "RenameValue": req.Name})
		if err != nil {
			return err
		}
		return respond.FormInvalid(c, "sections/deck_page", data, errs)
	}
	if _, err := h.service.RenameDeck(c.Context(), auth.UserID(c), c.Params("id"), req.Name); err != nil {
		return err
	}
	return h.RenderDeckPage(c)
}

// SetDeckImage uploads a deck cover picture, replaces any previous one, and
// re-renders the deck page. A validation failure (wrong file type, or no
// file at all) re-renders the same page with the cover-image field's inline
// error (422) — see deckPageData.
func (h *Handler) SetDeckImage(c *fiber.Ctx) error {
	image, e1 := h.saveImage(c, "image")
	errs, err := mergeFileErr(nil, "image", e1)
	if err != nil {
		return err
	}
	if image == "" && errs == nil {
		errs = respond.FieldErrors{"image": "Image is required"}
	}
	if len(errs) > 0 {
		h.removeMedia(image)
		data, err := h.deckPageData(c, fiber.Map{"ImageErrors": errs})
		if err != nil {
			return err
		}
		return respond.FormInvalid(c, "sections/deck_page", data, errs)
	}
	old, err := h.service.SetDeckImage(c.Context(), auth.UserID(c), c.Params("id"), image)
	if err != nil {
		h.removeMedia(image)
		return err
	}
	h.removeMedia(old)
	return h.RenderDeckPage(c)
}

// DeckDetail renders the facts belonging to a deck.
func (h *Handler) DeckDetail(c *fiber.Ctx) error {
	id := c.Params("id")
	facts, err := h.service.ListFactsByDeck(c.Context(), auth.UserID(c), id)
	if err != nil {
		return err
	}
	deckImage := ""
	if deck, err := h.service.GetDeck(c.Context(), auth.UserID(c), id); err == nil {
		deckImage = deck.Image
	}
	return respond.Partial(c, "components/deck_facts", fiber.Map{"DeckID": id, "Facts": facts, "DeckImage": deckImage})
}

// RenderDeckPage renders the full deck detail page.
// deckPageData builds the template data for the deck detail page, shared by
// the plain render (RenderDeckPage) and by the deck-page's own forms
// (rename, add tag, cover image) re-rendering the whole page in place on a
// validation failure — each of those inline forms targets #contenido
// already, so a failure just needs the same page re-rendered with that one
// form's field errors/submitted value overridden via override. Distinct
// *Errors/*Value key pairs per form avoid collisions between fields that
// happen to share an HTML name (the deck-name and add-tag inputs are both
// name="name").
func (h *Handler) deckPageData(c *fiber.Ctx, override fiber.Map) (fiber.Map, error) {
	id := c.Params("id")
	deck, err := h.service.GetDeck(c.Context(), auth.UserID(c), id)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusNotFound, "Deck not found")
	}
	facts, err := h.service.ListFactsByDeck(c.Context(), auth.UserID(c), id)
	if err != nil {
		return nil, err
	}
	groups, _ := h.service.ListGroups(c.Context(), auth.UserID(c))
	deckTags, err := h.service.ListDeckTags(c.Context(), auth.UserID(c), id)
	if err != nil {
		return nil, err
	}
	allTags, err := h.service.ListTags(c.Context(), auth.UserID(c))
	if err != nil {
		return nil, err
	}
	data := fiber.Map{
		"Title":        deck.Name,
		"Deck":         deck,
		"DeckID":       deck.ID,
		"DeckImage":    deck.Image,
		"Facts":        facts,
		"Groups":       groups,
		"DeckTags":     deckTags,
		"AllTags":      allTags,
		"RenameErrors": respond.FieldErrors(nil),
		"RenameValue":  deck.Name,
		"TagErrors":    respond.FieldErrors(nil),
		"TagValue":     "",
		"ImageErrors":  respond.FieldErrors(nil),
	}
	maps.Copy(data, override)
	return data, nil
}

// RenderDeckPage renders the full deck detail page.
func (h *Handler) RenderDeckPage(c *fiber.Ctx) error {
	data, err := h.deckPageData(c, nil)
	if err != nil {
		return err
	}
	return respond.Section(c, "deck_page", data)
}

// groupPageData builds the template data for the group detail page, shared
// by the plain render (RenderGroupPage) and the group-page's own rename form
// re-rendering the whole page in place on a validation failure.
func (h *Handler) groupPageData(c *fiber.Ctx, override fiber.Map) (fiber.Map, error) {
	id := c.Params("id")
	group, err := h.service.GetGroup(c.Context(), auth.UserID(c), id)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusNotFound, "Group not found")
	}
	decks, err := h.service.ListDecksByGroup(c.Context(), auth.UserID(c), id)
	if err != nil {
		return nil, err
	}
	data := fiber.Map{
		"Title":        group.Name,
		"Group":        group,
		"Decks":        decks,
		"RenameErrors": respond.FieldErrors(nil),
		"RenameValue":  group.Name,
	}
	maps.Copy(data, override)
	return data, nil
}

// RenderGroupPage renders the full page for a single group, showing its
// decks in a grid that adapts to screen width.
func (h *Handler) RenderGroupPage(c *fiber.Ctx) error {
	data, err := h.groupPageData(c, nil)
	if err != nil {
		return err
	}
	return respond.Section(c, "group_page", data)
}

// groupGrid returns the deck grid partial for a single group.
func (h *Handler) groupGrid(c *fiber.Ctx, groupID string) error {
	group, err := h.service.GetGroup(c.Context(), auth.UserID(c), groupID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Group not found")
	}
	decks, err := h.service.ListDecksByGroup(c.Context(), auth.UserID(c), groupID)
	if err != nil {
		return err
	}
	return respond.Partial(c, "components/group_deck_grid", fiber.Map{"Group": group, "Decks": decks})
}

// AddCardToDeck creates a card assigned to a specific deck and returns the
// refreshed deck card grid partial.
func (h *Handler) AddCardToDeck(c *fiber.Ctx) error {
	id := c.Params("id")
	front := strings.TrimSpace(c.FormValue("front"))
	if front == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Front is required")
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
		return err
	}
	imgBack, err := h.saveImage(c, "image_back")
	if err != nil {
		h.removeMedia(imgFront)
		return err
	}
	audFront, err := h.saveAudio(c, "audio_front")
	if err != nil {
		h.removeMedia(imgFront, imgBack)
		return err
	}
	audBack, err := h.saveAudio(c, "audio_back")
	if err != nil {
		h.removeMedia(imgFront, imgBack, audFront)
		return err
	}
	if _, err := h.service.AddFact(c.Context(), auth.UserID(c), front, back, id, grade, reverse, imgFront, imgBack, audFront, audBack); err != nil {
		h.removeMedia(imgFront, imgBack, audFront, audBack)
		return err
	}
	return h.deckGrid(c, id)
}
