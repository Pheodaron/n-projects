// Package migrations exposes the same Goose SQL migrations used by the CLI to
// integration tests. Production migration is an explicit mise task.
package migrations

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var Files embed.FS

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
