package testdb_test

import (
	"context"
	"crypto/rand"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/Pheodaron/n-projects/apps_back/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestAdoptExistingCoreAndRollback(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "migration_" + strings.ToLower(rand.Text())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	// Reproduce the schema left by the pre-Goose Core, without a Goose version table.
	sql, err := fs.ReadFile(testdb.Files, "00001_core.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, strings.Split(string(sql), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO sessions VALUES('existing','dev','dev','{"Title":"preserved"}')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = testdb.Up(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var title string
	if err = pool.QueryRow(ctx, `SELECT data->>'Title' FROM sessions WHERE id='existing'`).Scan(&title); err != nil || title != "preserved" {
		t.Fatalf("existing data: %s %v", title, err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, testdb.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	var missing bool
	if err = pool.QueryRow(ctx, `SELECT to_regclass('sessions') IS NULL AND to_regclass('runs') IS NULL AND to_regclass('receipts') IS NULL`).Scan(&missing); err != nil || !missing {
		t.Fatalf("down: %v %v", missing, err)
	}
	if err = testdb.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
}
