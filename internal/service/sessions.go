package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/jackc/pgx/v5"
)

func (s *Service) CreateSession(ctx context.Context, name, personalToken string) (model.HelloResponse, error) {
	client, err := s.Authenticate(ctx, name, personalToken)
	if err != nil {
		return model.HelloResponse{}, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return model.HelloResponse{}, err
	}
	out := model.HelloResponse{
		Client:       client,
		SessionToken: base64.RawURLEncoding.EncodeToString(tokenBytes),
	}
	hash := sha256.Sum256([]byte(out.SessionToken))
	now := s.cfg.Now().UTC()
	query, args, err := psql.Insert("sessions").
		Columns("token_hash", "client_id", "created_at", "expires_at").
		Values(hash[:], client.ID, now, now.Add(s.cfg.SessionTTL)).
		Suffix("RETURNING expires_at").
		ToSql()
	if err != nil {
		return model.HelloResponse{}, err
	}
	if err := s.db.QueryRow(ctx, query, args...).Scan(&out.ExpiresAt); err != nil {
		return model.HelloResponse{}, err
	}
	return out, nil
}

func (s *Service) AuthenticateSession(ctx context.Context, token string) (model.Client, error) {
	if token == "" || len(token) > 512 {
		return model.Client{}, model.NewError("unauthorized", "invalid or expired session")
	}
	hash := sha256.Sum256([]byte(token))
	columns := make([]string, len(clientColumns))
	for i, column := range clientColumns {
		columns[i] = "c." + column
	}
	query, args, err := psql.Select(columns...).
		From("sessions s").
		Join("clients c ON c.id = s.client_id").
		Where(sq.Eq{"s.token_hash": hash[:], "c.enabled": true}).
		Where(sq.Gt{"s.expires_at": s.cfg.Now().UTC()}).
		ToSql()
	if err != nil {
		return model.Client{}, err
	}
	client, err := scanClient(s.db.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Client{}, model.NewError("unauthorized", "invalid or expired session")
	}
	return client, err
}

func (s *Service) DeleteExpiredSessions(ctx context.Context) error {
	query, args, err := psql.Delete("sessions").
		Where(sq.LtOrEq{"expires_at": s.cfg.Now().UTC()}).
		ToSql()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}
