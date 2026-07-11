package devicesync

import "github.com/gofiber/fiber/v2"

// RegisterRoutes mounts the sync API. The session auth middleware skips
// /api/x4 (like /mcp); bearerAuth is the sole gate.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	g := app.Group("/api/x4", h.bearerAuth)
	g.Get("/hello", h.hello)
	g.Get("/decks", h.manifest)
	g.Get("/decks/:id/cards", h.deckCSV)
	// Batched exchange: firmware pushes small NDJSON batches, then downloads
	// the full state. The single POST /state remains for older firmware.
	g.Post("/decks/:id/state/push", h.statePush)
	g.Get("/decks/:id/state", h.stateGet)
	g.Post("/decks/:id/state", h.state)
	g.Post("/reviews", h.reviews)
}
