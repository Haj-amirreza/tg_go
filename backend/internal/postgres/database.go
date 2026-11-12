package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, url string, max int32) (*pgxpool.Pool, error) {
	c, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	c.ConnConfig.Tracer = tracer{}
	c.MaxConns = max
	c.MinConns = 0
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return nil, errors.New("database initialization failed")
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, errors.New("database unavailable")
	}
	return p, nil
}

type migrationReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func Ready(ctx context.Context, p *pgxpool.Pool) error { return ready(ctx, p) }

// ReadyTx verifies migration checksums in the caller's pinned schema/transaction.
func ReadyTx(ctx context.Context, tx pgx.Tx) error { return ready(ctx, tx) }
func ready(ctx context.Context, p migrationReader) error {
	rows, err := p.Query(ctx, "SELECT version,checksum FROM schema_migrations")
	if err != nil {
		return errors.New("migration required")
	}
	defer rows.Close()
	applied := map[string]string{}
	for rows.Next() {
		var version, checksum string
		if rows.Scan(&version, &checksum) != nil {
			return errors.New("migration registry unavailable")
		}
		applied[version] = checksum
	}
	if rows.Err() != nil {
		return errors.New("migration registry unavailable")
	}
	files, _ := migrations.ReadDir("migrations")
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		checksum := applied[file.Name()]
		data, _ := migrations.ReadFile("migrations/" + file.Name())
		sum := sha256.Sum256(data)
		if checksum != hex.EncodeToString(sum[:]) {
			return errors.New("migration checksum mismatch")
		}
	}
	return nil
}
func Migrate(ctx context.Context, p *pgxpool.Pool) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return errors.New("migration transaction failed")
	}
	defer tx.Rollback(context.Background())
	// Migration-only lock; no global lock will serialize runtime worker processing.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(764629182)"); err != nil {
		return errors.New("migration lock failed")
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version text PRIMARY KEY,checksum char(64) NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return errors.New("migration registry failed")
	}
	files, _ := migrations.ReadDir("migrations")
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		data, _ := migrations.ReadFile("migrations/" + file.Name())
		hash := sha256.Sum256(data)
		checksum := hex.EncodeToString(hash[:])
		var old string
		err = tx.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE version=$1", file.Name()).Scan(&old)
		if err == nil {
			if old != checksum {
				return fmt.Errorf("migration checksum mismatch: %s", file.Name())
			}
			continue
		}
		// A registry read error other than a missing row is not treated as a new migration.
		if !errors.Is(err, pgx.ErrNoRows) {
			return errors.New("migration registry read failed")
		}
		if _, err = tx.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("migration failed: %s", file.Name())
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES ($1,$2)", file.Name(), checksum); err != nil {
			return errors.New("migration registry write failed")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("migration commit failed")
	}
	return nil
}
