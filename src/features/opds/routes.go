package opds

import "github.com/gofiber/fiber/v2"

// RegisterRoutes mounts the OPDS catalog endpoints.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	app.Get("/opds", h.Catalog)
	app.Get("/opds/groups/:id", h.Group)
}
