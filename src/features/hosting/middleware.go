package hosting

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
)

// HTMXMiddleware exposes the HTMX flag to views via Locals.
func HTMXMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Locals("IsHTMX", c.Get("HX-Request") == "true")
		return c.Next()
	}
}

// LogRequestsMiddleware logs each request at debug level.
func LogRequestsMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		slog.Debug("request",
			"method", c.Method(),
			"path", c.Path(),
			"status", c.Response().StatusCode(),
			"dur", time.Since(start).String(),
		)
		return err
	}
}
