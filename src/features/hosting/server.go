package hosting

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/features/config"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html/v2"
)

// RouteRegistrar is implemented by each feature handler to mount its routes.
type RouteRegistrar interface {
	RegisterRoutes(app *fiber.App)
}

// Server wraps the Fiber app and its lifecycle.
type Server struct {
	app *fiber.App
	cfg *config.Manager
}

// NewServer builds the Fiber app, wires middleware, static files, the health
// check, and every feature's routes. The app-shell assets, the health check,
// and the login endpoints stay public; user media and every feature route sit
// behind the auth middleware.
func NewServer(cm *config.Manager, authSvc *auth.Service, registrars ...RouteRegistrar) *Server {
	engine := html.New("./views", ".html")
	engine.AddFunc("add", func(a, b int) int { return a + b })
	engine.AddFunc("dict", dict)
	engine.AddFunc("md", MarkdownHTML)

	app := fiber.New(fiber.Config{
		Views:             engine,
		AppName:           cm.Get().Server.AppName,
		BodyLimit:         cm.Get().Server.BodyLimit,
		PassLocalsToViews: true,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fe *fiber.Error
			if errors.As(err, &fe) {
				code = fe.Code
			}
			slog.Error("request error", "err", err, "path", c.Path())
			// fiber.Error messages (fiber.NewError(...)) are intentionally
			// user-facing; anything else is an internal error (wrapped Go
			// errors, DB failures, ...) and must not be echoed to the client.
			msg := err.Error()
			if code >= fiber.StatusInternalServerError {
				msg = "Something went wrong. Please try again."
			}
			return c.Status(code).SendString(msg)
		},
	})

	app.Use(recover.New(), HTMXMiddleware(), LogRequestsMiddleware(), func(c *fiber.Ctx) error {
		version := os.Getenv("IMAGE_TAG")
		if version == "" {
			version = "dev"
		}
		c.Locals("Version", version)
		return c.Next()
	})
	app.Static("/", "./public")
	app.Get("/health", func(c *fiber.Ctx) error { return c.SendString("OK") })

	authSvc.RegisterRoutes(app)
	app.Use(authSvc.Middleware())
	app.Static("/images", cm.Get().ImagePath)
	app.Static("/audio", cm.Get().ImagePath)

	for _, r := range registrars {
		r.RegisterRoutes(app)
	}

	return &Server{app: app, cfg: cm}
}

// Start begins listening; blocks until shutdown.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.Get().Server.Port)
	return s.app.Listen(addr)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}

// dict builds a map from alternating key/value template args, enabling
// `{{template "components/x" (dict "Foo" .Foo "Bar" 1)}}`.
func dict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict requires an even number of arguments")
	}
	m := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		m[key] = values[i+1]
	}
	return m, nil
}
