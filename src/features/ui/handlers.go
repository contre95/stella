package ui

import (
	"log/slog"

	"github.com/contre95/stella/src/features/hosting/respond"
	"github.com/contre95/stella/src/features/review"
	"github.com/gofiber/fiber/v2"
)

// Handler renders the top-level pages (dashboard, about).
type Handler struct {
	reviewSvc *review.Service
}

// NewHandler constructs the UI handler.
func NewHandler(reviewSvc *review.Service) *Handler {
	return &Handler{reviewSvc: reviewSvc}
}

// Dashboard renders the overview page with live aggregate stats.
func (h *Handler) Dashboard(c *fiber.Ctx) error {
	stats, err := h.reviewSvc.Stats(c.Context())
	if err != nil {
		slog.Error("dashboard stats", "err", err)
		stats = &review.Stats{}
	}
	forecast, err := h.reviewSvc.Forecast(c.Context(), 7)
	if err != nil {
		slog.Error("dashboard forecast", "err", err)
		forecast = nil
	}
	return respond.Section(c, "dashboard", fiber.Map{
		"Title":      "Dashboard",
		"DueToday":   stats.DueToday,
		"TotalCards": stats.TotalCards,
		"NewCards":   stats.NewCards,
		"Forecast":   forecast,
	})
}

// SidebarStats returns the deck/card counts for the sidebar.
func (h *Handler) SidebarStats(c *fiber.Ctx) error {
	stats, err := h.reviewSvc.Stats(c.Context())
	if err != nil {
		return c.SendString("")
	}
	return c.Render("partials/sidebar_stats", fiber.Map{
		"DeckCount":  stats.DeckCount,
		"TotalCards": stats.TotalCards,
	})
}

// About renders a placeholder section used to exercise HTMX navigation.
func (h *Handler) About(c *fiber.Ctx) error {
	return respond.Section(c, "about", fiber.Map{"Title": "About"})
}
