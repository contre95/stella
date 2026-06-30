package cards

import (
	"strconv"
	"strings"

	"github.com/contre95/stella/src/features/hosting/respond"
	"github.com/gofiber/fiber/v2"
)

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
	return respond.Section(c, "cards", fiber.Map{"Title": "Cards"})
}

// List returns the fact/card list partial.
func (h *Handler) List(c *fiber.Ctx) error {
	rows, err := h.service.ListFacts(c.Context())
	if err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return respond.Partial(c, "components/card_list", fiber.Map{"Facts": rows})
}

// Add creates a fact and returns the refreshed list.
func (h *Handler) Add(c *fiber.Ctx) error {
	front := strings.TrimSpace(c.FormValue("front"))
	if front == "" {
		return respond.ToastErr(c, fiber.StatusBadRequest, "Front is required")
	}
	back := strings.TrimSpace(c.FormValue("back"))
	reverse := c.FormValue("reverse") != ""
	grade := -1
	if g := c.FormValue("grade"); g != "" {
		if parsed, err := strconv.Atoi(g); err == nil {
			grade = parsed
		}
	}
	if _, err := h.service.AddFact(c.Context(), front, back, "", grade, reverse); err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return h.List(c)
}

// Delete removes a fact and returns the refreshed list.
func (h *Handler) Delete(c *fiber.Ctx) error {
	if err := h.service.DeleteFact(c.Context(), c.Params("id")); err != nil {
		return respond.ToastErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return h.List(c)
}
