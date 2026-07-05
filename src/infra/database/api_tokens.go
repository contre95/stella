package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/contre95/stella/src/features/auth"
)

func (s *SqliteSRS) AddAPIToken(ctx context.Context, tokenHash, userID, name string, created int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens (token_hash, user_id, name, created) VALUES (?, ?, ?, ?)`,
		tokenHash, userID, name, created)
	return err
}

func (s *SqliteSRS) UserByAPIToken(ctx context.Context, tokenHash string) (*auth.User, error) {
	var u auth.User
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.issuer, u.subject, u.email, u.name, u.created
		 FROM api_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash=?`, tokenHash).
		Scan(&u.ID, &u.Issuer, &u.Subject, &u.Email, &u.Name, &u.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrInvalidAPIToken
	}
	return &u, err
}

func (s *SqliteSRS) DeleteAPIToken(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE token_hash=?`, tokenHash)
	return err
}

func (s *SqliteSRS) ListAPITokens(ctx context.Context, userID string) ([]*auth.APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT token_hash, user_id, name, created FROM api_tokens WHERE user_id=? ORDER BY created DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []*auth.APIToken
	for rows.Next() {
		t := &auth.APIToken{}
		if err := rows.Scan(&t.Hash, &t.UserID, &t.Name, &t.Created); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}
