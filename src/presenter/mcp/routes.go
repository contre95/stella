package mcp

import (
	"net/http"

	"github.com/contre95/stella/src/features/cards"
	adaptor "github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPRegistrar implements hosting.RouteRegistrar, mounting the MCP server
// at /mcp on the Fiber app using the StreamableHTTP transport.
type HTTPRegistrar struct {
	handler http.Handler
}

// NewHTTPRegistrar builds an HTTPRegistrar backed by a new MCP server.
func NewHTTPRegistrar(name, version string, cardsService *cards.Service) *HTTPRegistrar {
	srv := NewServer(name, version, cardsService)
	raw := mcpsdk.NewStreamableHTTPHandler(func(_ *http.Request) *mcpsdk.Server {
		return srv
	}, nil)
	return &HTTPRegistrar{handler: http.StripPrefix("/mcp", raw)}
}

// RegisterRoutes mounts the MCP handler at /mcp and /mcp/*.
func (h *HTTPRegistrar) RegisterRoutes(app *fiber.App) {
	adapted := adaptor.HTTPHandler(h.handler)
	app.All("/mcp", adapted)
	app.All("/mcp/*", adapted)
}
