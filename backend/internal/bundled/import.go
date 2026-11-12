// Package bundled imports only the versioned catalogues shipped with this release.
// It does not migrate a deployed PHP database or enable Telegram delivery.
package bundled

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/legacy"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrSource = errors.New("bundled catalogue files do not match this release")
var ErrTarget = errors.New("bundled import requires the owner and an empty business database, or an identical completed import")
var ErrImport = errors.New("bundled import failed; transaction rolled back")

// SourceSHA256 pins the PHP-generated publication decisions, including withheld fields.
const SourceSHA256 = "3d2df10785d398bf7631a389de10fd4d3b1ac785c7bc940d3287010d420321fe"

// Paths are explicit offline files; no network, private PHP config or source database is used.
type Paths struct{ Contacts, Books, RawContacts string }
type Report struct {
	Books     int64 `json:"books"`
	Contents  int64 `json:"contents"`
	Published int64 `json:"published"`
	Draft     int64 `json:"draft"`
	Archived  int64 `json:"archived_sources"`
	Reused    bool  `json:"reused"`
}
type manifest struct {
	Type        string           `json:"type"`
	Version     int              `json:"version"`
	ContactHash string           `json:"contact_source_sha256"`
	BookHash    string           `json:"book_source_sha256"`
	Counts      map[string]int64 `json:"counts"`
}
type record struct {
	Type string             `json:"type"`
	Row  map[string]*string `json:"row"`
}

