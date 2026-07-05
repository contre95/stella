package auth

import (
	"strings"
	"time"

	"github.com/contre95/stella/src/features/hosting/respond"
	"github.com/gofiber/fiber/v2"
)

// TokenHandler serves the API token management UI at /settings/tokens.
type TokenHandler struct {
	svc *Service
}

// NewTokenHandler returns a handler for the token management pages.
func (s *Service) NewTokenHandler() *TokenHandler {
	return &TokenHandler{svc: s}
}

// RegisterRoutes mounts token management routes. These sit behind the auth
// middleware so only logged-in users can reach them.
func (h *TokenHandler) RegisterRoutes(app *fiber.App) {
	app.Get("/settings/tokens", h.listTokens)
	app.Post("/settings/tokens", h.createToken)
	app.Delete("/settings/tokens/:hash", h.revokeToken)
}

type tokenView struct {
	Hash    string
	Name    string
	Created string
}

func toViews(tokens []*APIToken) []tokenView {
	views := make([]tokenView, len(tokens))
	for i, t := range tokens {
		views[i] = tokenView{
			Hash:    t.Hash,
			Name:    t.Name,
			Created: time.Unix(t.Created, 0).Format("2006-01-02"),
		}
	}
	return views
}

func (h *TokenHandler) listTokens(c *fiber.Ctx) error {
	tokens, err := h.svc.ListAPITokens(c.Context(), UserID(c))
	if err != nil {
		return err
	}
	return respond.Section(c, "tokens", fiber.Map{
		"Tokens": toViews(tokens),
	})
}

func (h *TokenHandler) createToken(c *fiber.Ctx) error {
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		name = "Unnamed token"
	}
	raw, err := h.svc.GenerateAPIToken(c.Context(), UserID(c), name)
	if err != nil {
		return err
	}
	tokens, err := h.svc.ListAPITokens(c.Context(), UserID(c))
	if err != nil {
		return err
	}
	return respond.Section(c, "tokens", fiber.Map{
		"Tokens":   toViews(tokens),
		"NewToken": raw,
	})
}

func (h *TokenHandler) revokeToken(c *fiber.Ctx) error {
	if err := h.svc.RevokeAPIToken(c.Context(), c.Params("hash")); err != nil {
		return err
	}
	tokens, err := h.svc.ListAPITokens(c.Context(), UserID(c))
	if err != nil {
		return err
	}
	return respond.Section(c, "tokens", fiber.Map{
		"Tokens": toViews(tokens),
	})
}
