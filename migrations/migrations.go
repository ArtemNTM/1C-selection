package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	sq "github.com/Masterminds/squirrel"
	"github.com/bochkarevart/1c-selection/internal/store"
	"github.com/jackc/pgx/v5"
)

//go:embed *.sql
var files embed.FS

func Migrate(ctx context.Context, db store.Database) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	query, args, err := sq.StatementBuilder.PlaceholderFormat(sq.Dollar).
		Select().
		Column(sq.Expr("pg_advisory_xact_lock(?)", int64(732031918))).
		ToSql()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return err
	}
	if err := Apply(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func Apply(ctx context.Context, tx pgx.Tx) error {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return err
	}
	for _, name := range names {
		data, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}
