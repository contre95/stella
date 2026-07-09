package portability

import "github.com/gofiber/fiber/v2"

// RegisterRoutes mounts the portability endpoints.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	app.Post("/import", h.Import)
	app.Post("/decks/import", h.ImportDeck)
	app.Post("/groups/import", h.ImportGroup)
	app.Get("/decks/:id/export", h.ExportDeck)
	app.Get("/groups/:id/export", h.ExportGroup)
}