// Run imports the entire bundle atomically and never overwrites an existing import.
// An identical replay preserves subsequent administrator edits and activation state.
func Run(ctx context.Context, pool *pgxpool.Pool, appKey string, paths Paths) (report Report, err error) {
	crypt, e := legacy.New(appKey)
	if e != nil {
		return report, ErrSource
	}
	safe, e := readBounded(paths.Contacts, 8<<20)
	if e != nil || digest(safe) != SourceSHA256 {
		return report, ErrSource
	}
	var header manifest
	if e = walk(safe, func(line []byte, n int) error {
		if n == 0 {
			return json.Unmarshal(line, &header)
		}
		return nil
	}); e != nil || header.Type != "manifest" || header.Version != 1 || header.Counts["contents"] != 670 || header.Counts["published"] != 55 || header.Counts["draft"] != 615 {
		return report, ErrSource
	}
	raw, e := readBounded(paths.RawContacts, 8<<20)
	if e != nil || digest(raw) != header.ContactHash || !json.Valid(raw) {
		return report, ErrSource
	}
	books, e := readBounded(paths.Books, 8<<20)
	if e != nil || digest(books) != header.BookHash {
		return report, ErrSource
	}
	encrypted, e := crypt.Encrypt(raw)
	if e != nil {
		return report, ErrImport
	}
	if postgres.Ready(ctx, pool) != nil {
		return report, ErrTarget
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		return report, ErrImport
	}
	defer tx.Rollback(context.Background())
	defer func() {
		if err != nil && !errors.Is(err, ErrTarget) {
			err = ErrImport
		}
	}()
	var schema string
	if e = tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); e != nil || schema == "" || strings.HasPrefix(schema, "pg_") {
		return report, ErrTarget
	}
	// Resolve real schema tables before pg_temp, even on a reused pooled connection.
	if _, e = tx.Exec(ctx, `SELECT set_config('search_path',$1,true)`, pgx.Identifier{schema}.Sanitize()+",pg_temp"); e != nil {
		return report, ErrTarget
	}
	if postgres.ReadyTx(ctx, tx) != nil {
		return report, ErrTarget
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(764629191)`); e != nil {
		return report, e
	}
	var owner bool
	if e = tx.QueryRow(ctx, `SELECT pg_has_role(current_user,c.relowner,'USAGE') FROM pg_class c WHERE c.oid='bundled_import_runs'::regclass`).Scan(&owner); e != nil || !owner {
		return report, ErrTarget
	}
	var previous string
	e = tx.QueryRow(ctx, `SELECT report FROM bundled_import_runs WHERE source_sha256=$1`, SourceSHA256).Scan(&previous)
	if e == nil {
		if json.Unmarshal([]byte(previous), &report) != nil {
			return report, ErrImport
		}
		// Verify that the current APP_KEY can still open the completed source archive.
		var archive string
		if e = tx.QueryRow(ctx, `SELECT payload_enc FROM bundled_source_archive WHERE source_sha256=$1`, SourceSHA256).Scan(&archive); e != nil {
			return report, e
		}
		clear, e := crypt.Decrypt(archive)
		if e != nil || digest(clear) != header.ContactHash {
			return report, ErrTarget
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
	if e = copyContacts(ctx, tx, safe, header); e != nil {
		return report, e
	}
	if report.Books, e = copyBooks(ctx, tx, books); e != nil || report.Books != 7991 {
		return report, ErrImport
	}
	if e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE status='published'),count(*) FILTER(WHERE status='draft') FROM contents`).Scan(&report.Contents, &report.Published, &report.Draft); e != nil || report.Contents != 670 || report.Published != 55 || report.Draft != 615 {
		return report, ErrImport
	}
	report.Archived = 1
	encoded, _ := json.Marshal(report)
	if _, e = tx.Exec(ctx, `INSERT INTO bundled_import_runs(source_sha256,contact_sha256,book_sha256,report) VALUES($1,$2,$3,$4)`, SourceSHA256, header.ContactHash, header.BookHash, string(encoded)); e != nil {
		return report, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO bundled_source_archive VALUES($1,'bkatu_contacts_full.json',$2,$3)`, SourceSHA256, header.ContactHash, encrypted); e != nil {
		return report, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO settings(name,value,is_secret,updated_at) VALUES('bot_state','"off"',false,0) ON CONFLICT(name) DO UPDATE SET value='"off"',is_secret=false; UPDATE queue_control SET paused=true; UPDATE catalog_generations SET revision=revision+1 WHERE scope IN ('books','contents')`); e != nil {
		return report, e
	}
	marker, _ := json.Marshal(map[string]any{"version": 1, "ready": true, "source_sha256": SourceSHA256, "counts": map[string]int64{"university_books": 7991, "contents": 670, "published": 55, "draft": 615}})
	if _, e = tx.Exec(ctx, `INSERT INTO runtime_metadata(name,value) VALUES('bundled_data_import',$1::jsonb) ON CONFLICT(name) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, string(marker)); e != nil {
		return report, e
	}
	for _, table := range []string{"taxonomy", "contents", "content_revisions", "university_books"} {
		var seq string
		var next int64
		if e = tx.QueryRow(ctx, `SELECT pg_get_serial_sequence($1,'id')`, pgx.Identifier{schema, table}.Sanitize()).Scan(&seq); e != nil {
			return report, e
		}
		if e = tx.QueryRow(ctx, `SELECT COALESCE(max(id),0)+1 FROM `+pgx.Identifier{schema, table}.Sanitize()).Scan(&next); e != nil {
			return report, e
		}
		if _, e = tx.Exec(ctx, `ALTER SEQUENCE `+seq+` RESTART WITH `+strconv.FormatInt(next, 10)); e != nil {
			return report, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return report, e
	}
	return report, nil
}

// All mutable business tables must be empty. Bootstrap identities, login state,
// settings and the three built-in announcement categories may already exist.
func emptyTarget(ctx context.Context, tx pgx.Tx) error {
	tables := []string{"bundled_import_runs", "bundled_source_archive", "users", "taxonomy", "files", "contents", "content_revisions", "content_topics", "university_books", "student_verifications", "catalog_commands", "tickets", "ticket_messages", "student_channels", "personal_items", "listings", "lost_items", "reports", "student_limits", "announcement_items", "announcement_subscriptions", "announcement_dispatches", "advanced_contents", "advanced_revisions", "advanced_files", "content_templates", "interaction_values", "message_actions", "telegram_media", "telegram_send_limits", "work_jobs", "telegram_updates"}
	if _, e := tx.Exec(ctx, `LOCK TABLE `+strings.Join(tables, ",")+` IN ACCESS EXCLUSIVE MODE`); e != nil {
		return e
	}
	for _, table := range tables {
		var nonempty bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+`)`).Scan(&nonempty); e != nil {
			return e
		} else if nonempty {
			return ErrTarget
		}
	}
	var custom bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM announcement_categories WHERE name NOT IN ('فرهنگی','کاری','تکنولوژی') OR NOT active OR revision<>1 OR created_at<>0)`).Scan(&custom); e != nil {
		return e
	} else if custom {
		return ErrTarget
	}
	// A previous full-snapshot rehearsal cannot become a production bundled import.
	var archive *string
	if e := tx.QueryRow(ctx, `SELECT to_regclass('migration_import_runs')::text`).Scan(&archive); e != nil {
		return e
	}
	if archive != nil {
		return ErrTarget
	}
	return nil
}
func readBounded(path string, max int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, ErrSource
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) > max {
		return nil, ErrSource
	}
	return b, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func walk(data []byte, fn func([]byte, int) error) error {
	gz, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return e
	}
	defer gz.Close()
	scan := bufio.NewScanner(io.LimitReader(gz, 64<<20+1))
	scan.Buffer(make([]byte, 64<<10), 2<<20)
	n, total := 0, 0
	for scan.Scan() {
		total += len(scan.Bytes()) + 1
		if total > 64<<20 || n > 20000 {
			return ErrSource
		}
		if e = fn(scan.Bytes(), n); e != nil {
			return e
		}
		n++
	}
	if n == 0 {
		return ErrSource
	}
	return scan.Err()
}

