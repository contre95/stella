package cards

import "github.com/gofiber/fiber/v2"

// RegisterRoutes mounts the cards endpoints. Specific literal paths are
// registered before the :id wildcard.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	app.Get("/cards", h.RenderSection)
	app.Get("/cards/list", h.List)
	app.Post("/cards", h.Add)
	app.Delete("/cards/:id", h.Delete)
	app.Put("/cards/:id", h.Edit)
	app.Get("/cards/:id/edit", h.EditForm)
	app.Post("/cards/:id/reset", h.Reset)
	app.Post("/cards/:id/move", h.Move)

	app.Get("/decks", h.RenderDecks)
	app.Get("/decks/list", h.DeckList)
	app.Get("/decks/list-options", h.DeckOptions)
	app.Post("/decks", h.AddDeck)
	app.Delete("/decks/:id", h.DeleteDeck)
	app.Put("/decks/:id/name", h.RenameDeck)
	app.Put("/decks/:id/description", h.UpdateDeckDescription)
	app.Post("/decks/:id/image", h.SetDeckImage)
	app.Post("/decks/:id/group", h.SetDeckGroup)
	app.Post("/decks/:id/tags", h.AddDeckTag)
	app.Delete("/decks/:id/tags/:tagId", h.RemoveDeckTag)
	app.Post("/decks/:id/reset", h.ResetDeck)
	app.Post("/decks/:id/toggle-study", h.ToggleDeckStudy)
	app.Get("/decks/:id/facts", h.DeckDetail)
	app.Post("/decks/:id/cards", h.AddCardToDeck)
	app.Get("/decks/:id", h.RenderDeckPage)

	app.Post("/groups", h.AddGroup)
	app.Put("/groups/:id", h.UpdateGroup)
	app.Delete("/groups/:id", h.DeleteGroup)
	app.Get("/groups/:id", h.RenderGroupPage)

	app.Delete("/tags/:id", h.DeleteTag)
}
