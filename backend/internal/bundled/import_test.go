package bundled_test

import (
	"context"
	"github.com/Haj-amirreza/tg_go/backend/internal/bot"
	"github.com/Haj-amirreza/tg_go/backend/internal/botops"
	"github.com/Haj-amirreza/tg_go/backend/internal/bundled"
	"github.com/Haj-amirreza/tg_go/backend/internal/identity"
	"github.com/Haj-amirreza/tg_go/backend/internal/legacy"
	"github.com/Haj-amirreza/tg_go/backend/internal/testdb"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func paths() bundled.Paths {
	root := "../../../database/seed"
	return bundled.Paths{Contacts: filepath.Join(root, "directory-safe.jsonl.gz"), Books: filepath.Join(root, "university-books.jsonl.gz"), RawContacts: filepath.Join(root, "bkatu_contacts_full.json")}
}
func TestRealBundleAndBotActivation(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	key := strings.Repeat("1", 64)
	s, e := identity.New(p, key, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Bootstrap(ctx, identity.BootstrapInput{Username: "operator", Password: "Testing-only-strong-password", Name: "Operator"}); e != nil {
		t.Fatal(e)
	}
	report, e := bundled.Run(ctx, p, key, paths())
	if e != nil {
		t.Fatal(e)
	}
	if report.Books != 7991 || report.Contents != 670 || report.Published != 55 || report.Draft != 615 || report.Reused {
		t.Fatal("unexpected imported population")
	}
	var enc string
	if e = p.QueryRow(ctx, `SELECT payload_enc FROM bundled_source_archive`).Scan(&enc); e != nil {
		t.Fatal(e)
	}
	c, _ := legacy.New(key)
	raw, e := c.Decrypt(enc)
	original, _ := os.ReadFile(paths().RawContacts)
	if e != nil || string(raw) != string(original) {
		t.Fatal("original source was not preserved")
	}
	for _, table := range []string{"bundled_source_archive", "bundled_import_runs"} {
		var visible bool
		if e = p.QueryRow(ctx, `SELECT has_table_privilege('unibot_app',$1,'SELECT')`, table).Scan(&visible); e != nil || visible {
			t.Fatal("archive readable by runtime", e)
		}
	}
	// Replay must preserve edits, including deletion, rather than silently resetting the bank.
	if _, e = p.Exec(ctx, `UPDATE university_books SET title='edited',deleted=true WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	again, e := bundled.Run(ctx, p, key, paths())
	if e != nil || !again.Reused {
		t.Fatal("replay failed", e)
	}
	var edited bool
	p.QueryRow(ctx, `SELECT title='edited' AND deleted FROM university_books WHERE id=1`).Scan(&edited)
	if !edited {
		t.Fatal("replay overwrote edit")
	}
	if _, e = bundled.Run(ctx, p, strings.Repeat("2", 64), paths()); e == nil {
		t.Fatal("wrong archive key accepted")
	}
	metadata := botops.Metadata{ID: 123456789, Username: "university_test_bot"}
	opts := botops.Options{RequireBundleImport: true}
	st, e := botops.Prepare(ctx, p, metadata, opts)
	if e != nil || st.Ready || !st.Paused || st.Enabled {
		t.Fatal("unlinked bot ready", e)
	}
	if _, e = botops.Enable(ctx, p, metadata, opts); e == nil {
		t.Fatal("unlinked administrator allowed")
	}
	if e = botops.BindAdmin(ctx, p, "operator", "9007199254740993"); e != nil {
		t.Fatal(e)
	}
	if e = botops.BindAdmin(ctx, p, "operator", "9007199254740993"); e != nil {
		t.Fatal("same link not idempotent", e)
	}
	if e = botops.AddTelegramAdmin(ctx, p, "7777778"); e != nil {
		t.Fatal(e)
	}
	if e = botops.AddTelegramAdmin(ctx, p, "7777778"); e != nil {
		t.Fatal("extra admin replay", e)
	}
	var admins int
	p.QueryRow(ctx, `SELECT count(*) FROM admins WHERE active AND is_super`).Scan(&admins)
	if admins != 2 {
		t.Fatal("extra administrator was duplicated")
	}
	if e = botops.BindAdmin(ctx, p, "operator", "9007199254740994"); e == nil {
		t.Fatal("identity reassigned")
	}
	if _, e = botops.Enable(ctx, p, botops.Metadata{ID: 123456780, Username: metadata.Username}, opts); e == nil {
		t.Fatal("prepared identity changed")
	}
	if _, e = p.Exec(ctx, `INSERT INTO work_jobs(dedupe_key,kind,payload,external_effect) VALUES('new-update','telegram.update','{}',false)`); e != nil {
		t.Fatal(e)
	}
	if _, e = botops.Enable(ctx, p, metadata, opts); e == nil {
		t.Fatal("pending work was ignored")
	}
	opts.AllowPendingUpdates = true
	if _, e = p.Exec(ctx, `INSERT INTO work_jobs(dedupe_key,kind,payload,external_effect) VALUES('send','telegram.send','{}',true)`); e != nil {
		t.Fatal(e)
	}
	if _, e = botops.Enable(ctx, p, metadata, opts); e == nil {
		t.Fatal("pending delivery was ignored")
	}
	p.Exec(ctx, `DELETE FROM work_jobs WHERE dedupe_key='send'`)
	st, e = botops.Enable(ctx, p, metadata, opts)
	if e != nil || !st.Enabled || st.Paused {
		t.Fatal("activation failed", e)
	}
	if e = botops.BindAdmin(ctx, p, "operator", "9007199254740993"); e == nil {
		t.Fatal("live binding allowed")
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine := bot.Engine{Username: metadata.Username, AppKey: key, AdminURL: "https://panel.example.org/manage"}
	if err = engine.Handle(ctx, tx, workqueue.Job{ID: 9001, Kind: "telegram.update", Payload: []byte(`{"update_id":9001,"message":{"message_id":1,"chat":{"id":777777,"type":"private"},"from":{"id":777777,"first_name":"Student"},"text":"/start"}}`)}); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	var replies int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM work_jobs WHERE kind='telegram.send'`).Scan(&replies); err != nil || replies == 0 {
		tx.Rollback(ctx)
		t.Fatal("activated bot did not enqueue a response", err)
	}
	tx.Rollback(ctx)
	if e = botops.Disable(ctx, p); e != nil {
		t.Fatal(e)
	}
	st, e = botops.Check(ctx, p, opts)
	if e != nil || st.Enabled || !st.Paused || st.PendingUpdates != 1 {
		t.Fatal("disable lost queue", e)
	}
}

func TestBundleRefusesMutationAndCorruptInputs(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	key := strings.Repeat("1", 64)
	if _, e := p.Exec(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) VALUES('123',0,0)`); e != nil {
		t.Fatal(e)
	}
	if _, e := bundled.Run(ctx, p, key, paths()); e == nil {
		t.Fatal("business database overwritten")
	}
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM contents`).Scan(&n)
	if n != 0 {
		t.Fatal("partial import")
	}
	source := paths()
	source.RawContacts = filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(source.RawContacts, []byte("{}"), 0600)
	if _, e := bundled.Run(ctx, p, key, source); e == nil {
		t.Fatal("corrupt source accepted")
	}
}