type field struct{ name, kind string }

var specs = map[string][]field{
	"taxonomy":          {{"id", "int"}, {"kind", "text"}, {"name", "text"}, {"parent_id", "int"}, {"sort_order", "int"}},
	"contents":          {{"id", "int"}, {"kind", "text"}, {"title", "text"}, {"body", "text"}, {"metadata", "text"}, {"topic_id", "int"}, {"category_id", "int"}, {"file_id", "int"}, {"source_url", "text"}, {"license", "text"}, {"status", "text"}, {"priority", "int"}, {"pinned", "bool"}, {"published_at", "int"}, {"expires_at", "int"}, {"version", "int"}, {"reviewed_by", "int"}, {"created_at", "int"}, {"updated_at", "int"}},
	"content_revisions": {{"id", "int"}, {"content_id", "int"}, {"version", "int"}, {"snapshot", "text"}, {"admin_id", "int"}, {"created_at", "int"}},
	"content_topics":    {{"content_id", "int"}, {"topic_id", "int"}},
}

func copyContacts(ctx context.Context, tx pgx.Tx, data []byte, header manifest) error {
	counts := map[string]int64{}
	current := ""
	var batch [][]any
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		cols := []string{}
		for _, f := range specs[current] {
			cols = append(cols, f.name)
		}
		if current == "contents" {
			cols = append(cols, "search_text")
		}
		_, e := tx.CopyFrom(ctx, pgx.Identifier{current}, cols, pgx.CopyFromRows(batch))
		batch = nil
		size = 0
		return e
	}
	e := walk(data, func(line []byte, n int) error {
		if n == 0 {
			return nil
		}
		var r record
		if json.Unmarshal(line, &r) != nil {
			return ErrSource
		}
		spec, ok := specs[r.Type]
		if !ok || len(r.Row) != len(spec) {
			return ErrSource
		}
		if current != r.Type {
			if e := flush(); e != nil {
				return e
			}
			current = r.Type
		}
		row := []any{}
		for _, f := range spec {
			p, ok := r.Row[f.name]
			if !ok {
				return ErrSource
			}
			if p == nil {
				row = append(row, nil)
				continue
			}
			switch f.kind {
			case "int":
				v, e := strconv.ParseInt(*p, 10, 64)
				if e != nil {
					return ErrSource
				}
				row = append(row, v)
			case "bool":
				if *p != "0" && *p != "1" {
					return ErrSource
				}
				row = append(row, *p == "1")
			default:
				row = append(row, *p)
			}
		}
		if r.Type == "contents" {
			if r.Row["title"] == nil || r.Row["body"] == nil || r.Row["metadata"] == nil || !json.Valid([]byte(*r.Row["metadata"])) {
				return ErrSource
			}
			row = append(row, catalog.Normalize(*r.Row["title"]+" "+*r.Row["body"]+" "+*r.Row["metadata"]))
		}
		batch = append(batch, row)
		size += len(line)
		counts[r.Type]++
		if len(batch) >= 500 || size >= 2<<20 {
			return flush()
		}
		return nil
	})
	if e != nil {
		return e
	}
	if e = flush(); e != nil {
		return e
	}
	for table := range specs {
		if counts[table] != header.Counts[table] {
			return ErrSource
		}
	}
	return nil
}
func copyBooks(ctx context.Context, tx pgx.Tx, data []byte) (int64, error) {
	var count int64
	batch := [][]any{}
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, e := tx.CopyFrom(ctx, pgx.Identifier{"university_books"}, []string{"id", "source_key", "title", "details", "search_text", "category"}, pgx.CopyFromRows(batch))
		batch = nil
		size = 0
		return e
	}
	e := walk(data, func(line []byte, _ int) error {
		var in struct {
			Key     string         `json:"key"`
			Title   string         `json:"title"`
			Details map[string]any `json:"details"`
		}
		if json.Unmarshal(line, &in) != nil || len(in.Key) != 64 || !catalog.Text(in.Title, 2000) || in.Details == nil {
			return ErrSource
		}
		raw, e := json.Marshal(in.Details)
		if e != nil {
			return e
		}
		count++
		// PHP's original seed assigned IDs in this exact line order.
		text := in.Title + " " + string(raw)
		batch = append(batch, []any{count, in.Key, in.Title, string(raw), catalog.Normalize(text), catalog.Classify(text)})
		size += len(line)
		if len(batch) >= 500 || size >= 2<<20 {
			return flush()
		}
		return nil
	})
	if e != nil {
		return count, e
	}
	return count, flush()
}
