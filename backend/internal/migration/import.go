package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/legacy"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrTarget = errors.New("empty migrated rehearsal target in a *_test database required")
var ErrImport = errors.New("migration rehearsal failed; transaction rolled back")

type Report struct {
	Blockers        []string            `json:"cutover_blockers"`
	Withheld        map[string]int64    `json:"withheld_rows"`
	Imported        map[string]int64    `json:"imported"`
	Archived        map[string]int64    `json:"archived_only"`
	ExtraColumns    map[string][]string `json:"archived_extra_columns"`
	VerifiedFiles   int                 `json:"verified_files"`
	Reused          bool                `json:"reused"`
	ReadyForCutover bool                `json:"ready_for_cutover"`
}

var order = []string{"roles", "admins", "users", "admin_telegram_links", "settings", "taxonomy", "files", "contents", "content_revisions", "content_topics", "advanced_contents", "advanced_revisions", "advanced_files", "content_templates", "telegram_media", "interaction_values", "university_books", "student_verifications", "tickets", "ticket_messages", "listings", "lost_items", "reports", "personal_items", "student_channels", "announcement_categories", "announcement_items", "announcement_subscriptions", "announcement_dispatches"}
var jsonColumns = map[string]bool{"roles.permissions": true, "settings.value": true, "contents.metadata": true, "content_revisions.snapshot": true, "advanced_contents.document": true, "advanced_revisions.document": true, "content_templates.document": true, "university_books.details": true}

type column struct {
	name, kind         string
	required, identity bool
}

