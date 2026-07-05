package mcp

import (
	"net/http"
	"sync"

	"github.com/contre95/stella/src/features/auth"
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

// NewHTTPRegistrar builds an HTTPRegistrar that authenticates each request via
// authSvc and dispatches MCP tool calls as the token-owning user.
func NewHTTPRegistrar(name, version string, cardsService *cards.Service, authSvc *auth.Service) *HTTPRegistrar {
	var mu sync.Mutex
	servers := make(map[string]*mcpsdk.Server)

	getOrCreate := func(userID string) *mcpsdk.Server {
		mu.Lock()
		defer mu.Unlock()
		if srv, ok := servers[userID]; ok {
			return srv
		}
		srv := NewServer(name, version, cardsService, userID)
		servers[userID] = srv
		return srv
	}

	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		userID := auth.MCPUserIDFromContext(r.Context())
		if userID == "" {
			return nil
		}
		return getOrCreate(userID)
	}, nil)

	handler := authSvc.MCPBearerMiddleware(http.StripPrefix("/mcp", mcpHandler))
	return &HTTPRegistrar{handler: handler}
}

// RegisterRoutes mounts the MCP handler at /mcp and /mcp/*.
func (h *HTTPRegistrar) RegisterRoutes(app *fiber.App) {
	adapted := adaptor.HTTPHandler(h.handler)
	app.All("/mcp", adapted)
	app.All("/mcp/*", adapted)
}
