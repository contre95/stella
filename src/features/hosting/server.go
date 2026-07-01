package hosting

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/contre95/stella/src/features/config"
	"github.com/gofiber/fiber/v2"
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
// check, and every feature's routes.
func NewServer(cm *config.Manager, registrars ...RouteRegistrar) *Server {
	engine := html.New("./views", ".html")
	engine.Reload(cm.Get().Logger.Debug)
	engine.AddFunc("add", func(a, b int) int { return a + b })
	engine.AddFunc("isDebug", func() bool { return cm.Get().Logger.Debug })
	engine.AddFunc("dict", dict)

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
			return c.Status(code).SendString(err.Error())
		},
	})

	app.Use(HTMXMiddleware(), LogRequestsMiddleware())
	app.Static("/", "./public")
	app.Static("/images", cm.Get().ImagePath)
	app.Static("/audio", cm.Get().ImagePath)
	app.Get("/health", func(c *fiber.Ctx) error { return c.SendString("OK") })

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