func Import(ctx context.Context, pool *pgxpool.Pool, dir string) (report Report, err error) {
	report = Report{Blockers: []string{"rehearsal_only", "runtime_parity_and_source_queue_review_required", "private_files_runtime_activation_not_performed", "original_app_key_and_restore_rehearsal_required"}, Withheld: map[string]int64{}, Imported: map[string]int64{}, Archived: map[string]int64{}, ExtraColumns: map[string][]string{}}
	if !strings.HasSuffix(pool.Config().ConnConfig.Database, "_test") {
		return report, ErrTarget
	}
	if postgres.Ready(ctx, pool) != nil {
		return report, ErrTarget
	}
	source, e := openSnapshot(dir)
	if e != nil {
		return report, e
	}
	var config struct {
		Key string `json:"app_key"`
	}
	if json.Unmarshal(source.config, &config) != nil {
		return report, ErrSnapshot
	}
	crypt, e := legacy.New(config.Key)
	if e != nil {
		return report, ErrSnapshot
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		return report, ErrImport
	}
	defer tx.Rollback(context.Background())
	defer func() {
		if err != nil && !errors.Is(err, ErrSnapshot) && !errors.Is(err, ErrTarget) {
			err = ErrImport
		}
	}()
	var database, schema string
	if e = tx.QueryRow(ctx, `SELECT current_database(),current_schema()`).Scan(&database, &schema); e != nil || database != pool.Config().ConnConfig.Database || schema == "" || schema == "public" || strings.HasPrefix(schema, "pg_") {
		return report, ErrTarget
	}
	if _, e = tx.Exec(ctx, `SELECT set_config('search_path',$1,true)`, pgx.Identifier{schema}.Sanitize()+",pg_temp"); e != nil {
		return report, ErrTarget
	}
	required := append(append([]string{}, order...), "schema_migrations", "runtime_metadata", "queue_control", "catalog_generations", "work_jobs", "admin_sessions", "admin_access_grants", "auth_attempts", "telegram_login_replays", "message_actions", "catalog_commands", "telegram_send_limits", "student_limits")
	var found int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind='r' AND c.relname=ANY($2)`, schema, required).Scan(&found); e != nil || found != len(required) {
		return report, ErrTarget
	}
	if postgres.ReadyTx(ctx, tx) != nil {
		return report, ErrTarget
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(674102291)`); e != nil {
		return report, e
	}
	_, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS migration_import_runs(snapshot_hash text PRIMARY KEY,config text NOT NULL,report text NOT NULL);CREATE TABLE IF NOT EXISTS migration_import_tables(source_table text PRIMARY KEY,ddl text NOT NULL,row_count bigint NOT NULL);CREATE TABLE IF NOT EXISTS migration_import_rows(source_table text NOT NULL REFERENCES migration_import_tables(source_table),row_number bigint NOT NULL,cells text NOT NULL,PRIMARY KEY(source_table,row_number));REVOKE ALL ON migration_import_runs,migration_import_tables,migration_import_rows FROM PUBLIC;DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='unibot_app') THEN REVOKE ALL ON migration_import_runs,migration_import_tables,migration_import_rows FROM unibot_app;END IF;END $$;`)
	if e != nil {
		return report, e
	}
	var previous string
	e = tx.QueryRow(ctx, `SELECT report FROM migration_import_runs WHERE snapshot_hash=$1`, source.hash).Scan(&previous)
	if e == nil {
		if json.Unmarshal([]byte(previous), &report) != nil {
			return report, ErrImport
		}
		report.Reused = true
		return report, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return report, e
	}
	if e = emptyTarget(ctx, tx); e != nil {
		return report, e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM announcement_categories`); e != nil {
		return report, e
	}
	// The entire original snapshot is preserved, including inactive sessions and unmapped domains.
	batch := [][]any{}
	batchBytes := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, e := tx.CopyFrom(ctx, pgx.Identifier{"migration_import_rows"}, []string{"source_table", "row_number", "cells"}, pgx.CopyFromRows(batch))
		batch = nil
		batchBytes = 0
		return e
	}
	e = source.walk(func(r record, row int64) error {
		if r.DDL != nil {
			if e := flush(); e != nil {
				return e
			}
			_, e := tx.Exec(ctx, `INSERT INTO migration_import_tables VALUES($1,$2,$3)`, r.Table, *r.DDL, source.manifest.Tables[r.Table])
			return e
		}
		if r.Row != nil {
			batch = append(batch, []any{r.Table, row, string(r.Row)})
			batchBytes += len(r.Row)
			if len(batch) >= 500 || batchBytes >= 8<<20 {
				return flush()
			}
		}
		return nil
	})
	if e != nil {
		return report, e
	}
	if e = flush(); e != nil {
		return report, e
	}
	active := map[string]bool{}
	for _, table := range order {
		active[table] = true
		if _, present := source.manifest.Tables[table]; !present {
			continue
		}
		n, extra, e := importTable(ctx, tx, table, crypt, source)
		if e != nil {
			return report, e
		}
		report.Imported[table] = n
		if n < source.manifest.Tables[table] {
			report.Withheld[table] = source.manifest.Tables[table] - n
		}
		if len(extra) > 0 {
			report.ExtraColumns[table] = extra
		}
	}
	for table, count := range source.manifest.Tables {
		if !active[table] {
			report.Archived[table] = count
		}
	}
	// A new Go session/keyboard is required; source state still exists in the private archive.
	if _, e = tx.Exec(ctx, `UPDATE settings SET value='"off"',is_secret=false WHERE name='bot_state';INSERT INTO settings(name,value,is_secret,updated_at) VALUES('bot_state','"off"',false,0) ON CONFLICT(name) DO NOTHING;UPDATE queue_control SET paused=true;UPDATE catalog_generations SET revision=revision+1`); e != nil {
		return report, e
	}
	if e = repairTaxonomy(ctx, tx); e != nil {
		return report, e
	}
	if e = sequences(ctx, tx); e != nil {
		return report, e
	}
	report.VerifiedFiles = len(source.manifest.Files) - 2
	raw, _ := json.Marshal(report)
	if _, e = tx.Exec(ctx, `INSERT INTO migration_import_runs VALUES($1,$2,$3)`, source.hash, string(source.config), string(raw)); e != nil {
		return report, e
	}
	if e = tx.Commit(ctx); e != nil {
		return report, e
	}
	return report, nil
}
func emptyTarget(ctx context.Context, tx pgx.Tx) error {
	rows, e := tx.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE' ORDER BY table_name`)
	if e != nil {
		return e
	}
	tables := []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			rows.Close()
			return e
		}
		tables = append(tables, name)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// Lock all active/operational tables until commit so a concurrent writer cannot enter the rehearsal.
	locked := []string{}
	for _, table := range tables {
		locked = append(locked, pgx.Identifier{table}.Sanitize())
	}
	if _, e = tx.Exec(ctx, "LOCK TABLE "+strings.Join(locked, ",")+" IN ACCESS EXCLUSIVE MODE"); e != nil {
		return e
	}
	for _, table := range tables {
		if table == "schema_migrations" || table == "runtime_metadata" || table == "queue_control" {
			continue
		}
		var count int64
		if table == "catalog_generations" {
			e = tx.QueryRow(ctx, `SELECT count(*) FROM catalog_generations WHERE scope NOT IN ('contents','books','leisure') OR revision<>1`).Scan(&count)
		} else if table == "announcement_categories" {
			e = tx.QueryRow(ctx, `SELECT count(*) FROM announcement_categories WHERE NOT ((id=1 AND name='فرهنگی' OR id=2 AND name='کاری' OR id=3 AND name='تکنولوژی') AND active AND created_at=0 AND revision=1)`).Scan(&count)
		} else {
			e = tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count)
		}
		if e != nil {
			return e
		}
		if count != 0 {
			return ErrTarget
		}
	}

	return nil
}
func columns(ctx context.Context, tx pgx.Tx, table string) ([]column, error) {
	rows, e := tx.Query(ctx, `SELECT a.attname,t.typname,a.attnotnull AND NOT a.atthasdef,a.attidentity<>'' FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid WHERE a.attrelid=to_regclass($1) AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`, pgx.Identifier{table}.Sanitize())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []column{}
	for rows.Next() {
		var c column
		if e = rows.Scan(&c.name, &c.kind, &c.required, &c.identity); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func converted(c column, value *string) (any, error) {
	if value == nil {
		return nil, nil
	}
	v := *value
	switch c.kind {
	case "bool":
		if v == "0" {
			return false, nil
		}
		if v == "1" {
			return true, nil
		}
		return nil, ErrSnapshot
	case "int2", "int4", "int8":
		bits := 64
		if c.kind == "int2" {
			bits = 16
		}
		if c.kind == "int4" {
			bits = 32
		}
		n, e := strconv.ParseInt(v, 10, bits)
		if e != nil || c.identity && n <= 0 {
			return nil, ErrSnapshot
		}
		return n, nil
	case "date":
		d, e := time.Parse("2006-01-02", v)
		if e != nil {
			return nil, ErrSnapshot
		}
		return d, nil
	case "text", "varchar", "bpchar":
		if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return nil, ErrSnapshot
		}
		return v, nil
	default:
		return nil, ErrSnapshot
	}
}
func importTable(ctx context.Context, tx pgx.Tx, table string, crypt *legacy.Crypto, source snapshot) (int64, []string, error) {
	cols, e := columns(ctx, tx, table)
	if e != nil {
		return 0, nil, e
	}
	var cursor, total int64
	extras := map[string]bool{}
	for {
		rows, e := tx.Query(ctx, `SELECT row_number,cells FROM migration_import_rows WHERE source_table=$1 AND row_number>$2 ORDER BY row_number LIMIT 100`, table, cursor)
		if e != nil {
			return 0, nil, e
		}
		type item struct {
			raw   string
			index int64
		}
		items := []item{}
		for rows.Next() {
			var i item
			if e = rows.Scan(&i.index, &i.raw); e != nil {
				rows.Close()
				return 0, nil, e
			}
			items = append(items, i)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return 0, nil, e
		}
		if len(items) == 0 {
			break
		}
		// Rows from a table must expose the same source column set; missing destination defaults remain defaults.
		var chosen []column
		var names []string
		values := [][]any{}
		for _, i := range items {
			cursor = i.index
			row, e := cells([]byte(i.raw))
			if e != nil {
				return 0, nil, e
			}
			if table == "settings" {
				name := cell(row, "name")
				if retiredSetting(name) {
					continue
				}
				if cell(row, "is_secret") == "1" {
					if _, e = crypt.Decrypt(cell(row, "value")); e != nil {
						return 0, nil, ErrSnapshot
					}
				}
			}
			if e = validateOpaque(table, row, crypt, source); e != nil {
				return 0, nil, e
			}
			targetNames := map[string]bool{}
			current := []column{}
			for _, c := range cols {
				targetNames[c.name] = true
				_, present := row[c.name]
				derived := table == "contents" && c.name == "search_text"
				if c.required && !present && !derived {
					return 0, nil, ErrSnapshot
				}
				if c.identity && !present {
					return 0, nil, ErrSnapshot
				}
				if present || derived {
					current = append(current, c)
				}
			}
			for name := range row {
				if !targetNames[name] {
					extras[name] = true
				}
			}
			if chosen == nil {
				chosen = current
				for _, c := range chosen {
					names = append(names, c.name)
				}
			} else {
				if len(chosen) != len(current) {
					return 0, nil, ErrSnapshot
				}
				for j := range chosen {
					if chosen[j].name != current[j].name {
						return 0, nil, ErrSnapshot
					}
				}
			}
			out := []any{}
			for _, c := range chosen {
				value := row[c.name]
				if table == "contents" && c.name == "search_text" {
					v := catalog.Normalize(cell(row, "title") + " " + cell(row, "body") + " " + cell(row, "metadata"))
					value = &v
				}
				if table == "users" && c.name == "conversation" || table == "taxonomy" && c.name == "parent_id" || table == "admin_telegram_links" && (c.name == "challenge_hash" || c.name == "expires_at") {
					value = nil
				}
				if table == "announcement_dispatches" && c.name == "state" {
					v := "cancelled"
					value = &v
				}
				if table == "settings" && cell(row, "name") == "bot_state" && c.name == "value" {
					v := `"off"`
					value = &v
				}
				if jsonColumns[table+"."+c.name] && value != nil && !(table == "settings" && cell(row, "is_secret") == "1") {
					if !json.Valid([]byte(*value)) {
						return 0, nil, ErrSnapshot
					}
				}
				v, e := converted(c, value)
				if e != nil {
					return 0, nil, e
				}
				out = append(out, v)
			}
			values = append(values, out)
		}
		if len(values) > 0 {
			n, e := tx.CopyFrom(ctx, pgx.Identifier{table}, names, pgx.CopyFromRows(values))
			if e != nil {
				return 0, nil, e
			}
			total += n
		}
	}
	list := []string{}
	for key := range extras {
		list = append(list, key)
	}
	sort.Strings(list)
	return total, list, nil
}
func cell(row map[string]*string, name string) string {
	if row[name] == nil {
		return ""
	}
	return *row[name]
}
func retiredSetting(name string) bool {
	return name == "telegram_token" || name == "bot_info" || strings.Contains(name, "webhook") || strings.HasPrefix(name, "admin_access_") || name == "telegram_admin_roster"
}
func validateOpaque(table string, row map[string]*string, crypt *legacy.Crypto, source snapshot) error {
	if table == "admins" {
		for _, name := range []string{"totp_secret", "recovery_codes"} {
			if row[name] != nil && cell(row, name) != "" {
				if _, e := crypt.Decrypt(cell(row, name)); e != nil {
					return ErrSnapshot
				}
			}
		}
	}
	if table == "student_verifications" {
		if _, e := crypt.Decrypt(cell(row, "details")); e != nil {
			return ErrSnapshot
		}
	}
	if table == "files" {
		name := cell(row, "storage_name")
		if len(name) != 64 || strings.Trim(name, "0123456789abcdef") != "" {
			return ErrSnapshot
		}
		expected, ok := source.manifest.Files["uploads/"+name]
		size, e := strconv.ParseInt(cell(row, "size"), 10, 64)
		if !ok || e != nil || size != expected.Bytes || cell(row, "checksum") != expected.SHA {
			return ErrSnapshot
		}
	}
	return nil
}
func repairTaxonomy(ctx context.Context, tx pgx.Tx) error {
	rows, e := tx.Query(ctx, `SELECT cells FROM migration_import_rows WHERE source_table='taxonomy' ORDER BY row_number`)
	if e != nil {
		return e
	}
	pairs := [][]int64{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		r, e := cells([]byte(raw))
		if e != nil {
			rows.Close()
			return e
		}
		if r["parent_id"] == nil {
			continue
		}
		id, e := strconv.ParseInt(cell(r, "id"), 10, 64)
		if e != nil {
			rows.Close()
			return ErrSnapshot
		}
		parent, e := strconv.ParseInt(cell(r, "parent_id"), 10, 64)
		if e != nil {
			rows.Close()
			return ErrSnapshot
		}
		pairs = append(pairs, []int64{id, parent})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(pairs) == 0 {
		return nil
	}
	ids, parents := []int64{}, []int64{}
	for _, p := range pairs {
		ids = append(ids, p[0])
		parents = append(parents, p[1])
	}
	_, e = tx.Exec(ctx, `UPDATE taxonomy t SET parent_id=p.parent FROM unnest($1::bigint[],$2::bigint[]) p(id,parent) WHERE t.id=p.id`, ids, parents)
	if e != nil {
		return e
	}
	var cycle bool
	e = tx.QueryRow(ctx, `WITH RECURSIVE walk AS (SELECT id,parent_id,ARRAY[id] AS path,false AS cycle FROM taxonomy UNION ALL SELECT w.id,t.parent_id,w.path||t.id,t.id=ANY(w.path) FROM walk w JOIN taxonomy t ON t.id=w.parent_id WHERE NOT w.cycle) SELECT EXISTS(SELECT 1 FROM walk WHERE cycle)`).Scan(&cycle)
	if e == nil && cycle {
		return ErrSnapshot
	}
	return e
}
func sequences(ctx context.Context, tx pgx.Tx) error {
	for _, table := range order {
		cols, e := columns(ctx, tx, table)
		if e != nil {
			return e
		}
		for _, c := range cols {
			if !c.identity {
				continue
			}
			var seq *string
			if e = tx.QueryRow(ctx, `SELECT pg_get_serial_sequence($1,$2)`, pgx.Identifier{table}.Sanitize(), c.name).Scan(&seq); e != nil {
				return e
			}
			if seq == nil {
				continue
			}
			var maxID int64
			if e = tx.QueryRow(ctx, fmt.Sprintf(`SELECT COALESCE(MAX(%s),0) FROM %s`, pgx.Identifier{c.name}.Sanitize(), pgx.Identifier{table}.Sanitize())).Scan(&maxID); e != nil {
				return e
			}
			if maxID == 9223372036854775807 {
				return ErrSnapshot
			}

			next := maxID + 1
			if _, e = tx.Exec(ctx, fmt.Sprintf(`ALTER SEQUENCE %s RESTART WITH %d`, *seq, next)); e != nil {
				return e
			}
		}
	}
	return nil
}
