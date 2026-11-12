// Package testdb provisions only disposable schemas within an explicitly named *_test database.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
)

func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("disposable *_test database required")
	}
	ctx := context.Background()
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("database unavailable")
	}
	seed := make([]byte, 8)
	if _, err = rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"domain_" + hex.EncodeToString(seed)}.Sanitize()
	if _, err = owner.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = strings.Trim(schema, "\"")
	cfg.ConnConfig.Tracer = postgres.QueryTracer()
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(); owner.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); owner.Close() })
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
