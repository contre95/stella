// Package auth owns users, login sessions, and the OIDC login flow. In
// single-user mode (auth.singleUser: true) it collapses to a middleware that
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

// Store is the persistence contract for users and sessions.
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
	if a.SingleUser {
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
	s.verifier = provider.Verifier(&oidc.Config{ClientID: a.OIDC.ClientID})
	s.oauth = &oauth2.Config{
		ClientID:     a.OIDC.ClientID,
		ClientSecret: a.OIDC.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  strings.TrimRight(a.OIDC.PublicURL, "/") + "/auth/callback",
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return s, nil
}

// SingleUser reports whether authentication is disabled.
func (s *Service) SingleUser() bool { return s.cfg.Get().Auth.SingleUser }

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
