package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/contre95/stella/src/features/auth"
)

func (s *SqliteSRS) AddOAuthClient(ctx context.Context, c *auth.OAuthClient) error {
	redirectURIs, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO oauth_clients (client_id, client_name, redirect_uris, created) VALUES (?, ?, ?, ?)`,
		c.ID, c.Name, string(redirectURIs), c.Created)
	return err
}

func (s *SqliteSRS) OAuthClientByID(ctx context.Context, clientID string) (*auth.OAuthClient, error) {
	var c auth.OAuthClient
	var redirectURIs string
	err := s.db.QueryRowContext(ctx,
		`SELECT client_id, client_name, redirect_uris, created FROM oauth_clients WHERE client_id=?`,
		clientID).Scan(&c.ID, &c.Name, &redirectURIs, &c.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrOAuthClientNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(redirectURIs), &c.RedirectURIs); err != nil {
		return nil, err
	}
	return &c, nil
}
