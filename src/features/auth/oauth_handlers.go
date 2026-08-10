package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	adaptor "github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	oauthFlowCookieName = "stella_oauth_authz"
	oauthFlowTTL        = 10 * time.Minute
)

// registerOAuthEndpoints mounts the public halves of the OAuth Authorization
// Server flow — discovery, dynamic client registration, and the token
// endpoint — none of which carry a session cookie, so they must sit ahead of
// Middleware() like /auth/login does. Called from RegisterRoutes.
func (s *Service) registerOAuthEndpoints(app *fiber.App) {
	app.Post("/oauth/register", s.registerClient)
	app.Post("/oauth/token", s.token)
	app.Get("/.well-known/oauth-authorization-server", s.authServerMetadata)
	app.Get("/.well-known/oauth-protected-resource", adaptor.HTTPHandler(sdkauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:             s.PublicURL() + "/mcp",
		AuthorizationServers: []string{s.PublicURL()},
		ScopesSupported:      []string{"mcp"},
	})))
	// Browser-based MCP clients (e.g. a claude.ai custom connector calling
	// these endpoints via fetch()) send a CORS preflight before a JSON POST;
	// answer it directly since these routes sit ahead of any CORS middleware.
	app.Options("/oauth/register", corsPreflight)
	app.Options("/oauth/token", corsPreflight)
}

// corsPreflight answers a CORS preflight OPTIONS request for the public
// discovery/register/token endpoints, which must be reachable cross-origin.
func corsPreflight(c *fiber.Ctx) error {
	c.Set("Access-Control-Allow-Origin", "*")
	c.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	c.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	return c.SendStatus(fiber.StatusNoContent)
}

// authServerMetadata serves RFC 8414 metadata describing Stella acting as its
// own OAuth Authorization Server for MCP clients — layered on top of the
// OIDC login the /oauth/authorize consent screen requires.
func (s *Service) authServerMetadata(c *fiber.Ctx) error {
	base := s.PublicURL()
	c.Set("Access-Control-Allow-Origin", "*")
	return c.JSON(oauthex.AuthServerMeta{
		Issuer:                            base,
		AuthorizationEndpoint:             base + "/oauth/authorize",
		TokenEndpoint:                     base + "/oauth/token",
		RegistrationEndpoint:              base + "/oauth/register",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
	})
}

// registerClient implements Dynamic Client Registration (RFC 7591). No auth
// is required — see RegisterOAuthClient's doc comment.
func (s *Service) registerClient(c *fiber.Ctx) error {
	c.Set("Access-Control-Allow-Origin", "*")
	var req oauthex.ClientRegistrationMetadata
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(oauthex.ClientRegistrationError{
			ErrorCode: "invalid_client_metadata", ErrorDescription: "malformed JSON body",
		})
	}
	client, err := s.RegisterOAuthClient(c.Context(), req.ClientName, req.RedirectURIs)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(oauthex.ClientRegistrationError{
			ErrorCode: "invalid_redirect_uri", ErrorDescription: err.Error(),
		})
	}
	// oauthex.ClientRegistrationResponse's Unix-timestamp JSON encoding is
	// implemented on the *pointer* receiver (it needs to turn the zero-value
	// time.Time fields into an omitted/int64 field instead of the default
	// RFC 3339 string) — c.JSON must be given a pointer, or that MarshalJSON
	// is skipped entirely and time.Time's default string encoding leaks
	// through, which strict clients (e.g. opencode's DCR response schema)
	// reject with "expected number, received string".
	return c.Status(fiber.StatusCreated).JSON(&oauthex.ClientRegistrationResponse{
		ClientRegistrationMetadata: oauthex.ClientRegistrationMetadata{
			RedirectURIs:            client.RedirectURIs,
			ClientName:              client.Name,
			TokenEndpointAuthMethod: "none",
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
		},
		ClientID:         client.ID,
		ClientIDIssuedAt: time.Unix(client.Created, 0),
	})
}

// token implements the token endpoint for both the authorization_code and
// refresh_token grants (RFC 6749 §4.1.3, §6).
func (s *Service) token(c *fiber.Ctx) error {
	c.Set("Access-Control-Allow-Origin", "*")
	var (
		tokens *IssuedTokens
		err    error
	)
	switch c.FormValue("grant_type") {
	case "authorization_code":
		tokens, err = s.ExchangeAuthorizationCode(c.Context(),
			c.FormValue("code"), c.FormValue("client_id"), c.FormValue("redirect_uri"), c.FormValue("code_verifier"))
	case "refresh_token":
		tokens, err = s.RefreshAccessToken(c.Context(), c.FormValue("refresh_token"), c.FormValue("client_id"))
	default:
		slog.Warn("oauth token request with unsupported grant_type", "grant_type", c.FormValue("grant_type"), "content_type", c.Get("Content-Type"))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unsupported_grant_type"})
	}
	if err != nil {
		code := "invalid_grant"
		if errors.Is(err, ErrOAuthClientNotFound) {
			code = "invalid_client"
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": code, "error_description": err.Error()})
	}
	resp := fiber.Map{
		"access_token": tokens.AccessToken,
		"token_type":   "Bearer",
		"expires_in":   tokens.ExpiresIn,
	}
	if tokens.RefreshToken != "" {
		resp["refresh_token"] = tokens.RefreshToken
	}
	if tokens.Scope != "" {
		resp["scope"] = tokens.Scope
	}
	return c.JSON(resp)
}

// pendingAuthorization is the OAuth request validated by the GET
// /oauth/authorize handler, carried to the consent decision in an
// HttpOnly, SameSite=Lax cookie. Reading it back doubles as CSRF protection
// for the consent POST — a cross-site request cannot carry the cookie, and a
// same-site one can only replay a request Stella itself already validated.
type pendingAuthorization struct {
	ClientID      string `json:"client_id"`
	ClientName    string `json:"client_name"`
	RedirectURI   string `json:"redirect_uri"`
	State         string `json:"state"`
	CodeChallenge string `json:"code_challenge"`
	Scope         string `json:"scope"`
}

func (s *Service) setPendingAuthorization(c *fiber.Ctx, p pendingAuthorization) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	s.setCookie(c, oauthFlowCookieName, base64.RawURLEncoding.EncodeToString(b), oauthFlowTTL)
	return nil
}

