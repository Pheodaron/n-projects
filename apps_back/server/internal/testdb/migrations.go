// Package testdb applies the root Goose SQL migrations in integration tests.
package testdb

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Go tests run from internal/<package>; the shared SQL stays at repository root.
var Files = os.DirFS("../../../../migrations")

func Up(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, Files)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
