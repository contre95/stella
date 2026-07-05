package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/contre95/stella/src/features/auth"
)

// Compile-time assertion that we satisfy the auth persistence contract.
var _ auth.Store = (*SqliteSRS)(nil)

// UpsertUser returns the user with u's (issuer, subject) identity, creating it
// with u's ID when missing and refreshing name/email when they changed.
func (s *SqliteSRS) UpsertUser(ctx context.Context, u *auth.User) (*auth.User, error) {
	var existing auth.User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, issuer, subject, email, name, created FROM users WHERE issuer=? AND subject=?`,
		u.Issuer, u.Subject).
		Scan(&existing.ID, &existing.Issuer, &existing.Subject, &existing.Email, &existing.Name, &existing.Created)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO users (id, issuer, subject, email, name, created) VALUES (?, ?, ?, ?, ?, ?)`,
			u.ID, u.Issuer, u.Subject, u.Email, u.Name, u.Created)
		if err != nil {
			return nil, err
		}
		return u, nil
	case err != nil:
		return nil, err
	}
	if existing.Email != u.Email || existing.Name != u.Name {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE users SET email=?, name=? WHERE id=?`, u.Email, u.Name, existing.ID); err != nil {
			return nil, err
		}
		existing.Email, existing.Name = u.Email, u.Name
	}
	return &existing, nil
}

// UserByIdentity resolves an (issuer, subject) identity to its user;
// auth.ErrUnknownUser when no such user exists.
func (s *SqliteSRS) UserByIdentity(ctx context.Context, issuer, subject string) (*auth.User, error) {
	var u auth.User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, issuer, subject, email, name, created FROM users WHERE issuer=? AND subject=?`,
		issuer, subject).
		Scan(&u.ID, &u.Issuer, &u.Subject, &u.Email, &u.Name, &u.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrUnknownUser
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *SqliteSRS) AddSession(ctx context.Context, tokenHash, userID string, expires int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires) VALUES (?, ?, ?)`, tokenHash, userID, expires)
	return err
}

func (s *SqliteSRS) UserBySession(ctx context.Context, tokenHash string, now int64) (*auth.User, error) {
	var u auth.User
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.issuer, u.subject, u.email, u.name, u.created
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash=? AND s.expires>?`, tokenHash, now).
		Scan(&u.ID, &u.Issuer, &u.Subject, &u.Email, &u.Name, &u.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *SqliteSRS) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

func (s *SqliteSRS) DeleteExpiredSessions(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires<=?`, now)
	return err
}
