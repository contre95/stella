package auth

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const (
	sessionCookieName = "stella_session"
	sessionTTL        = 30 * 24 * time.Hour
	// flowCookieName holds the state/nonce/PKCE-verifier triple during the
	// round trip to the identity provider.
	flowCookieName = "stella_oidc_flow"
	flowTTL        = 10 * time.Minute
	// clientMetadataPath is where Stella serves its own Client ID Metadata
	// Document (CIMD) when auth.oidc.cimd is enabled.
	clientMetadataPath = "/auth/client-metadata.json"
)

// clientMetadataURL is the client_id CIMD-registered clients authenticate
// as — per the CIMD spec it must exactly match the URL the document is
// served at.
func clientMetadataURL(publicURL string) string {
	return strings.TrimRight(publicURL, "/") + clientMetadataPath
}

// RegisterRoutes mounts the login endpoints. They sit in front of the auth
// middleware so an unauthenticated browser can reach them. Single-user mode
// has no login, so nothing is registered.
func (s *Service) RegisterRoutes(app *fiber.App) {
	if s.SingleUser() {
		return
	}
	app.Get("/auth/login", s.login)
	app.Get("/auth/callback", s.callback)
	app.Get("/auth/logout", s.logout)
	if s.cfg.Get().Auth.OIDC.CIMD {
		app.Get(clientMetadataPath, s.clientMetadata)
	}
	s.registerOAuthEndpoints(app)
}

// clientMetadata serves Stella's Client ID Metadata Document so an identity
// provider that supports CIMD (e.g. Pocket ID) can register Stella as an
// OIDC client without a manually created client record — the operator just
// allowlists this URL at the provider instead.
func (s *Service) clientMetadata(c *fiber.Ctx) error {
	a := s.cfg.Get().Auth
	base := strings.TrimRight(a.PublicURL, "/")
	return c.JSON(fiber.Map{
		"client_id":                  clientMetadataURL(a.PublicURL),
		"client_name":                s.cfg.Get().Server.AppName,
		"redirect_uris":              []string{base + "/auth/callback"},
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

// login starts the OIDC authorization-code flow: remember state, nonce, PKCE
// verifier, and where to return the browser afterwards in a short-lived
// cookie, then hand the browser to the issuer.
func (s *Service) login(c *fiber.Ctx) error {
	state, nonce, verifier := randomToken(), randomToken(), oauth2.GenerateVerifier()
	returnTo := base64.RawURLEncoding.EncodeToString([]byte(sameOriginPath(c.Query("return_to"))))
	s.setCookie(c, flowCookieName, state+"."+nonce+"."+verifier+"."+returnTo, flowTTL)
	return c.Redirect(
		s.oauth.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.S256ChallengeOption(verifier)),
		fiber.StatusFound,
	)
}

// sameOriginPath restricts a return_to value to a same-origin relative path,
// ruling out its use as an open redirect — "https://evil.example" and
// "//evil.example" (protocol-relative) both parse with a non-empty host.
func sameOriginPath(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	if u, err := url.Parse(raw); err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return raw
}

// callback finishes the flow: validate state, exchange the code, verify the
// ID token (issuer, audience, signature, nonce), upsert the user, and open a
// session.
func (s *Service) callback(c *fiber.Ctx) error {
	flow := strings.SplitN(c.Cookies(flowCookieName), ".", 4)
	s.clearCookie(c, flowCookieName)
	if len(flow) != 4 || c.Query("state") == "" || c.Query("state") != flow[0] {
		return fiber.NewError(fiber.StatusBadRequest, "login flow expired or state mismatch — go to /auth/login and try again")
	}
	nonce, verifier := flow[1], flow[2]
	returnTo := "/"
	if b, err := base64.RawURLEncoding.DecodeString(flow[3]); err == nil {
		returnTo = sameOriginPath(string(b))
	}
	if e := c.Query("error"); e != "" {
		return fiber.NewError(fiber.StatusBadGateway, "identity provider error: "+e+": "+c.Query("error_description"))
	}

	token, err := s.oauth.Exchange(c.Context(), c.Query("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		return fmt.Errorf("exchanging authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return fiber.NewError(fiber.StatusBadGateway, "identity provider returned no id_token")
	}
	idToken, err := s.verifier.Verify(c.Context(), rawIDToken)
	if err != nil {
		return fmt.Errorf("verifying id token: %w", err)
	}
	if idToken.Nonce != nonce {
		return fiber.NewError(fiber.StatusBadRequest, "id token nonce mismatch")
	}

	var claims struct {
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return fmt.Errorf("decoding id token claims: %w", err)
	}
	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	now := time.Now()
	user, err := s.store.UpsertUser(c.Context(), &User{
		ID:      uuid.NewString(),
		Issuer:  idToken.Issuer,
		Subject: idToken.Subject,
		Email:   claims.Email,
		Name:    name,
		Created: now.Unix(),
	})
	if err != nil {
		return fmt.Errorf("upserting user: %w", err)
	}

	sessionToken := randomToken()
	if err := s.store.AddSession(c.Context(), hashToken(sessionToken), user.ID, now.Add(sessionTTL).Unix()); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	// Logins are rare; piggyback session garbage collection on them.
	_ = s.store.DeleteExpiredSessions(c.Context(), now.Unix())
	s.setCookie(c, sessionCookieName, sessionToken, sessionTTL)
	return c.Redirect(returnTo, fiber.StatusFound)
}

// logout deletes the session and shows a minimal signed-out page instead of
// bouncing straight back to the issuer, which would silently sign in again
// while the provider's own session is still alive.
func (s *Service) logout(c *fiber.Ctx) error {
	if token := c.Cookies(sessionCookieName); token != "" {
		_ = s.store.DeleteSession(c.Context(), hashToken(token))
	}
	s.clearCookie(c, sessionCookieName)
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return c.SendString(`<!DOCTYPE html><html><head><title>Signed out</title></head>` +
		`<body style="font-family:sans-serif;display:grid;place-items:center;height:100vh;margin:0">` +
		`<p>Signed out of Stella. <a href="/auth/login">Sign in again</a></p></body></html>`)
}

func (s *Service) setCookie(c *fiber.Ctx, name, value string, ttl time.Duration) {
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  time.Now().Add(ttl),
		HTTPOnly: true,
		Secure:   s.secureCookies(),
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

func (s *Service) clearCookie(c *fiber.Ctx, name string) {
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HTTPOnly: true,
		Secure:   s.secureCookies(),
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}
