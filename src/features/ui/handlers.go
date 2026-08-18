package ui

import (
	"log/slog"

	"github.com/contre95/stella/src/features/auth"
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

// Dashboard renders the overview page with live aggregate stats. An optional
// ?decks=id1,id2 query param (the comma-joined study-enabled deck IDs of one
// group, matching the /review deck-filter convention) scopes the whole page
// to that group; absent it, the page covers all groups.
func (h *Handler) Dashboard(c *fiber.Ctx) error {
	userID := auth.UserID(c)
	deckFilter := c.Query("decks")
	deckIDs := review.ParseDeckFilter(deckFilter)

	stats, err := h.reviewSvc.Stats(c.Context(), userID, deckIDs)
	if err != nil {
		slog.Error("dashboard stats", "err", err)
		stats = &review.Stats{}
	}
	forecast, err := h.reviewSvc.Forecast(c.Context(), userID, 7, deckIDs)
	if err != nil {
		slog.Error("dashboard forecast", "err", err)
		forecast = nil
	}
	heatmap, err := h.reviewSvc.StudyHeatmap(c.Context(), userID, deckIDs)
	if err != nil {
		slog.Error("dashboard heatmap", "err", err)
		heatmap = nil
	}
	// The per-group pie charts only make sense across all groups — scoped to
	// a single group they'd always be a single 100% slice.
	groupBreakdown := &review.GroupBreakdown{}
	if len(deckIDs) == 0 {
		groupBreakdown, err = h.reviewSvc.GroupBreakdown(c.Context(), userID)
		if err != nil {
			slog.Error("dashboard group breakdown", "err", err)
			groupBreakdown = &review.GroupBreakdown{}
		}
	}
	groups, _, err := h.reviewSvc.GroupsForPicker(c.Context(), userID)
	if err != nil {
		slog.Error("dashboard groups for picker", "err", err)
		groups = nil
	}
	studyGroups := make([]review.GroupForPicker, 0, len(groups))
	for _, g := range groups {
		if g.StudyCount > 0 {
			studyGroups = append(studyGroups, g)
		}
	}
	return respond.Section(c, "dashboard", fiber.Map{
		"DueToday":    stats.DueToday,
		"TotalCards":  stats.TotalCards,
		"StudyCards":  stats.StudyCards,
		"NewCards":    stats.NewCards,
		"UnseenCards": stats.UnseenCards,
		"Forecast":    forecast,
		"Heatmap":     heatmap,
		"GroupCharts": groupBreakdown,
		"StudyGroups": studyGroups,
		"DeckFilter":  deckFilter,
	})
}

// SidebarStats returns the deck/card counts for the sidebar.
func (h *Handler) SidebarStats(c *fiber.Ctx) error {
	stats, err := h.reviewSvc.Stats(c.Context(), auth.UserID(c), nil)
	if err != nil {
		slog.Error("sidebar stats", "err", err)
		return c.SendString("")
	}
	return c.Render("partials/sidebar_stats", fiber.Map{
		"StudyCards": stats.StudyCards,
	})
}

// About renders a placeholder section used to exercise HTMX navigation.
func (h *Handler) About(c *fiber.Ctx) error {
	return respond.Section(c, "about", fiber.Map{"Title": "About"})
}
