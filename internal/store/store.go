package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Executor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Database interface {
	Executor
	Begin(context.Context) (pgx.Tx, error)
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool: pool,
	}
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return s.pool.Exec(ctx, query, args...)
}

func (s *Store) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return s.pool.Query(ctx, query, args...)
}

func (s *Store) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return s.pool.QueryRow(ctx, query, args...)
}

func (s *Store) Begin(ctx context.Context) (pgx.Tx, error) {
	return s.pool.Begin(ctx)
}

func (s *Store) BeginTx(ctx context.Context, cfg pgx.TxOptions) (pgx.Tx, error) {
	return s.pool.BeginTx(ctx, cfg)
}
