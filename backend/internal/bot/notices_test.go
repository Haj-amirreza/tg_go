package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/announcements"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/rich"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"strings"
	"testing"
)

func TestNoticeBankSubscriptionAndDeferredRevocation(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	f.send("notice:home", true)
	var user, admin int64
	f.pool.QueryRow(ctx, `SELECT id FROM users WHERE telegram_id='123456'`).Scan(&user)
	_, e := f.pool.Exec(ctx, `INSERT INTO roles(name,permissions) VALUES('publisher','["content","publish","campaigns"]')`)
	if e != nil {
		t.Fatal(e)
	}
	e = f.pool.QueryRow(ctx, `INSERT INTO admins(username,password_hash,name,role_id,is_super,created_at) SELECT 'publisher','x','Publisher',id,true,1 FROM roles WHERE name='publisher' RETURNING id`).Scan(&admin)
	if e != nil {
		t.Fatal(e)
	}
	tx, _ := f.pool.Begin(ctx)
	c, e := catalog.Save(ctx, tx, catalog.Content{Kind: "announcements", Title: "اطلاعیه آزمون", Body: "متن اطلاعیه", Status: "published", Metadata: map[string]any{"access": "public"}}, admin, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = announcements.Assign(ctx, tx, c.ID, 1, "bank"); e != nil {
		t.Fatal(e)
	}
	bank, e := announcements.Preview(ctx, tx, c.ID, "bank")
	if e != nil || bank.Count != 0 {
		t.Fatal(bank, e)
	}
	if id, e := announcements.Dispatch(ctx, tx, bank, admin); e != nil || id != 0 {
		t.Fatal("bank dispatched", id, e)
	}
	if e = announcements.Follow(ctx, tx, user, 1, true); e != nil {
		t.Fatal(e)
	}
	p, e := announcements.Preview(ctx, tx, c.ID, "followers")
	if e != nil || p.Count != 1 {
		t.Fatal(p, e)
	}
	id, e := announcements.Dispatch(ctx, tx, p, admin)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(body(f.send("notice:bank:0:0", true)), "?start=notice_") {
		t.Fatal("missing deep link")
	}
	if !strings.Contains(body(f.send("notice:follows", true)), "دسته") {
		t.Fatal("no follows")
	}
	raw, _ := json.Marshal(id)
	tx, _ = f.pool.Begin(ctx)
	if e = (Engine{}).ExpandNotice(ctx, tx, workqueue.Job{Payload: raw}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	var jobs int
	f.pool.QueryRow(ctx, `SELECT count(*) FROM work_jobs WHERE dedupe_key LIKE $1`, fmt.Sprintf("notice:%d:user:%%", id)).Scan(&jobs)
	if jobs == 0 {
		t.Fatal("no broadcast jobs")
	}
	tx, _ = f.pool.Begin(ctx)
	if ok, e := announcements.DeliveryAllowed(ctx, tx, id, user); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if e = announcements.Follow(ctx, tx, user, 1, false); e != nil {
		t.Fatal(e)
	}
	if ok, e := announcements.DeliveryAllowed(ctx, tx, id, user); e != nil || ok {
		t.Fatal("unsubscribe not enforced", e)
	}
	tx.Rollback(ctx)
}
func TestRichToggleNonceAndStaleContent(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	f.send("menu", true)
	tx, _ := f.pool.Begin(ctx)
	var admin int64
	tx.Exec(ctx, `INSERT INTO roles(name,permissions) VALUES('rich','["content","publish"]')`)
	tx.QueryRow(ctx, `INSERT INTO admins(username,password_hash,name,role_id,created_at) SELECT 'rich','x','Rich',id,1 FROM roles WHERE name='rich' RETURNING id`).Scan(&admin)
	c, e := catalog.Save(ctx, tx, catalog.Content{Kind: "announcements", Title: "تیک آزمایشی", Status: "published", Metadata: map[string]any{"access": "public"}}, admin, true)
	if e != nil {
		t.Fatal(e)
	}
	doc := rich.Document{Schema: 1, Rich: true, Blocks: []rich.Block{{Type: "buttons", Enabled: true, Columns: 1, Items: []rich.Item{{Text: "خواندم", Action: "toggle", Step: 1, Max: 100}}}}}
	raw, _ := json.Marshal(doc)
	tx.Exec(ctx, `INSERT INTO advanced_contents(content_id,document,updated_at) VALUES($1,$2,1)`, c.ID, string(raw))
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	out := f.send(fmt.Sprintf("view:%d", c.ID), true)
	action := findData(t, out, "☐ خواندم")
	if !strings.Contains(fmt.Sprint(f.send(action, true)), "☑") {
		t.Fatal("toggle failed")
	}
	f.send(action, true)
	var value int
	f.pool.QueryRow(ctx, `SELECT value FROM interaction_values LIMIT 1`).Scan(&value)
	if value != 1 {
		t.Fatal("replay toggled twice")
	}
	f.pool.Exec(ctx, `UPDATE contents SET version=version+1 WHERE id=$1`, c.ID)
	f.send(action, true)
	f.pool.QueryRow(ctx, `SELECT value FROM interaction_values LIMIT 1`).Scan(&value)
	if value != 1 {
		t.Fatal("stale action")
	}
}
func TestDispatchCannotExceedCapAcrossChangingMembership(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	actor := adminFlow(f, `["content","publish","campaigns"]`)
	f.pool.Exec(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) VALUES('999888',1,1)`)
	tx, _ := f.pool.Begin(ctx)
	c, e := catalog.Save(ctx, tx, catalog.Content{Kind: "announcements", Title: "سقف ارسال", Body: "متن", Status: "published", Metadata: map[string]any{}}, actor, true)
	if e != nil {
		t.Fatal(e)
	}
	var dispatch int64
	e = tx.QueryRow(ctx, `INSERT INTO announcement_dispatches(content_id,content_version,audience,max_user_id,created_by,created_at,enqueued_count) VALUES($1,1,'all',999999,$2,1,1499) RETURNING id`, c.ID, actor).Scan(&dispatch)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(dispatch)
	if e = (Engine{}).ExpandNotice(ctx, tx, workqueue.Job{Payload: raw}); e != nil {
		t.Fatal(e)
	}
	var state string
	tx.QueryRow(ctx, `SELECT state FROM announcement_dispatches WHERE id=$1`, dispatch).Scan(&state)
	if state != "cancelled" {
		t.Fatal("audience growth ignored", state)
	}
	tx.Rollback(ctx)
}
