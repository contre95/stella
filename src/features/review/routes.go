package review

import "github.com/gofiber/fiber/v2"

// RegisterRoutes mounts all review endpoints.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	app.Get("/review", h.RenderSession)
	app.Get("/review/pick", h.PickGroup)
	app.Post("/review/:cardId/grade", h.GradeCard)
	app.Get("/review/done", h.Done)
}
