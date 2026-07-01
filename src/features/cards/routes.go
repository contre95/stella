package cards

import "github.com/gofiber/fiber/v2"

// RegisterRoutes mounts the cards endpoints. Specific literal paths are
// registered before the :id wildcard.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	app.Get("/cards", h.RenderSection)
	app.Get("/cards/list", h.List)
	app.Post("/cards", h.Add)
	app.Delete("/cards/:id", h.Delete)

	app.Get("/decks", h.RenderDecks)
	app.Get("/decks/list", h.DeckList)
	app.Get("/decks/list-options", h.DeckOptions)
	app.Post("/decks", h.AddDeck)
	app.Post("/decks/import", h.ImportDeck)
	app.Delete("/decks/:id", h.DeleteDeck)
	app.Get("/decks/:id/export", h.ExportDeck)
	app.Get("/decks/:id/facts", h.DeckDetail)
	app.Post("/decks/:id/cards", h.AddCardToDeck)
}
