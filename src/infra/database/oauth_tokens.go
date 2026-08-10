package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/contre95/stella/src/features/auth"
)

func (s *SqliteSRS) AddOAuthToken(ctx context.Context, t *auth.OAuthToken) error {
	var refreshHash any
	if t.RefreshHash != "" {
		refreshHash = t.RefreshHash
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_tokens (access_token_hash, refresh_token_hash, client_id, user_id, scope, access_expires, refresh_expires, created)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.AccessHash, refreshHash, t.ClientID, t.UserID, t.Scope, t.AccessExpires, t.RefreshExpires, t.Created)
	return err
}

func (s *SqliteSRS) UserByOAuthAccessToken(ctx context.Context, accessHash string, now int64) (*auth.User, error) {
	var u auth.User
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.issuer, u.subject, u.email, u.name, u.created
		 FROM oauth_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.access_token_hash=? AND t.access_expires>?`, accessHash, now).
		Scan(&u.ID, &u.Issuer, &u.Subject, &u.Email, &u.Name, &u.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrInvalidOAuthToken
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *SqliteSRS) OAuthTokenByRefreshHash(ctx context.Context, refreshHash string, now int64) (*auth.OAuthToken, error) {
	var t auth.OAuthToken
	err := s.db.QueryRowContext(ctx,
		`SELECT access_token_hash, refresh_token_hash, client_id, user_id, scope, access_expires, refresh_expires, created
		 FROM oauth_tokens WHERE refresh_token_hash=? AND refresh_expires>?`, refreshHash, now).
		Scan(&t.AccessHash, &t.RefreshHash, &t.ClientID, &t.UserID, &t.Scope, &t.AccessExpires, &t.RefreshExpires, &t.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *SqliteSRS) RotateOAuthAccessToken(ctx context.Context, refreshHash, newAccessHash string, newExpires int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE oauth_tokens SET access_token_hash=?, access_expires=? WHERE refresh_token_hash=?`,
		newAccessHash, newExpires, refreshHash)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return auth.ErrInvalidGrant
	}
	return nil
}

func (s *SqliteSRS) ListOAuthClientsForUser(ctx context.Context, userID string) ([]*auth.AuthorizedClient, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.client_id, c.client_name, MAX(t.created)
		 FROM oauth_tokens t JOIN oauth_clients c ON c.client_id = t.client_id
		 WHERE t.user_id=?
		 GROUP BY c.client_id, c.client_name
		 ORDER BY MAX(t.created) DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var clients []*auth.AuthorizedClient
	for rows.Next() {
		c := &auth.AuthorizedClient{}
		if err := rows.Scan(&c.ClientID, &c.Name, &c.Created); err != nil {
			return nil, err
		}
		clients = append(clients, c)
	}
	return clients, rows.Err()
}

func (s *SqliteSRS) RevokeOAuthClientForUser(ctx context.Context, clientID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE client_id=? AND user_id=?`, clientID, userID)
	return err
}

// DeleteExpiredOAuthTokens purges grants whose refresh token has expired —
// once that's gone the access token can never be renewed again either, so
// the whole row is dead.
func (s *SqliteSRS) DeleteExpiredOAuthTokens(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE refresh_expires<=?`, now)
	return err
}
