package ui

import "github.com/gofiber/fiber/v2"

// RegisterRoutes mounts the UI pages.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	app.Get("/", h.Dashboard)
	app.Get("/about", h.About)
}
