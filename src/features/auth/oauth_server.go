// This file implements Stella acting as its own OAuth 2.1 Authorization
// Server for MCP clients (dynamic client registration, authorization code +
// PKCE, access/refresh tokens), layered on top of the session cookie the
// OIDC login flow in handlers.go already establishes. See oauth_handlers.go
// for the HTTP endpoints that drive this.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	authCodeTTL    = 5 * time.Minute
	accessTokenTTL = time.Hour
)

// IssuedTokens is the token-endpoint response: a fresh access token, and a
// refresh token when one was issued (empty when refreshing, since refresh
// tokens are not rotated).
type IssuedTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64 // seconds
	Scope        string
}

// RegisterOAuthClient implements Dynamic Client Registration (RFC 7591) for
// public (PKCE-only, no client secret) MCP clients. Open to anyone — DCR is
// intentionally unauthenticated; security comes from PKCE plus the user
// consent screen, not client secrecy.
func (s *Service) RegisterOAuthClient(ctx context.Context, name string, redirectURIs []string) (*OAuthClient, error) {
	if len(redirectURIs) == 0 {
		return nil, fmt.Errorf("redirect_uris is required")
	}
	for _, ru := range redirectURIs {
		if err := checkRedirectURI(ru); err != nil {
			return nil, err
		}
	}
	c := &OAuthClient{
		ID:           randomToken(),
		Name:         strings.TrimSpace(name),
		RedirectURIs: redirectURIs,
		Created:      time.Now().Unix(),
	}
	if err := s.store.AddOAuthClient(ctx, c); err != nil {
		return nil, fmt.Errorf("registering oauth client: %w", err)
	}
	return c, nil
}

// checkRedirectURI rejects schemes that would enable XSS or make the
// authorize/token redirect otherwise unsafe. Any other scheme is allowed —
// MCP clients redirect to https callbacks, loopback http (native apps, RFC
// 8252), or app-specific custom schemes.
func checkRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid redirect_uri %q: %w", raw, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "", "javascript", "data", "vbscript":
		return fmt.Errorf("redirect_uri %q has a disallowed or missing scheme", raw)
	}
	return nil
}

// ResolveOAuthClientRedirect validates that redirectURI is registered for
// clientID. Callers must not redirect the browser on failure — an
// unregistered redirect_uri is exactly the open-redirect the OAuth security
// BCP warns against — show an in-page error instead.
func (s *Service) ResolveOAuthClientRedirect(ctx context.Context, clientID, redirectURI string) (*OAuthClient, error) {
	client, err := s.store.OAuthClientByID(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(client.RedirectURIs, redirectURI) {
		return nil, fmt.Errorf("redirect_uri does not match any URI registered for this client")
	}
	return client, nil
}

// IssueAuthCode records a single-use authorization code after the user
// approves the consent screen, and returns the raw code (only its hash is
// persisted).
func (s *Service) IssueAuthCode(ctx context.Context, clientID, userID, redirectURI, codeChallenge, scope string) (string, error) {
	code := randomToken()
	err := s.store.AddAuthCode(ctx, hashToken(code), &AuthCode{
		ClientID:      clientID,
		UserID:        userID,
		RedirectURI:   redirectURI,
		CodeChallenge: codeChallenge,
		Scope:         scope,
		Expires:       time.Now().Add(authCodeTTL).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("issuing authorization code: %w", err)
	}
	return code, nil
}

// ExchangeAuthorizationCode redeems a single-use authorization code for an
// access + refresh token pair, verifying the redirect_uri, client_id, and
// PKCE code_verifier all match what the code was issued for.
func (s *Service) ExchangeAuthorizationCode(ctx context.Context, code, clientID, redirectURI, codeVerifier string) (*IssuedTokens, error) {
	c, err := s.store.ConsumeAuthCode(ctx, hashToken(code), time.Now().Unix())
	if err != nil {
		return nil, err
	}
	if c.ClientID != clientID || c.RedirectURI != redirectURI {
		return nil, ErrInvalidGrant
	}
	if pkceChallengeS256(codeVerifier) != c.CodeChallenge {
		return nil, ErrInvalidGrant
	}
	return s.issueTokenPair(ctx, c.ClientID, c.UserID, c.Scope)
}

// RefreshAccessToken exchanges a refresh token for a fresh access token. The
// refresh token itself is not rotated — the same one keeps working until the
// user revokes the client from account settings.
func (s *Service) RefreshAccessToken(ctx context.Context, refreshToken, clientID string) (*IssuedTokens, error) {
	t, err := s.store.OAuthTokenByRefreshHash(ctx, hashToken(refreshToken))
	if err != nil {
		return nil, err
	}
	if t.ClientID != clientID {
		return nil, ErrInvalidGrant
	}
	access := randomToken()
	expires := time.Now().Add(accessTokenTTL).Unix()
	if err := s.store.RotateOAuthAccessToken(ctx, hashToken(refreshToken), hashToken(access), expires); err != nil {
		return nil, err
	}
	return &IssuedTokens{AccessToken: access, ExpiresIn: int64(accessTokenTTL.Seconds()), Scope: t.Scope}, nil
}

// issueTokenPair mints and persists a fresh access + refresh token for a
// newly completed authorization.
func (s *Service) issueTokenPair(ctx context.Context, clientID, userID, scope string) (*IssuedTokens, error) {
	access, refresh := randomToken(), randomToken()
	now := time.Now()
	err := s.store.AddOAuthToken(ctx, &OAuthToken{
		AccessHash:    hashToken(access),
		RefreshHash:   hashToken(refresh),
		ClientID:      clientID,
		UserID:        userID,
		Scope:         scope,
		AccessExpires: now.Add(accessTokenTTL).Unix(),
		Created:       now.Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("issuing tokens: %w", err)
	}
	// Codes and tokens accumulate quickly (every /oauth/token call), unlike
	// logins; sweep expired ones opportunistically here.
	_ = s.store.DeleteExpiredOAuthCodes(ctx, now.Unix())
	_ = s.store.DeleteExpiredOAuthTokens(ctx, now.Unix())
	return &IssuedTokens{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int64(accessTokenTTL.Seconds()),
		Scope:        scope,
	}, nil
}

// pkceChallengeS256 computes the RFC 7636 S256 code_challenge for a verifier,
// to compare against the one stored with the authorization code.
func pkceChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ListAuthorizedOAuthClients returns the OAuth clients userID has granted
// access to, for the "connected apps" list in account settings.
func (s *Service) ListAuthorizedOAuthClients(ctx context.Context, userID string) ([]*AuthorizedClient, error) {
	return s.store.ListOAuthClientsForUser(ctx, userID)
}

// RevokeOAuthClient revokes userID's access tokens for clientID, scoped to
// userID so one user can never revoke another's grant.
func (s *Service) RevokeOAuthClient(ctx context.Context, clientID, userID string) error {
	return s.store.RevokeOAuthClientForUser(ctx, clientID, userID)
}
