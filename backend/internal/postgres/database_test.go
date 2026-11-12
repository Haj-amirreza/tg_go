package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMigrations(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("use a disposable *_test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database connection failed")
	}
	defer owner.Close()
	seed := make([]byte, 6)
	rand.Read(seed)
	schema := "foundation_" + hex.EncodeToString(seed)
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = owner.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer owner.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
	scoped, _ := pgxpool.ParseConfig(url)
	scoped.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, scoped)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if Ready(ctx, pool) == nil {
		t.Fatal("unmigrated DB ready")
	}
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal("replay not idempotent", err)
	}
	if err = Ready(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	files, _ := migrations.ReadDir("migrations")
	if count != len(files) {
		t.Fatal("migration duplicated")
	}
	var stage int
	if err = pool.QueryRow(ctx, "SELECT (value->>'stage')::int FROM runtime_metadata WHERE name='migration_stage'").Scan(&stage); err != nil || stage != 4 {
		t.Fatal("bootstrap data invalid")
	}
	pool.Exec(ctx, "UPDATE schema_migrations SET checksum=repeat('0',64)")
	if Migrate(ctx, pool) == nil || Ready(ctx, pool) == nil {
		t.Fatal("checksum tampering accepted")
	}
}
