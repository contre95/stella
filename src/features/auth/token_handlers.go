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

type oauthClientView struct {
	ClientID string
	Name     string
	Created  string
}

func toOAuthClientViews(clients []*AuthorizedClient) []oauthClientView {
	views := make([]oauthClientView, len(clients))
	for i, c := range clients {
		views[i] = oauthClientView{
			ClientID: c.ClientID,
			Name:     c.Name,
			Created:  time.Unix(c.Created, 0).Format("2006-01-02"),
		}
	}
	return views
}

// settingsData assembles the shared view model for /settings/tokens: static
// API tokens plus the OAuth clients the user has authorized via /oauth/*.
func (h *TokenHandler) settingsData(c *fiber.Ctx) (fiber.Map, error) {
	tokens, err := h.svc.ListAPITokens(c.Context(), UserID(c))
	if err != nil {
		return nil, err
	}
	clients, err := h.svc.ListAuthorizedOAuthClients(c.Context(), UserID(c))
	if err != nil {
		return nil, err
	}
	return fiber.Map{"Tokens": toViews(tokens), "OAuthClients": toOAuthClientViews(clients)}, nil
}

func (h *TokenHandler) listTokens(c *fiber.Ctx) error {
	data, err := h.settingsData(c)
	if err != nil {
		return err
	}
	return respond.Section(c, "tokens", data)
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
	data, err := h.settingsData(c)
	if err != nil {
		return err
	}
	data["NewToken"] = raw
	return respond.Section(c, "tokens", data)
}

func (h *TokenHandler) revokeToken(c *fiber.Ctx) error {
	if err := h.svc.RevokeAPIToken(c.Context(), c.Params("hash"), UserID(c)); err != nil {
		return err
	}
	data, err := h.settingsData(c)
	if err != nil {
		return err
	}
	return respond.Section(c, "tokens", data)
}

func (h *TokenHandler) revokeOAuthClient(c *fiber.Ctx) error {
	if err := h.svc.RevokeOAuthClient(c.Context(), c.Params("clientID"), UserID(c)); err != nil {
		return err
	}
	data, err := h.settingsData(c)
	if err != nil {
		return err
	}
	return respond.Section(c, "tokens", data)
}
