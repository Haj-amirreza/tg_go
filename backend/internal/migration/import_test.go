package migration

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Haj-amirreza/tg_go/backend/internal/legacy"
	"github.com/Haj-amirreza/tg_go/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const fixtureKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
const fixtureTelegram = "9007199254740993"
const fixtureDocument = "{\n  \"schema\":1, \"rich\":true, \"blocks\":[{\"type\":\"text\",\"enabled\":true,\"text\":\"<b>سلام دانشگاه</b>\"}]\n}"

type importFixture struct {
	dir                              string
	rows                             map[string][]map[string]any
	cipher, token, totp, permissions string
}

func fixture(t *testing.T) *importFixture {
	t.Helper()
	crypt, e := legacy.New(fixtureKey)
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := crypt.Encrypt([]byte(`{"full_name":"دانشجوی آزمایشی","phone":"+989120000001","student_number":"40012345"}`))
	if e != nil {
		t.Fatal(e)
	}
	token, e := crypt.Encrypt([]byte(`"synthetic-bot-token"`))
	if e != nil {
		t.Fatal(e)
	}
	totp, e := crypt.Encrypt([]byte("JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"))
	if e != nil {
		t.Fatal(e)
	}
	permissions := ` ["content", "publish", "tickets"] `
	content := []byte("synthetic private upload\x00\xff")
	sum := sha256.Sum256(content)
	rows := map[string][]map[string]any{
		"roles":                      {{"id": "41", "name": "مدیر آزمایشی", "permissions": permissions, "level": "20"}},
		"admins":                     {{"id": "71", "username": "migration_owner", "password_hash": "$2y$04$GrxL/GTc0OF3D26Aw1LYkOsEw3jBtu5V.avgCzi4A2wWtCrhgqGMm", "name": "مدیر نمونه", "role_id": "41", "is_super": "1", "active": "1", "auth_version": "3", "totp_secret": totp, "totp_last": "0", "recovery_codes": nil, "created_at": "100"}},
		"users":                      {{"id": "101", "telegram_id": fixtureTelegram, "first_name": "دانشجو", "last_name": "نمونه", "username": "sample_user", "status": "active", "delivery_blocked": "0", "interactions": "7", "tags": "دانشجو,نمونه", "notes": nil, "conversation": `{"support_chat":{"ticket":501},"sensitive":"old state"}`, "created_at": "100", "last_seen": "200"}},
		"admin_telegram_links":       {{"admin_id": "71", "telegram_id": fixtureTelegram, "challenge_hash": strings.Repeat("1", 64), "expires_at": "9999999999", "linked_at": "100"}},
		"settings":                   {{"name": "bot_state", "value": `"on"`, "is_secret": "0", "updated_at": "100"}, {"name": "telegram_token", "value": token, "is_secret": "1", "updated_at": "100"}, {"name": "welcome", "value": `"به دانشگاه خوش آمدید"`, "is_secret": "0", "updated_at": "100"}},
		"files":                      {{"id": "151", "original_name": "کتاب آزمایشی.pdf", "storage_name": strings.Repeat("a", 64), "mime": "application/pdf", "size": fmt.Sprint(len(content)), "checksum": hex.EncodeToString(sum[:]), "created_at": "100"}},
		"taxonomy":                   {{"id": "202", "kind": "major", "name": "رشته رایانه", "parent_id": "201", "sort_order": "1"}, {"id": "201", "kind": "faculty", "name": "دانشکده مهندسی", "parent_id": nil, "sort_order": "0"}},
		"contents":                   {{"id": "301", "kind": "announcements", "title": "اطلاعیه آزمون", "body": "متن فارسی با ک و ی", "metadata": `{"access":"public","audience_tag":""}`, "topic_id": "202", "category_id": nil, "file_id": "151", "source_url": "https://example.test/source", "license": "نمونه", "status": "published", "priority": "2", "pinned": "1", "published_at": "100", "expires_at": nil, "version": "2", "reviewed_by": "71", "created_at": "100", "updated_at": "200"}},
		"content_topics":             {{"content_id": "301", "topic_id": "202"}},
		"content_revisions":          {{"id": "351", "content_id": "301", "version": "2", "snapshot": "{ \"body\": \"نسخهٔ اصلی\" }", "admin_id": "71", "created_at": "200"}},
		"advanced_contents":          {{"content_id": "301", "document": fixtureDocument, "revision": "2", "updated_at": "200"}},
		"advanced_revisions":         {{"content_id": "301", "version": "2", "document": fixtureDocument, "created_at": "200"}},
		"advanced_files":             {{"content_id": "301", "file_id": "151"}},
		"university_books":           {{"id": "401", "source_key": strings.Repeat("b", 64), "title": "کتاب رایانه", "details": "{ \"نویسنده\": \"نمونه\", \"تعداد\": \"2\" }", "search_text": "کتاب رایانه نمونه", "category": "engineering", "deleted": "1"}},
		"tickets":                    {{"id": "501", "user_id": "101", "subject": "پرسش آزمایشی", "category": "عمومی", "priority": "normal", "state": "open", "assigned_to": "71", "created_at": "100", "updated_at": "200"}},
		"ticket_messages":            {{"id": "551", "ticket_id": "501", "admin_id": "71", "body": "یادداشت خصوصی", "internal": "1", "created_at": "200"}},
		"student_verifications":      {{"user_id": "101", "status": "approved", "phase": "complete", "details": cipher, "student_hash": legacy.SignJSON([]byte("40012345"), fixtureKey), "revision": "2", "review_note": "بررسی شد", "reviewed_by": "71", "reviewed_at": "200", "created_at": "100", "updated_at": "200"}},
		"announcement_categories":    {{"id": "601", "name": "دستهٔ آزمایشی", "active": "1", "created_at": "100"}},
		"announcement_items":         {{"content_id": "301", "category_id": "601", "delivery_mode": "followers", "creation_key": nil}},
		"announcement_subscriptions": {{"user_id": "101", "category_id": "601", "created_at": "100"}},
		"announcement_dispatches":    {{"id": "651", "content_id": "301", "content_version": "2", "category_id": "601", "audience": "followers", "max_user_id": "101", "cursor_id": "0", "state": "pending", "created_by": "71", "created_at": "200"}},
		"admin_sessions":             {{"token_hash": strings.Repeat("d", 64), "admin_id": "71", "auth_version": "3", "expires_at": "9999999999", "created_at": "100"}},
		"web_tokens":                 {{"token_hash": strings.Repeat("e", 64), "user_id": "101", "purpose": "support", "expires_at": "9999999999"}},
		"jobs":                       {{"id": "701", "dedupe_key": "old-unresolved-job", "kind": "sendMessage", "payload": "{ \"text\": \"پیام قدیمی\" }", "user_id": "101", "campaign_id": nil, "state": "pending", "attempts": "1", "available_at": "100", "lease_until": nil, "lease_token": nil, "result": nil, "error": nil, "created_at": "100", "finished_at": nil}},
		"favorites":                  {{"user_id": "101", "content_id": "301", "created_at": "100", "opaque": "\xff\x00\r\n"}},
	}
	dir := t.TempDir()
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(dir, "uploads"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "uploads", strings.Repeat("a", 64)), content, 0600); e != nil {
		t.Fatal(e)
	}
	f := &importFixture{dir: dir, rows: rows, cipher: cipher, token: token, totp: totp, permissions: permissions}
	f.write(t)
	return f
}
func (f *importFixture) write(t *testing.T) {
	t.Helper()
	path := filepath.Join(f.dir, "rows.ndjson.gz")
	file, e := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	writer := gzip.NewWriter(file)
	enc := json.NewEncoder(writer)
	write := func(v any) {
		if e := enc.Encode(v); e != nil {
			t.Fatal(e)
		}
	}
	write(map[string]any{"format": "unibot-source-v1", "encoding": "base64-per-cell", "created_at": "2026-11-11T00:00:00Z"})
	tables := []string{}
	counts := map[string]int64{}
	for table := range f.rows {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		write(map[string]any{"table": table, "ddl": base64.StdEncoding.EncodeToString([]byte("CREATE TABLE `" + table + "` (`id` bigint)"))})
		for _, row := range f.rows[table] {
			cells := map[string]any{}
			for key, value := range row {
				if value == nil {
					cells[key] = nil
				} else {
					cells[key] = base64.StdEncoding.EncodeToString([]byte(value.(string)))
				}
			}
			write(map[string]any{"table": table, "row": cells})
		}
		counts[table] = int64(len(f.rows[table]))
		write(map[string]any{"table": table, "count": counts[table]})
	}
	if e = writer.Close(); e != nil {
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	config, _ := json.Marshal(map[string]string{"app_key": fixtureKey, "dsn": "mysql:dbname=synthetic_source_test", "db_password": "synthetic-only"})
	if e = os.WriteFile(filepath.Join(f.dir, "resolved-config.private.json"), config, 0600); e != nil {
		t.Fatal(e)
	}
	files := map[string]any{}
	e = filepath.WalkDir(f.dir, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() || entry.Name() == "manifest.private.json" {
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(raw)
		rel, _ := filepath.Rel(f.dir, path)
		files[filepath.ToSlash(rel)] = map[string]any{"sha256": hex.EncodeToString(sum[:]), "bytes": len(raw)}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	manifest, _ := json.Marshal(map[string]any{"format": "unibot-preparation-v1", "complete": true, "source_tables": counts, "files": files, "uploads_present": true, "requires": []string{"source-write-and-DDL-pause", "external-environment-secrets-backup", "restore-rehearsal", "target-schema-completion", "queue-reconciliation"}})
	if e = os.WriteFile(filepath.Join(f.dir, "manifest.private.json"), manifest, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestImportPreservesDataAndDisablesExternalEffects(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	f := fixture(t)
	report, e := Import(ctx, p, f.dir)
	if e != nil {
		t.Fatal(e)
	}
	if report.ReadyForCutover || report.Reused {
		t.Fatal("first rehearsal incorrectly reported", report)
	}
	for _, name := range []string{"users", "admins", "contents", "student_verifications", "university_books", "ticket_messages"} {
		if report.Imported[name] != 1 {
			t.Fatalf("missing operational table %s: %#v", name, report.Imported)
		}
	}
	for _, name := range []string{"jobs", "admin_sessions", "web_tokens", "favorites"} {
		if report.Archived[name] != 1 {
			t.Fatalf("missing archived table %s: %#v", name, report.Archived)
		}
	}
	var telegram, cipher, document, permissions string
	var conversation *string
	var id int64
	if e = p.QueryRow(ctx, `SELECT telegram_id,conversation FROM users WHERE id=101`).Scan(&telegram, &conversation); e != nil || telegram != fixtureTelegram {
		t.Fatal("telegram ID lost precision", telegram, e)
	}
	if conversation != nil && *conversation != "" && *conversation != "{}" {
		t.Fatal("old conversation reactivated")
	}
	if e = p.QueryRow(ctx, `SELECT details FROM student_verifications WHERE user_id=101`).Scan(&cipher); e != nil || cipher != f.cipher {
		t.Fatal("ciphertext changed", e)
	}
	crypt, _ := legacy.New(fixtureKey)
	clear, e := crypt.Decrypt(cipher)
	if e != nil || !strings.Contains(string(clear), "دانشجوی آزمایشی") {
		t.Fatal("ciphertext no longer decrypts", e)
	}
	if e = p.QueryRow(ctx, `SELECT document FROM advanced_contents WHERE content_id=301`).Scan(&document); e != nil || document != fixtureDocument {
		t.Fatal("document raw bytes changed", e)
	}
	if e = p.QueryRow(ctx, `SELECT permissions FROM roles WHERE id=41`).Scan(&permissions); e != nil || permissions != f.permissions {
		t.Fatal("role permissions changed", e)
	}
	var totp string
	if e = p.QueryRow(ctx, `SELECT totp_secret FROM admins WHERE id=71`).Scan(&totp); e != nil || totp != f.totp {
		t.Fatal("TOTP ciphertext changed", e)
	}
	totpPlain, e := crypt.Decrypt(totp)
	if e != nil || string(totpPlain) != "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP" {
		t.Fatal("TOTP no longer decrypts", e)
	}
	var internal, deleted bool
	if e = p.QueryRow(ctx, `SELECT internal FROM ticket_messages WHERE id=551`).Scan(&internal); e != nil || !internal {
		t.Fatal("internal note became public", e)
	}
	if e = p.QueryRow(ctx, `SELECT deleted FROM university_books WHERE id=401`).Scan(&deleted); e != nil || !deleted {
		t.Fatal("deleted book resurrected", e)
	}
	var state string
	if e = p.QueryRow(ctx, `SELECT value FROM settings WHERE name='bot_state'`).Scan(&state); e != nil || state != `"off"` {
		t.Fatal("bot enabled during rehearsal", state, e)
	}
	if e = p.QueryRow(ctx, `SELECT state FROM announcement_dispatches WHERE id=651`).Scan(&state); e != nil || state != "cancelled" {
		t.Fatal("old dispatch active", state, e)
	}
	for _, table := range []string{"admin_sessions", "admin_access_grants", "work_jobs"} {
		var n int
		if e = p.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil || n != 0 {
			t.Fatal("volatile state activated", table, n, e)
		}
	}
	var archived string
	if e = p.QueryRow(ctx, `SELECT cells FROM migration_import_rows WHERE source_table='users' ORDER BY row_number LIMIT 1`).Scan(&archived); e != nil {
		t.Fatal(e)
	}
	var cells map[string]*string
	if e = json.Unmarshal([]byte(archived), &cells); e != nil {
		t.Fatal(e)
	}
	if cells["telegram_id"] == nil {
		t.Fatal("archived telegram missing")
	}
	raw, e := base64.StdEncoding.DecodeString(*cells["telegram_id"])
	if e != nil || string(raw) != fixtureTelegram {
		t.Fatal("archive lost raw Telegram ID", e)
	}
	raw, e = base64.StdEncoding.DecodeString(*cells["conversation"])
	if e != nil || string(raw) != f.rows["users"][0]["conversation"] {
		t.Fatal("archive lost old conversation", e)
	}
	if e = p.QueryRow(ctx, `SELECT cells FROM migration_import_rows WHERE source_table='favorites' ORDER BY row_number LIMIT 1`).Scan(&archived); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal([]byte(archived), &cells); e != nil {
		t.Fatal(e)
	}
	if cells["opaque"] == nil {
		t.Fatal("archive lost opaque bytes")
	}
	raw, e = base64.StdEncoding.DecodeString(*cells["opaque"])
	if e != nil || string(raw) != "\xff\x00\r\n" {
		t.Fatal("opaque bytes changed", e)
	}
	var challenge *string
	var expires *int64
	if e = p.QueryRow(ctx, `SELECT challenge_hash,expires_at FROM admin_telegram_links WHERE admin_id=71`).Scan(&challenge, &expires); e != nil || challenge != nil || expires != nil {
		t.Fatal("old challenge reactivated", e)
	}
	var secret string
	e = p.QueryRow(ctx, `SELECT value FROM settings WHERE name='telegram_token'`).Scan(&secret)
	if e == nil && secret == f.token {
		t.Fatal("old Telegram token active")
	}
	if e = p.QueryRow(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) VALUES('9007199254740995',1,1) RETURNING id`).Scan(&id); e != nil || id <= 101 {
		t.Fatal("sequence not advanced", id, e)
	}
	if e = p.QueryRow(ctx, `INSERT INTO taxonomy(kind,name,parent_id) VALUES('major','رشتهٔ تازه',201) RETURNING id`).Scan(&id); e != nil || id <= 202 {
		t.Fatal("taxonomy sequence/FK broken", id, e)
	}
}

func TestImportReplayDoesNotOverwriteSubsequentEdits(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	f := fixture(t)
	if _, e := Import(ctx, p, f.dir); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `UPDATE users SET first_name='پس از انتقال' WHERE id=101`); e != nil {
		t.Fatal(e)
	}
	report, e := Import(ctx, p, f.dir)
	if e != nil || !report.Reused {
		t.Fatal("replay not recognized", report, e)
	}
	var value string
	p.QueryRow(ctx, `SELECT first_name FROM users WHERE id=101`).Scan(&value)
	if value != "پس از انتقال" {
		t.Fatal("replay overwrote data")
	}
}

func TestImportRefusesNonemptyTarget(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, e := p.Exec(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) VALUES('123456',1,1)`); e != nil {
		t.Fatal(e)
	}
	if _, e := Import(ctx, p, fixture(t).dir); e == nil {
		t.Fatal("nonempty target accepted")
	}
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	if n != 1 {
		t.Fatal("existing target modified")
	}
}

func TestImportRejectsTamperingBeforeWrites(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	f := fixture(t)
	file := filepath.Join(f.dir, "rows.ndjson.gz")
	stream, e := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	stream.Write([]byte("corrupt"))
	stream.Close()
	if _, e = Import(ctx, p, f.dir); e == nil {
		t.Fatal("corrupted archive accepted")
	}
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	if n != 0 {
		t.Fatal("corrupted snapshot wrote data")
	}
}

func TestImportInvalidValuesRollBackEverything(t *testing.T) {
	for _, scenario := range []string{"boolean", "foreign-key", "integer-overflow", "missing-required-column"} {
		t.Run(scenario, func(t *testing.T) {
			p := testdb.Open(t)
			ctx := context.Background()
			f := fixture(t)
			switch scenario {
			case "boolean":
				f.rows["users"][0]["delivery_blocked"] = "2"
			case "foreign-key":
				f.rows["contents"][0]["reviewed_by"] = "999999"
			case "missing-required-column":
				delete(f.rows["users"][0], "created_at")
			case "integer-overflow":
				f.rows["listings"] = []map[string]any{{"id": "801", "user_id": "101", "title": "آگهی", "body": "متن آگهی", "price": "18446744073709551615", "category_id": nil, "file_id": nil, "contact": "", "state": "pending", "moderator_note": "", "fingerprint": strings.Repeat("f", 64), "expires_at": "9999999999", "created_at": "100", "updated_at": "100"}}
			}
			f.write(t)
			if _, e := Import(ctx, p, f.dir); e == nil {
				t.Fatal("invalid snapshot accepted")
			}
			var n int
			p.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
			if n != 0 {
				t.Fatal("partial import persisted")
			}
			p.QueryRow(ctx, `SELECT count(*) FROM roles`).Scan(&n)
			if n != 0 {
				t.Fatal("role persisted after rollback")
			}
		})
	}
}

func TestConcurrentImportOfSameSnapshotIsIdempotent(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	f := fixture(t)
	type result struct {
		report Report
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() { <-start; r, e := Import(ctx, p, f.dir); results <- result{r, e} }()
	}
	close(start)
	imported, reused := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.report.Reused {
			reused++
		} else {
			imported++
		}
	}
	if imported != 1 || reused != 1 {
		t.Fatal("concurrent import was not serialized", imported, reused)
	}
	var count int
	if e := p.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate or missing concurrent import", count, e)
	}
}

func TestImportRefusesFallbackSearchPathSchema(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	var fallback string
	if e := p.QueryRow(ctx, `SELECT current_schema()`).Scan(&fallback); e != nil {
		t.Fatal(e)
	}
	partial := fallback + "_partial"
	quoted := pgx.Identifier{partial}.Sanitize()
	fallbackQuoted := pgx.Identifier{fallback}.Sanitize()
	if _, e := p.Exec(ctx, "CREATE SCHEMA "+quoted+";CREATE TABLE "+quoted+".users (LIKE "+fallbackQuoted+".users INCLUDING DEFAULTS INCLUDING CONSTRAINTS)"); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO roles(id,name,permissions) VALUES(999,'fallback marker','[]');INSERT INTO settings(name,value,updated_at) VALUES('fallback_marker','"keep"',1);UPDATE queue_control SET paused=false`); e != nil {
		t.Fatal(e)
	}
	cfg := p.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = quoted + "," + fallbackQuoted
	cfg.MaxConns = 2
	scoped, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal("cannot create scoped test pool")
	}
	t.Cleanup(func() { scoped.Close(); p.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE") })
	f := fixture(t)
	f.rows = map[string][]map[string]any{"roles": f.rows["roles"], "settings": f.rows["settings"]}
	f.write(t)
	if _, e = Import(ctx, scoped, f.dir); !errors.Is(e, ErrTarget) {
		t.Fatal("partial schema must fail target preflight", e)
	}
	var count int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM roles`).Scan(&count); e != nil || count != 1 {
		t.Fatal("fallback roles mutated", count, e)
	}
	var marker string
	if e = p.QueryRow(ctx, `SELECT value FROM settings WHERE name='fallback_marker'`).Scan(&marker); e != nil || marker != `"keep"` {
		t.Fatal("fallback settings mutated", e)
	}
	var paused bool
	if e = p.QueryRow(ctx, `SELECT paused FROM queue_control`).Scan(&paused); e != nil || paused {
		t.Fatal("fallback queue control mutated", e)
	}
	if e = scoped.QueryRow(ctx, "SELECT count(*) FROM "+quoted+".users").Scan(&count); e != nil || count != 0 {
		t.Fatal("partial schema mutated", count, e)
	}
}

func TestImportRejectsWrongApplicationKey(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	f := fixture(t)
	configPath := filepath.Join(f.dir, "resolved-config.private.json")
	raw, e := os.ReadFile(configPath)
	if e != nil {
		t.Fatal(e)
	}
	var config map[string]any
	if e = json.Unmarshal(raw, &config); e != nil {
		t.Fatal(e)
	}
	config["app_key"] = strings.Repeat("1", 64)
	raw, e = json.Marshal(config)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(configPath, raw, 0600); e != nil {
		t.Fatal(e)
	}
	manifestPath := filepath.Join(f.dir, "manifest.private.json")
	manifestRaw, e := os.ReadFile(manifestPath)
	if e != nil {
		t.Fatal(e)
	}
	var manifest map[string]any
	if e = json.Unmarshal(manifestRaw, &manifest); e != nil {
		t.Fatal(e)
	}
	files := manifest["files"].(map[string]any)
	sum := sha256.Sum256(raw)
	files["resolved-config.private.json"] = map[string]any{"sha256": hex.EncodeToString(sum[:]), "bytes": len(raw)}
	manifestRaw, e = json.Marshal(manifest)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(manifestPath, manifestRaw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Import(ctx, p, f.dir); e == nil {
		t.Fatal("ciphertext accepted with a different application key")
	}
	for _, table := range []string{"roles", "admins", "users"} {
		var n int
		if e = p.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil || n != 0 {
			t.Fatal("wrong-key import partially persisted", table, n, e)
		}
	}
}

func TestMigrationArchiveIsNotReadableByRuntimeRole(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	var exists bool
	if e := p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='unibot_app')`).Scan(&exists); e != nil {
		t.Fatal(e)
	}
	if !exists {
		t.Skip("runtime role not provisioned")
	}
	if _, e := Import(ctx, p, fixture(t).dir); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"migration_import_rows", "migration_import_tables", "migration_import_runs"} {
		var allowed bool
		if e := p.QueryRow(ctx, `SELECT has_table_privilege('unibot_app',$1,'SELECT')`, table).Scan(&allowed); e != nil || allowed {
			t.Fatal("private migration archive granted to runtime role", table, e)
		}
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	var schema string
	if e = tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "GRANT USAGE ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" TO unibot_app;SET LOCAL ROLE unibot_app"); e != nil {
		t.Fatal(e)
	}
	var data string
	if e = tx.QueryRow(ctx, `SELECT cells FROM migration_import_rows LIMIT 1`).Scan(&data); e == nil {
		t.Fatal("runtime role read private migration archive")
	}
}

func TestImportDoesNotResolveTemporaryBusinessTables(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	f := fixture(t)
	conn, e := p.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = conn.Exec(ctx, `CREATE TEMP TABLE users(id bigint)`); e != nil {
		t.Fatal(e)
	}
	conn.Release()
	if _, e = Import(ctx, p, f.dir); e != nil {
		t.Fatal(e)
	}
	var schema string
	p.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema)
	var count int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{schema, "users"}.Sanitize()).Scan(&count); e != nil || count != 1 {
		t.Fatal("temporary relation shadowed target", count, e)
	}
}

// Optional actual PHP-export -> Go-import rehearsal. Only the synthetic transfer_*_test fixture is accepted.
func TestPHPExportedSnapshotRoundTrip(t *testing.T) {
	dir := os.Getenv("MIGRATION_PHP_FIXTURE")
	if dir == "" {
		t.Skip("MIGRATION_PHP_FIXTURE required")
	}
	raw, e := os.ReadFile(filepath.Join(dir, "resolved-config.private.json"))
	if e != nil {
		t.Fatal("fixture config unavailable")
	}
	var config struct {
		DSN string `json:"dsn"`
	}
	if json.Unmarshal(raw, &config) != nil || !regexp.MustCompile(`dbname=transfer_[a-f0-9]+_test(?:;|$)`).MatchString(config.DSN) {
		t.Fatal("synthetic PHP fixture required")
	}
	p := testdb.Open(t)
	ctx := context.Background()
	report, e := Import(ctx, p, dir)
	if e != nil || report.ReadyForCutover {
		t.Fatal("rehearsal failed", e)
	}
	var tables, rows int64
	if e = p.QueryRow(ctx, `SELECT count(*),COALESCE(sum(row_count),0) FROM migration_import_tables`).Scan(&tables, &rows); e != nil || tables != 58 || rows < 40 {
		t.Fatal("source archive incomplete", tables, rows, e)
	}
	var telegram, cipher, metadata string
	if e = p.QueryRow(ctx, `SELECT telegram_id FROM users WHERE id=101`).Scan(&telegram); e != nil || telegram != fixtureTelegram {
		t.Fatal("source identity changed", e)
	}
	if e = p.QueryRow(ctx, `SELECT details FROM student_verifications WHERE user_id=101`).Scan(&cipher); e != nil {
		t.Fatal(e)
	}
	crypt, _ := legacy.New(fixtureKey)
	plain, e := crypt.Decrypt(cipher)
	if e != nil || !strings.Contains(string(plain), "40012345") {
		t.Fatal("PHP ciphertext incompatible", e)
	}
	if e = p.QueryRow(ctx, `SELECT metadata FROM contents WHERE id=301`).Scan(&metadata); e != nil || metadata != `{ "access":"public" }` {
		t.Fatal("PHP raw JSON changed", e)
	}
	replay, e := Import(ctx, p, dir)
	if e != nil || !replay.Reused {
		t.Fatal("PHP snapshot replay failed", e)
	}
}
