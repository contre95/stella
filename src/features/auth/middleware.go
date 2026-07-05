package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Locals keys set by the middleware. With PassLocalsToViews enabled they are
// directly available to templates ({{.UserName}}, {{.MultiUser}}, ...).
const (
	localsUserID    = "UserID"
	localsUserName  = "UserName"
	localsUserEmail = "UserEmail"
	localsMultiUser = "MultiUser"
)

// UserID returns the authenticated user's ID for the request. The middleware
// guarantees it is set on every route registered behind it.
func UserID(c *fiber.Ctx) string {
	id, _ := c.Locals(localsUserID).(string)
	return id
}

// Middleware authenticates every request behind it. In single-user mode it
// pins the request to the local account; otherwise it resolves the session
// cookie and sends unauthenticated requests to the login flow.
func (s *Service) Middleware() fiber.Handler {
	if s.SingleUser() {
		return func(c *fiber.Ctx) error {
			c.Locals(localsUserID, LocalUserID)
			c.Locals(localsMultiUser, false)
			return c.Next()
		}
	}
	return func(c *fiber.Ctx) error {
		// /mcp uses bearer token auth (MCPBearerMiddleware); skip session check.
		if strings.HasPrefix(c.Path(), "/mcp") {
			return c.Next()
		}
		if token := c.Cookies(sessionCookieName); token != "" {
			user, err := s.store.UserBySession(c.Context(), hashToken(token), time.Now().Unix())
			switch {
			case err == nil:
				c.Locals(localsUserID, user.ID)
				c.Locals(localsUserName, user.DisplayName())
				c.Locals(localsUserEmail, user.Email)
				c.Locals(localsMultiUser, true)
				return c.Next()
			case !errors.Is(err, ErrNoSession):
				return err
			}
		}
		// Not signed in: HTMX gets a client-side redirect, plain browser
		// navigation a real one, and non-idempotent requests a 401.
		if c.Get("HX-Request") == "true" {
			c.Set("HX-Redirect", "/auth/login")
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		if c.Method() == fiber.MethodGet || c.Method() == fiber.MethodHead {
			return c.Redirect("/auth/login", fiber.StatusFound)
		}
		return fiber.ErrUnauthorized
	}
}
