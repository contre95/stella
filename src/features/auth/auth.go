// Package auth owns users, login sessions, the OIDC login flow, and (in
// multi-user mode) Stella acting as its own OAuth Authorization Server for
// MCP clients — see oauth_server.go and oauth_handlers.go. When auth.enabled
// is false (single-user mode) it collapses to a middleware that pins every
// request to the built-in local user and registers no routes.
//
// Scope is not currently an access-control boundary: every credential this
// package can issue — a static API token, or an OAuth access token from any
// approved client — grants the same full read/write/delete access to the
// owning user's data. OAuth requests still carry a "scope" value end to end
// (consent screen, AuthCode, OAuthToken) and it's shown to the user and
// echoed back to the client, but nothing checks it against what a tool call
// is allowed to do. A scope of "read-only" would look identical, in terms of
// actual access, to no scope at all. Narrowing this would mean threading a
// granted-scope check into every MCP tool handler (see cards.NewMCPServer) —
// out of scope for now, but the plumbing is already there to build on.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/contre95/stella/src/features/config"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// LocalUserID is the fixed ID of the built-in account that owns all data in
// single-user mode.
const LocalUserID = "local"

// ErrNoSession is returned by Store.UserBySession for unknown or expired
// session tokens.
var ErrNoSession = errors.New("no session")

// ErrInvalidAPIToken is returned by Store.UserByAPIToken when the token is
// unknown.
var ErrInvalidAPIToken = errors.New("invalid api token")

// ErrInvalidOAuthToken is returned by Store.UserByOAuthAccessToken when the
// access token is unknown, revoked, or expired.
var ErrInvalidOAuthToken = errors.New("invalid oauth token")

// ErrOAuthClientNotFound is returned by Store.OAuthClientByID for an unknown
// client_id.
var ErrOAuthClientNotFound = errors.New("oauth client not found")

// ErrInvalidGrant is returned by Store.ConsumeAuthCode and
// Store.OAuthTokenByRefreshHash for an unknown, expired, or already-used
// code/token — mirrors the OAuth "invalid_grant" error code.
var ErrInvalidGrant = errors.New("invalid grant")

// APIToken grants bearer-token access to the MCP endpoint for its owner.
type APIToken struct {
	Hash    string // SHA-256 hex of the raw token (never store raw)
	UserID  string
	Name    string
	Created int64 // POSIX seconds
}

// OAuthClient is an MCP client dynamically registered via RFC 7591. Stella
// only supports public clients (PKCE, no client secret), so there is no
// secret to store.
type OAuthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	Created      int64 // POSIX seconds
}

// AuthCode is a single-use OAuth authorization code, bound to the client,
// user, redirect URI, and PKCE challenge it was issued for.
type AuthCode struct {
	ClientID      string
	UserID        string
	RedirectURI   string
	CodeChallenge string // PKCE S256 challenge; "plain" is not supported
	Scope         string // carried through end to end, not enforced — see the package doc comment
	Expires       int64  // POSIX seconds
}

// OAuthToken is an access/refresh token pair issued to an OAuth client on
// behalf of a user. RefreshHash is empty for a token with no refresh token.
// Scope is stored and echoed back to the client but not currently enforced —
// every grant (OAuth or a static API token) has the same full access; see the
// package doc comment.
type OAuthToken struct {
	AccessHash     string
	RefreshHash    string
	ClientID       string
	UserID         string
	Scope          string
	AccessExpires  int64 // POSIX seconds
	RefreshExpires int64 // POSIX seconds; the whole grant dies here, not just the access token
	Created        int64 // POSIX seconds
}

// AuthorizedClient is an OAuth client a user has granted access to, for the
// "connected apps" list in account settings.
type AuthorizedClient struct {
	ClientID string
	Name     string
	Created  int64 // POSIX seconds of the most recent authorization
}

// User is an account that owns decks, groups, facts, cards, and reviews.
type User struct {
	ID      string
	Issuer  string // OIDC issuer URL, or "local" for the single-user account
	Subject string // OIDC subject claim; unique per issuer
	Email   string
	Name    string
	Created int64 // POSIX seconds
}

// DisplayName is the best human label available for the user.
func (u *User) DisplayName() string {
	switch {
	case u.Name != "":
		return u.Name
	case u.Email != "":
		return u.Email
	default:
		return u.Subject
	}
}

