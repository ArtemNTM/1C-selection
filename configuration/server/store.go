package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bochkarevart/1c-selection/internal/store"
	"github.com/bochkarevart/1c-selection/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openStore(ctx context.Context, cfg Config) (*store.Store, error) {
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("configure PostgreSQL: %w", err)
	}
	if err := pool.Ping(startupCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	db := store.NewStore(pool)
	if err := migrations.Migrate(startupCtx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate PostgreSQL: %w", err)
	}
	if err := provisionClients(startupCtx, db, cfg.Clients, os.Stdout); err != nil {
		db.Close()
		return nil, fmt.Errorf("provision clients: %w", err)
	}
	return db, nil
}
