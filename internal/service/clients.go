package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	sq "github.com/Masterminds/squirrel"
	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/bochkarevart/1c-selection/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var clientColumns = []string{
	"id",
	"name",
	"kind",
	"role",
	"trust",
	"enabled",
	"created_at",
}

func scanClient(row pgx.Row) (model.Client, error) {
	var c model.Client
	err := row.Scan(&c.ID, &c.Name, &c.Kind, &c.Role, &c.Trust, &c.Enabled, &c.CreatedAt)
	return c, err
}

func validTrust(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}

func validateClient(req model.CreateClientRequest) error {
	if strings.TrimSpace(req.Name) == "" ||
		strings.ContainsRune(req.Name, '\x00') ||
		utf8.RuneCountInString(req.Name) > 100 ||
		(req.Kind != "staff" && req.Kind != "freelance") ||
		(req.Role != "reporter" && req.Role != "editor") ||
		!validTrust(req.Trust) {
		return model.NewError("invalid", "name, kind (staff/freelance), role (reporter/editor), and trust in [0,1] are required")
	}
	return nil
}

func clientEnabled(value *bool) bool {
	return value == nil || *value
}

func newPersonalToken() (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(tokenBytes), nil
}

func (s *Service) CreateClient(ctx context.Context, req model.CreateClientRequest) (model.ClientCredentials, error) {
	var out model.ClientCredentials
	if err := validateClient(req); err != nil {
		return out, err
	}
	token, err := newPersonalToken()
	if err != nil {
		return out, err
	}
	out.Token = token
	hash := sha256.Sum256([]byte(out.Token))
	query, args, err := psql.Insert("clients").
		Columns("name", "kind", "role", "trust", "token_hash", "enabled").
		Values(req.Name, req.Kind, req.Role, req.Trust, hash[:], clientEnabled(req.Enabled)).
		Suffix("RETURNING " + strings.Join(clientColumns, ", ")).
		ToSql()
	if err != nil {
		return out, err
	}
	c, err := scanClient(s.db.QueryRow(ctx, query, args...))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return model.ClientCredentials{}, model.NewError("conflict", "client name already exists")
		}
		return model.ClientCredentials{}, err
	}
	out.Client = c
	if s.cache != nil {
		s.cache.InvalidateClients()
		s.cache.InvalidateStories()
	}
	return out, nil
}

func (s *Service) ClientByID(ctx context.Context, id int64) (model.Client, error) {
	query, args, err := psql.Select(clientColumns...).
		From("clients").
		Where(sq.Eq{"id": id}).
		ToSql()
	if err != nil {
		return model.Client{}, err
	}
	c, err := scanClient(s.db.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, model.NewError("not_found", "client not found")
	}
	return c, err
}

func (s *Service) Authenticate(ctx context.Context, name, token string) (model.Client, error) {
	var c model.Client
	var expected []byte
	query, args, err := psql.Select(clientColumns...).
		Column("token_hash").
		From("clients").
		Where(sq.Eq{"name": name}).
		ToSql()
	if err != nil {
		return c, err
	}
	err = s.db.QueryRow(ctx, query, args...).Scan(
		&c.ID,
		&c.Name,
		&c.Kind,
		&c.Role,
		&c.Trust,
		&c.Enabled,
		&c.CreatedAt,
		&expected,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	actual := sha256.Sum256([]byte(token))
	if errors.Is(err, pgx.ErrNoRows) || !c.Enabled || subtle.ConstantTimeCompare(actual[:], expected) != 1 {
		return model.Client{}, model.NewError("unauthorized", "invalid client credentials")
	}
	return c, nil
}

func (s *Service) UpdateClient(ctx context.Context, id int64, req model.UpdateClientRequest) (model.Client, error) {
	if req.Trust == nil && req.Enabled == nil {
		return model.Client{}, model.NewError("invalid", "trust or enabled is required")
	}
	if req.Trust != nil && !validTrust(*req.Trust) {
		return model.Client{}, model.NewError("invalid", "trust must be a finite number in [0,1]")
	}
	update := psql.Update("clients").
		Where(sq.Eq{"id": id}).
		Suffix("RETURNING " + strings.Join(clientColumns, ", "))
	if req.Trust != nil {
		update = update.Set("trust", *req.Trust)
	}
	if req.Enabled != nil {
		update = update.Set("enabled", *req.Enabled)
	}
	query, args, err := update.ToSql()
	if err != nil {
		return model.Client{}, err
	}
	c, err := scanClient(s.db.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, model.NewError("not_found", "client not found")
	}
	if err != nil {
		return c, err
	}
	if s.cache != nil {
		s.cache.InvalidateClients()
		s.cache.InvalidateStories()
	}
	return c, nil
}

func (s *Service) ListClients(ctx context.Context) ([]model.Client, error) {
	if s.cache != nil {
		if result, ok := s.cache.Clients(); ok {
			return result, nil
		}
	}
	query, args, err := psql.Select(clientColumns...).
		From("clients").
		OrderBy("id").
		ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.Client, 0)
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func ValidateProvisionClient(req model.ProvisionClientRequest) error {
	if err := validateClient(req.CreateClientRequest); err != nil {
		return err
	}
	if req.Token != "" && (len(req.Token) < 32 || len(req.Token) > 512) {
		return model.NewError("invalid", "configured client token must contain 32 to 512 bytes")
	}
	return nil
}

// ProvisionClients issues tokens for new clients that have no supplied token.
// save runs before commit so a failed credential write cannot strand a new account.
// Existing clients keep their token, role, trust and enabled state.
func ProvisionClients(ctx context.Context, db store.Database, clients []model.ProvisionClientRequest, save func(model.ClientCredentials) error) ([]model.ClientCredentials, error) {
	for _, req := range clients {
		if err := ValidateProvisionClient(req); err != nil {
			return nil, err
		}
	}
	if len(clients) == 0 {
		return nil, nil
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var issued []model.ClientCredentials
	for _, req := range clients {
		generated := req.Token == ""
		if generated {
			req.Token, err = newPersonalToken()
			if err != nil {
				return nil, err
			}
		}
		hash := sha256.Sum256([]byte(req.Token))
		query, args, err := psql.Insert("clients").
			Columns("name", "kind", "role", "trust", "token_hash", "enabled").
			Values(req.Name, req.Kind, req.Role, req.Trust, hash[:], clientEnabled(req.Enabled)).
			Suffix("ON CONFLICT (name) DO NOTHING RETURNING " + strings.Join(clientColumns, ", ")).
			ToSql()
		if err != nil {
			return nil, err
		}
		client, err := scanClient(tx.QueryRow(ctx, query, args...))
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		credentials := model.ClientCredentials{Client: client, Token: req.Token}
		if generated {
			if save != nil {
				if err := save(credentials); err != nil {
					return nil, err
				}
			}
		}
		issued = append(issued, credentials)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return issued, nil
}
