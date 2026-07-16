package review

import (
	"strconv"
	"strings"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/features/config"
	"github.com/contre95/stella/src/features/hosting/respond"
	"github.com/gofiber/fiber/v2"
)

// Handler is the HTTP transport for the review feature.
type Handler struct {
	service *Service
	cfg     *config.Manager
}

// NewHandler constructs the review handler.
func NewHandler(service *Service, cfg *config.Manager) *Handler {
	return &Handler{service: service, cfg: cfg}
}

// parseDeckFilter splits the "decks" query param into a slice of IDs.
// An empty or absent param returns nil (= all decks).
func parseDeckFilter(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveDeckIDs returns the deck IDs for a study session. When raw is empty
// (no explicit filter), it returns only the study-enabled decks. When raw is
// non-empty it parses and uses those IDs directly.
func (h *Handler) resolveDeckIDs(c *fiber.Ctx, raw string) ([]string, error) {
	if raw != "" {
		return parseDeckFilter(raw), nil
	}
	return h.service.EnabledDeckIDs(c.Context(), auth.UserID(c))
}

// doneData builds the template data for the session-complete page, offering a
// learn-ahead session when future-scheduled cards exist.
func (h *Handler) doneData(c *fiber.Ctx, title, deckFilter string, deckIDs []string) fiber.Map {
	canLearnAhead, err := h.service.HasLearnAhead(c.Context(), auth.UserID(c), deckIDs)
	if err != nil {
		canLearnAhead = false
	}
	return fiber.Map{"Title": title, "CanLearnAhead": canLearnAhead, "DeckFilter": deckFilter}
}

// Pick serves GET /review/pick — group/tag selection before a study session.
// When neither groups nor tags exist it falls through directly to RenderSession.
func (h *Handler) Pick(c *fiber.Ctx) error {
	named, ungrouped, err := h.service.GroupsForPicker(c.Context(), auth.UserID(c))
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	tags, err := h.service.TagsForPicker(c.Context(), auth.UserID(c))
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	if len(named) == 0 && ungrouped == nil && len(tags) == 0 {
		return h.RenderSession(c)
	}
	return respond.Section(c, "study_pick", fiber.Map{
		"Title":     "Study",
		"Groups":    named,
		"Ungrouped": ungrouped,
		"Tags":      tags,
	})
}

// RenderSession serves GET /review — shows the first card or the done state.
// Optional ?decks=id1,id2 restricts the session to those decks, and
// ?learnahead=1 keeps serving future-scheduled cards once the queue is empty.
// When no ?decks= filter is given, only study-enabled decks are included.
func (h *Handler) RenderSession(c *fiber.Ctx) error {
	deckFilter := c.Query("decks")
	deckIDs, err := h.resolveDeckIDs(c, deckFilter)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	learnAhead := c.Query("learnahead") == "1"

	ci, err := h.service.NextCard(c.Context(), auth.UserID(c), deckIDs, learnAhead)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	if ci == nil {
		return respond.Section(c, "review_done", h.doneData(c, "Review", deckFilter, deckIDs))
	}
	return respond.Section(c, "review", fiber.Map{
		"Title":         "Review",
		"Card":          ci.Card,
		"Fact":          ci.Fact,
		"Remaining":     ci.Remaining,
		"DeckName":      ci.DeckName,
		"DeckColor":     ci.DeckColor,
		"DeckImage":     ci.DeckImage,
		"DueLabel":      ci.DueLabel,
		"DueTone":       ci.DueTone,
		"DeckFilter":    deckFilter,
		"LearnAhead":    learnAhead,
		"Previews":      ci.Previews,
		"AutoplayAudio": h.cfg.Get().Cards.AutoplayAudio,
	})
}

// GradeCard handles POST /review/:cardId/grade — applies the grade and returns
// the next card partial, or redirects the HTMX target to the done section when
// the queue is empty. The deck filter and learn-ahead flag are forwarded via
// the ?decks and ?learnahead query params.
func (h *Handler) GradeCard(c *fiber.Ctx) error {
	cardID := c.Params("cardId")
	deckFilter := c.Query("decks")
	deckIDs, err := h.resolveDeckIDs(c, deckFilter)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	learnAhead := c.Query("learnahead") == "1"

	grade, err := strconv.Atoi(c.FormValue("grade"))
	if err != nil || grade < 0 || grade > 5 {
		return respond.ToastErr(c, fiber.StatusBadRequest, "grade must be 0–5")
	}

	if err := h.service.Grade(c.Context(), auth.UserID(c), cardID, grade); err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}

	ci, err := h.service.NextCard(c.Context(), auth.UserID(c), deckIDs, learnAhead)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	if ci == nil {
		c.Set("HX-Retarget", "#contenido")
		c.Set("HX-Reswap", "outerHTML")
		c.Set("HX-Push-Url", "/review/done")
		return c.Render("sections/review_done", h.doneData(c, "Review Complete", deckFilter, deckIDs))
	}
	return c.Render("partials/review_card", fiber.Map{
		"Card":          ci.Card,
		"Fact":          ci.Fact,
		"Remaining":     ci.Remaining,
		"DeckName":      ci.DeckName,
		"DeckColor":     ci.DeckColor,
		"DeckImage":     ci.DeckImage,
		"DueLabel":      ci.DueLabel,
		"DueTone":       ci.DueTone,
		"DeckFilter":    deckFilter,
		"LearnAhead":    learnAhead,
		"Previews":      ci.Previews,
		"AutoplayAudio": h.cfg.Get().Cards.AutoplayAudio,
	})
}

// Done serves GET /review/done — session complete page.
func (h *Handler) Done(c *fiber.Ctx) error {
	deckFilter := c.Query("decks")
	deckIDs, err := h.resolveDeckIDs(c, deckFilter)
	if err != nil {
		deckIDs = nil
	}
	return respond.Section(c, "review_done", h.doneData(c, "Review Complete", deckFilter, deckIDs))
}
