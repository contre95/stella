package ui

import (
	"github.com/contre95/stella/src/features/hosting/respond"
	"github.com/gofiber/fiber/v2"
)

// Handler renders the top-level pages (dashboard, about).
type Handler struct{}

// NewHandler constructs the UI handler.
func NewHandler() *Handler { return &Handler{} }

// Dashboard renders the overview page. Counts are placeholders until the
// review feature and database land.
func (h *Handler) Dashboard(c *fiber.Ctx) error {
	return respond.Section(c, "dashboard", fiber.Map{
		"Title":      "Dashboard",
		"DueToday":   0,
		"TotalCards": 0,
		"NewCards":   0,
	})
}

// About renders a placeholder section used to exercise HTMX navigation.
func (h *Handler) About(c *fiber.Ctx) error {
	return respond.Section(c, "about", fiber.Map{"Title": "About"})
}
