package review

import (
	"strconv"
	"strings"

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

// RenderSession serves GET /review — shows the first card or the done state.
// Optional ?decks=id1,id2 restricts the session to those decks.
func (h *Handler) RenderSession(c *fiber.Ctx) error {
	deckFilter := c.Query("decks")
	deckIDs := parseDeckFilter(deckFilter)

	ci, err := h.service.NextCard(c.Context(), deckIDs)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	if ci == nil {
		return respond.Section(c, "review_done", fiber.Map{"Title": "Review"})
	}
	return respond.Section(c, "review", fiber.Map{
		"Title":         "Review",
		"Card":          ci.Card,
		"Fact":          ci.Fact,
		"Remaining":     ci.Remaining,
		"DeckName":      ci.DeckName,
		"DeckColor":     ci.DeckColor,
		"DeckFilter":    deckFilter,
		"AutoplayAudio": h.cfg.Get().Cards.AutoplayAudio,
	})
}

// GradeCard handles POST /review/:cardId/grade — applies the grade and returns
// the next card partial, or redirects the HTMX target to the done section when
// the queue is empty. The deck filter is forwarded via the ?decks query param.
func (h *Handler) GradeCard(c *fiber.Ctx) error {
	cardID := c.Params("cardId")
	deckFilter := c.Query("decks")
	deckIDs := parseDeckFilter(deckFilter)

	grade, err := strconv.Atoi(c.FormValue("grade"))
	if err != nil || grade < 0 || grade > 5 {
		return respond.ToastErr(c, fiber.StatusBadRequest, "grade must be 0–5")
	}

	if err := h.service.Grade(c.Context(), cardID, grade); err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}

	ci, err := h.service.NextCard(c.Context(), deckIDs)
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	if ci == nil {
		c.Set("HX-Retarget", "#contenido")
		c.Set("HX-Reswap", "outerHTML")
		c.Set("HX-Push-Url", "/review/done")
		return c.Render("sections/review_done", fiber.Map{"Title": "Review Complete"})
	}
	return c.Render("partials/review_card", fiber.Map{
		"Card":          ci.Card,
		"Fact":          ci.Fact,
		"Remaining":     ci.Remaining,
		"DeckName":      ci.DeckName,
		"DeckColor":     ci.DeckColor,
		"DeckFilter":    deckFilter,
		"AutoplayAudio": h.cfg.Get().Cards.AutoplayAudio,
	})
}

// Done serves GET /review/done — session complete page.
func (h *Handler) Done(c *fiber.Ctx) error {
	return respond.Section(c, "review_done", fiber.Map{"Title": "Review Complete"})
}