// Store is the persistence contract for users, sessions, and API tokens.
type Store interface {
	// UpsertUser returns the user with u's (issuer, subject) identity, creating
	// it with u's ID if missing and refreshing name/email if they changed.
	UpsertUser(ctx context.Context, u *User) (*User, error)
	AddSession(ctx context.Context, tokenHash, userID string, expires int64) error
	// UserBySession resolves an unexpired session token hash to its user;
	// ErrNoSession when the session is unknown or expired.
	UserBySession(ctx context.Context, tokenHash string, now int64) (*User, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteExpiredSessions(ctx context.Context, now int64) error
	AddAPIToken(ctx context.Context, tokenHash, userID, name string, created int64) error
	// UserByAPIToken resolves a token hash to its owner; ErrInvalidAPIToken
	// when the token is unknown.
	UserByAPIToken(ctx context.Context, tokenHash string) (*User, error)
	// DeleteAPIToken deletes the token only if it belongs to userID, so one
	// user can never revoke another's token even if they learn its hash.
	DeleteAPIToken(ctx context.Context, tokenHash, userID string) error
	ListAPITokens(ctx context.Context, userID string) ([]*APIToken, error)

	// AddOAuthClient persists a dynamically registered OAuth client.
	AddOAuthClient(ctx context.Context, c *OAuthClient) error
	// OAuthClientByID returns the registered client, or ErrOAuthClientNotFound.
	OAuthClientByID(ctx context.Context, clientID string) (*OAuthClient, error)

	AddAuthCode(ctx context.Context, codeHash string, c *AuthCode) error
	// ConsumeAuthCode fetches and deletes an authorization code by hash in one
	// step, so a code can never be redeemed twice; ErrInvalidGrant if unknown
	// or expired.
	ConsumeAuthCode(ctx context.Context, codeHash string, now int64) (*AuthCode, error)

	AddOAuthToken(ctx context.Context, t *OAuthToken) error
	// UserByOAuthAccessToken resolves an unexpired access token hash to its
	// owner; ErrInvalidOAuthToken when unknown or expired.
	UserByOAuthAccessToken(ctx context.Context, accessHash string, now int64) (*User, error)
	// OAuthTokenByRefreshHash returns the token record for an unexpired
	// refresh token hash; ErrInvalidGrant when unknown or expired — the
	// client must then send the user through /oauth/authorize again.
	OAuthTokenByRefreshHash(ctx context.Context, refreshHash string, now int64) (*OAuthToken, error)
	// RotateOAuthAccessToken replaces the access token bound to a refresh
	// token with a newly issued one, keeping the same refresh token.
	RotateOAuthAccessToken(ctx context.Context, refreshHash, newAccessHash string, newExpires int64) error
	// ListOAuthClientsForUser returns the clients userID has authorized, most
	// recently authorized first.
	ListOAuthClientsForUser(ctx context.Context, userID string) ([]*AuthorizedClient, error)
	// RevokeOAuthClientForUser deletes every token userID holds for clientID,
	// scoped to userID so one user can never revoke another's grant.
	RevokeOAuthClientForUser(ctx context.Context, clientID, userID string) error
	DeleteExpiredOAuthCodes(ctx context.Context, now int64) error
	DeleteExpiredOAuthTokens(ctx context.Context, now int64) error
}

// Service drives authentication: the OIDC login flow, session cookies, and
// the request middleware. In single-user mode only the middleware is active.
type Service struct {
	store      Store
	cfg        *config.Manager
	publicURLs []string // normalized (no trailing slash); [0] is the fallback for an unrecognized Host
	endpoint   oauth2.Endpoint
	verifiers  map[string]*oidc.IDTokenVerifier // keyed by the matching entry in publicURLs
}

// NewService builds the auth service. Single-user mode just ensures the local
// account exists; multi-user mode also runs OIDC discovery against the
// configured issuer, so the identity provider must be reachable at startup.
func NewService(ctx context.Context, store Store, cfg *config.Manager) (*Service, error) {
	s := &Service{store: store, cfg: cfg}
	a := cfg.Get().Auth
	if !a.Enabled {
		_, err := store.UpsertUser(ctx, &User{
			ID: LocalUserID, Issuer: "local", Subject: "local", Created: time.Now().Unix(),
		})
		if err != nil {
			return nil, fmt.Errorf("ensuring local user: %w", err)
		}
		return s, nil
	}
	for _, u := range a.PublicURLs {
		s.publicURLs = append(s.publicURLs, strings.TrimRight(u, "/"))
	}
	provider, err := oidc.NewProvider(ctx, a.OIDC.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %q: %w", a.OIDC.Issuer, err)
	}
	s.endpoint = provider.Endpoint()
	// CIMD clients are public by definition (token_endpoint_auth_method:
	// none) — the client_id is the URL of the metadata document Stella
	// serves itself (see clientMetadata in handlers.go), not a
	// provider-issued string, and there is no secret. Under CIMD, each
	// configured public URL is a distinct client identity to the provider
	// (its client_id is that URL's own metadata document), so each needs its
	// own verifier checking the ID token audience against it.
	s.verifiers = make(map[string]*oidc.IDTokenVerifier, len(s.publicURLs))
	for _, publicURL := range s.publicURLs {
		clientID := a.OIDC.ClientID
		if a.OIDC.CIMD {
			clientID = clientMetadataURL(publicURL)
		}
		s.verifiers[publicURL] = provider.Verifier(&oidc.Config{ClientID: clientID})
	}
	return s, nil
}

// SingleUser reports whether authentication is disabled (auth.enabled: false).
func (s *Service) SingleUser() bool { return !s.cfg.Get().Auth.Enabled }

// oauthConfig builds the oauth2.Config for logging a browser in against the
// upstream OIDC provider, scoped to publicURL: the client_id (in CIMD mode)
// and redirect_uri both depend on which of Auth.PublicURLs the request came
// in on.
func (s *Service) oauthConfig(publicURL string) *oauth2.Config {
	a := s.cfg.Get().Auth
	clientID, clientSecret := a.OIDC.ClientID, a.OIDC.ClientSecret
	if a.OIDC.CIMD {
		clientID, clientSecret = clientMetadataURL(publicURL), ""
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     s.endpoint,
		RedirectURL:  publicURL + "/auth/callback",
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
}

// secureCookies reports whether cookies should carry the Secure flag, derived
// from publicURL — the request's resolved base URL — being served over https.
func (s *Service) secureCookies(publicURL string) bool {
	return strings.HasPrefix(publicURL, "https://")
}

// effectivePublicURL returns the configured Auth.PublicURLs entry whose host
// matches host (as sent in the request's Host header), so the login flow and
// the OAuth AS surface for MCP clients both mirror whichever domain the
// instance was actually reached at. Falls back to the first configured entry
// for a Host that matches none of them — e.g. a request via a raw IP.
func (s *Service) effectivePublicURL(host string) string {
	for _, u := range s.publicURLs {
		if pu, err := url.Parse(u); err == nil && pu.Host == host {
			return u
		}
	}
	if len(s.publicURLs) > 0 {
		return s.publicURLs[0]
	}
	return ""
}

// randomToken returns a fresh 256-bit URL-safe random string.
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken maps a session token to the digest stored in the database, so a
// leaked database does not leak usable session tokens.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// GenerateAPIToken mints a new random bearer token for the user. The raw token
// is returned once — only its SHA-256 hash is persisted.
func (s *Service) GenerateAPIToken(ctx context.Context, userID, name string) (string, error) {
	raw := randomToken()
	return raw, s.store.AddAPIToken(ctx, hashToken(raw), userID, name, time.Now().Unix())
}

// ListAPITokens returns all API tokens for the user (hashes, never raw values).
func (s *Service) ListAPITokens(ctx context.Context, userID string) ([]*APIToken, error) {
	return s.store.ListAPITokens(ctx, userID)
}

// RevokeAPIToken deletes an API token by its hash, scoped to its owner so a
// user can never revoke another user's token.
func (s *Service) RevokeAPIToken(ctx context.Context, tokenHash, userID string) error {
	return s.store.DeleteAPIToken(ctx, tokenHash, userID)
}

// userByRawAPIToken resolves a raw bearer token to its owner by hashing first.
func (s *Service) userByRawAPIToken(ctx context.Context, raw string) (*User, error) {
	return s.store.UserByAPIToken(ctx, hashToken(raw))
}

// UserByBearer resolves a raw bearer token to its owning user. Used by the
// device sync API, which authenticates with API tokens in all auth modes.
func (s *Service) UserByBearer(ctx context.Context, raw string) (*User, error) {
	return s.userByRawAPIToken(ctx, raw)
}

// mcpUserIDKey is the context key used to propagate the authenticated user ID
// into the net/http MCP handler.
type mcpUserIDKey struct{}

// MCPUserIDFromContext returns the user ID injected by MCPBearerMiddleware, or
// the empty string if the middleware was not run.
func MCPUserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(mcpUserIDKey{}).(string)
	return v
}

// MCPBearerMiddleware is a net/http middleware that authenticates requests to
// the MCP endpoint. In single-user mode the local user is assumed without a
// token check; in multi-user mode a valid Bearer token is required — either a
// long-lived static API token (/settings/tokens) or an OAuth access token
// issued via the /oauth/* flow (see oauth_server.go).
func (s *Service) MCPBearerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var userID string
		if s.SingleUser() {
			userID = LocalUserID
		} else {
			parts := strings.Fields(r.Header.Get("Authorization"))
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				s.mcpUnauthorized(w, r, "missing bearer token")
				return
			}
			user, err := s.userByMCPBearer(r.Context(), parts[1])
			if err != nil {
				s.mcpUnauthorized(w, r, "invalid or expired token")
				return
			}
			userID = user.ID
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpUserIDKey{}, userID)))
	})
}

// userByMCPBearer resolves a raw MCP bearer token against both credential
// stores, trying the static API tokens first.
func (s *Service) userByMCPBearer(ctx context.Context, raw string) (*User, error) {
	if user, err := s.userByRawAPIToken(ctx, raw); err == nil {
		return user, nil
	}
	return s.store.UserByOAuthAccessToken(ctx, hashToken(raw), time.Now().Unix())
}

// mcpUnauthorized replies 401 with a WWW-Authenticate header pointing MCP
// clients at protected resource metadata (RFC 9728 / the MCP authorization
// spec), so a client with no token yet can discover how to obtain one.
func (s *Service) mcpUnauthorized(w http.ResponseWriter, r *http.Request, msg string) {
	publicURL := s.effectivePublicURL(r.Host)
	w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer resource_metadata=%q", publicURL+"/.well-known/oauth-protected-resource"))
	http.Error(w, msg, http.StatusUnauthorized)
}
