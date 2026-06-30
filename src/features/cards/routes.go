package cards

import "github.com/gofiber/fiber/v2"

// RegisterRoutes mounts the cards endpoints. Specific literal paths are
// registered before the :id wildcard.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	app.Get("/cards", h.RenderSection)
	app.Get("/cards/list", h.List)
	app.Post("/cards", h.Add)
	app.Delete("/cards/:id", h.Delete)
}
