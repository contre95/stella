package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/contre95/stella/src/features/auth"
)

func (s *SqliteSRS) AddAuthCode(ctx context.Context, codeHash string, c *auth.AuthCode) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, scope, expires)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		codeHash, c.ClientID, c.UserID, c.RedirectURI, c.CodeChallenge, c.Scope, c.Expires)
	return err
}

// ConsumeAuthCode fetches and deletes the code in the same call so it can
// never be redeemed twice, even under concurrent requests.
func (s *SqliteSRS) ConsumeAuthCode(ctx context.Context, codeHash string, now int64) (*auth.AuthCode, error) {
	var c auth.AuthCode
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM oauth_codes WHERE code_hash=? AND expires>?
		 RETURNING client_id, user_id, redirect_uri, code_challenge, scope, expires`,
		codeHash, now).Scan(&c.ClientID, &c.UserID, &c.RedirectURI, &c.CodeChallenge, &c.Scope, &c.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *SqliteSRS) DeleteExpiredOAuthCodes(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_codes WHERE expires<=?`, now)
	return err
}
