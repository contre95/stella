package auth

import "github.com/gofiber/fiber/v2"

// RegisterRoutes mounts token management routes. These sit behind the auth
// middleware so only logged-in users can reach them.
func (h *TokenHandler) RegisterRoutes(app *fiber.App) {
	app.Get("/settings/tokens", h.listTokens)
	app.Post("/settings/tokens", h.createToken)
	app.Delete("/settings/tokens/:hash", h.revokeToken)
	app.Delete("/settings/oauth-clients/:clientID", h.revokeOAuthClient)
}