func (s *Service) popPendingAuthorization(c *fiber.Ctx) (*pendingAuthorization, error) {
	raw := c.Cookies(oauthFlowCookieName)
	s.clearCookie(c, oauthFlowCookieName)
	if raw == "" {
		return nil, fmt.Errorf("authorization request expired or was already used")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var p pendingAuthorization
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// OAuthConsentHandler serves the browser-facing halves of the authorization
// flow (/oauth/authorize): the consent screen and the user's decision. It
// sits behind the normal session Middleware() — unlike the rest of this file
// — so it can tell which user is consenting. An unauthenticated GET is
// redirected to /auth/login (with return_to) by that middleware for free,
// reusing the existing OIDC login flow as the "who is this" step.
type OAuthConsentHandler struct {
	svc *Service
}

// NewOAuthConsentHandler returns the handler for the consent screen.
func (s *Service) NewOAuthConsentHandler() *OAuthConsentHandler {
	return &OAuthConsentHandler{svc: s}
}

// RegisterRoutes mounts the consent screen routes.
func (h *OAuthConsentHandler) RegisterRoutes(app *fiber.App) {
	app.Get("/oauth/authorize", h.authorize)
	app.Post("/oauth/authorize", h.decide)
}

// authorize validates the incoming authorization request and renders the
// consent screen. redirect_uri is checked against the registered client
// before anything else — an unregistered redirect_uri must never be
// redirected to (that's an open-redirect), so that failure renders an
// in-page error instead. Once redirect_uri is trusted, any other validation
// error is reported by redirecting back to the client with an OAuth error.
func (h *OAuthConsentHandler) authorize(c *fiber.Ctx) error {
	clientID, redirectURI := c.Query("client_id"), c.Query("redirect_uri")
	client, err := h.svc.ResolveOAuthClientRedirect(c.Context(), clientID, redirectURI)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest,
			"Unknown client or unregistered redirect_uri — the MCP client may need to reconnect.")
	}

	state, challenge := c.Query("state"), c.Query("code_challenge")
	if c.Query("response_type") != "code" || c.Query("code_challenge_method") != "S256" || challenge == "" {
		return c.Redirect(redirectURLWithParams(redirectURI, map[string]string{
			"error":             "invalid_request",
			"error_description": `response_type must be "code" and PKCE (S256) is required`,
			"state":             state,
		}), fiber.StatusFound)
	}

	name := client.Name
	if name == "" {
		name = "An MCP client"
	}
	pending := pendingAuthorization{
		ClientID: clientID, ClientName: name, RedirectURI: redirectURI,
		State: state, CodeChallenge: challenge, Scope: c.Query("scope"),
	}
	if err := h.svc.setPendingAuthorization(c, pending); err != nil {
		return err
	}
	// client_name is whatever the client claimed at registration — DCR is
	// open, so a malicious client could register itself as e.g. "Claude
	// Desktop" to phish a user who follows a crafted /oauth/authorize link.
	// redirect_uri, by contrast, is exactly where the authorization code is
	// actually about to be sent — show it so the user has something to
	// verify beyond a self-asserted name.
	return c.Render("oauth_consent", fiber.Map{
		"ClientName":     name,
		"Scope":          pending.Scope,
		"RedirectOrigin": displayOrigin(redirectURI),
	})
}

// displayOrigin extracts a redirect_uri's scheme://host for display on the
// consent screen, e.g. "https://claude.ai" or "http://127.0.0.1:53219". Falls
// back to the raw URI for schemes with no host (native-app custom schemes).
func displayOrigin(redirectURI string) string {
	u, err := url.Parse(redirectURI)
	if err != nil || u.Host == "" {
		return redirectURI
	}
	return u.Scheme + "://" + u.Host
}

// decide handles the consent screen's Approve/Deny submission.
func (h *OAuthConsentHandler) decide(c *fiber.Ctx) error {
	pending, err := h.svc.popPendingAuthorization(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest,
			"Authorization request expired — go back to your MCP client and try connecting again.")
	}
	if c.FormValue("decision") != "approve" {
		return c.Redirect(redirectURLWithParams(pending.RedirectURI, map[string]string{
			"error": "access_denied", "state": pending.State,
		}), fiber.StatusFound)
	}
	code, err := h.svc.IssueAuthCode(c.Context(), pending.ClientID, UserID(c), pending.RedirectURI, pending.CodeChallenge, pending.Scope)
	if err != nil {
		return err
	}
	return c.Redirect(redirectURLWithParams(pending.RedirectURI, map[string]string{
		"code": code, "state": pending.State,
	}), fiber.StatusFound)
}

// redirectURLWithParams merges params into redirectURI's query string,
// skipping empty values (e.g. an omitted state). Falls back to the raw
// redirectURI if it fails to parse — it was already validated as a
// registered, well-formed URI by the time this is called.
func redirectURLWithParams(redirectURI string, params map[string]string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
