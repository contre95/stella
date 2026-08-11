// Package auth owns users, login sessions, and the OIDC login flow. When
// auth.enabled is false (single-user mode) it collapses to a middleware that
// pins every request to the built-in local user and registers no routes.
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

// APIToken grants bearer-token access to the MCP endpoint for its owner.
type APIToken struct {
	Hash    string // SHA-256 hex of the raw token (never store raw)
	UserID  string
	Name    string
	Created int64 // POSIX seconds
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
}

// Service drives authentication: the OIDC login flow, session cookies, and
// the request middleware. In single-user mode only the middleware is active.
type Service struct {
	store    Store
	cfg      *config.Manager
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
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
	provider, err := oidc.NewProvider(ctx, a.OIDC.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %q: %w", a.OIDC.Issuer, err)
	}
	// CIMD clients are public by definition (token_endpoint_auth_method:
	// none) — the client_id is the URL of the metadata document Stella
	// serves itself (see clientMetadata in handlers.go), not a
	// provider-issued string, and there is no secret.
	clientID, clientSecret := a.OIDC.ClientID, a.OIDC.ClientSecret
	if a.OIDC.CIMD {
		clientID, clientSecret = clientMetadataURL(a.OIDC.PublicURL), ""
	}
	s.verifier = provider.Verifier(&oidc.Config{ClientID: clientID})
	s.oauth = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  strings.TrimRight(a.OIDC.PublicURL, "/") + "/auth/callback",
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return s, nil
}

// SingleUser reports whether authentication is disabled (auth.enabled: false).
func (s *Service) SingleUser() bool { return !s.cfg.Get().Auth.Enabled }

// secureCookies reports whether cookies should carry the Secure flag, derived
// from the instance being served over https.
func (s *Service) secureCookies() bool {
	return strings.HasPrefix(s.cfg.Get().Auth.OIDC.PublicURL, "https://")
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
// token check; in multi-user mode a valid Bearer token is required.
func (s *Service) MCPBearerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var userID string
		if s.SingleUser() {
			userID = LocalUserID
		} else {
			parts := strings.Fields(r.Header.Get("Authorization"))
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			user, err := s.userByRawAPIToken(r.Context(), parts[1])
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			userID = user.ID
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpUserIDKey{}, userID)))
	})
}
