// import-snapshot is an offline rehearsal command. It cannot target a production database.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Haj-amirreza/tg_go/backend/internal/migration"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var schemaName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,99}$`)

type options struct {
	snapshot, schema, databaseEnv string
	apply                         bool
	timeout                       time.Duration
}

func parse(args []string, stderr io.Writer) (options, int) {
	var o options
	flags := flag.NewFlagSet("import-snapshot", flag.ContinueOnError)
	var diagnostics bytes.Buffer
	flags.SetOutput(&diagnostics)
	flags.StringVar(&o.snapshot, "snapshot", "", "private prepare.php snapshot directory")
	flags.StringVar(&o.schema, "schema", "", "existing empty, migrated PostgreSQL rehearsal schema")
	flags.StringVar(&o.databaseEnv, "database-env", "TEST_DATABASE_URL", "environment variable containing destination DSN (database must end in _test)")
	flags.BoolVar(&o.apply, "apply", false, "explicitly import into the rehearsal schema; never enables production delivery")
	flags.DurationVar(&o.timeout, "timeout", 15*time.Minute, "overall import deadline (maximum 1h)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, _ = io.Copy(stderr, &diagnostics)
			return o, 0
		}
		fmt.Fprintln(stderr, "Invalid arguments; use -help. No target changed.")
		return o, 2
	}
	if flags.NArg() != 0 || o.snapshot == "" || !schemaName.MatchString(o.schema) || strings.HasPrefix(o.schema, "pg_") || (o.schema == "information_schema" || o.schema == "public") || !envName.MatchString(o.databaseEnv) || o.timeout < time.Second || o.timeout > time.Hour {
		fmt.Fprintln(stderr, "Provide -snapshot, an explicit safe -schema, and a valid destination environment variable. No target changed.")
		return o, 2
	}
	if !o.apply {
		fmt.Fprintln(stderr, "No import performed. Use -apply only with an empty migrated rehearsal schema in a *_test database.")
		return o, 2
	}
	return o, -1
}

// targetConfig validates the destination before opening a connection or exposing a DSN.
func targetConfig(dsn, schema string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || dsn == "" || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") || !schemaName.MatchString(schema) || strings.HasPrefix(schema, "pg_") || (schema == "information_schema" || schema == "public") {
		return nil, fmt.Errorf("an explicit *_test database and safe schema are required")
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize()
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.MaxConns = 2
	cfg.MinConns = 0
	return cfg, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	o, status := parse(args, stderr)
	if status >= 0 {
		return status
	}
	cfg, err := targetConfig(os.Getenv(o.databaseEnv), o.schema)
	if err != nil {
		fmt.Fprintln(stderr, "Destination rejected: an explicit *_test database and safe schema are required. No target changed.")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "Destination unavailable; inspect connection settings privately.")
		return 1
	}
	defer pool.Close()
	var exists bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, o.schema).Scan(&exists); err != nil || !exists {
		fmt.Fprintln(stderr, "Destination schema unavailable; create and migrate a dedicated rehearsal schema first. No import performed.")
		return 1
	}
	if postgres.Ready(ctx, pool) != nil {
		fmt.Fprintln(stderr, "Destination migrations are missing or changed. No import performed.")
		return 1
	}
	report, err := migration.Import(ctx, pool, o.snapshot)
	if err != nil {
		fmt.Fprintln(stderr, "Import failed; no successful import is claimed. Inspect snapshot integrity, schema compatibility and empty-target requirements privately.")
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if encoder.Encode(report) != nil {
		fmt.Fprintln(stderr, "Import finished but report output failed; inspect the import registry before retrying.")
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
