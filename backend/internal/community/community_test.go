package community

import (
	"context"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/testdb"
	"testing"
)

func TestPersonalOwnershipDatesAndOptimisticEdit(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tx, _ := pool.Begin(ctx)
	defer tx.Rollback(ctx)
	var user, other int64
	tx.QueryRow(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) VALUES('111222',1,1) RETURNING id`).Scan(&user)
	tx.QueryRow(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) VALUES('333444',1,1) RETURNING id`).Scan(&other)
	tx.Exec(ctx, `INSERT INTO student_verifications(user_id,status,details,created_at,updated_at) VALUES($1,'approved','{}',1,1),($2,'approved','{}',1,1)`, user, other)
	p := Personal{Kind: "task", Title: "تحویل تمرین", Body: "ریاضی", Day: "2026-11-07", Clock: "15:30"}
	p, e := PersonalSave(ctx, tx, user, p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = PersonalSave(ctx, tx, other, p); e != catalog.ErrConflict {
		t.Fatal("other user edited item", e)
	}
	old := p
	p.Done = true
	p, e = PersonalSave(ctx, tx, user, p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = PersonalSave(ctx, tx, user, old); e != catalog.ErrConflict {
		t.Fatal("stale edit accepted", e)
	}
	p.Day = "2026-02-30"
	if _, e = PersonalSave(ctx, tx, user, p); e != catalog.ErrInvalid {
		t.Fatal("invalid date", e)
	}
	rows, e := PersonalList(ctx, tx, other, "2026-11-07", "task", 0)
	if e != nil || len(rows) != 0 {
		t.Fatal("private list leaked", e)
	}
}
func TestConsentModerationAndAtomicBulkArchive(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tx, _ := pool.Begin(ctx)
	defer tx.Rollback(ctx)
	var user int64
	tx.QueryRow(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) VALUES('100100',1,1) RETURNING id`).Scan(&user)
	tx.Exec(ctx, `INSERT INTO student_verifications(user_id,status,details,created_at,updated_at) VALUES($1,'approved','{}',1,1)`, user)
	a := Ad{Kind: "market", Title: "فروش کتاب ریاضی", Body: "کتاب سالم و بدون یادداشت", Contact: "@contact_user"}
	if _, e := Save(ctx, tx, a, user, false); e != catalog.ErrInvalid {
		t.Fatal("no consent accepted", e)
	}
	a.Consent = true
	a, e := Save(ctx, tx, a, user, false)
	if e != nil || a.State != "pending" {
		t.Fatal(a, e)
	}
	if _, e = Save(ctx, tx, Ad{Kind: "market", Title: a.Title, Body: a.Body, Consent: true}, user, false); e != catalog.ErrConflict {
		t.Fatal("duplicate accepted", e)
	}
	if list, e := List(ctx, tx, "market", user, false, false, "", 0); e != nil || len(list) != 0 {
		t.Fatal("pending public", e)
	}
	a.State = "published"
	a, e = Save(ctx, tx, a, user, true)
	if e != nil {
		t.Fatal(e)
	}
	selection := []catalog.Revision{{ID: fmt.Sprint(a.ID), Revision: a.Revision}, {ID: "9999999", Revision: 1}}
	if e = RemoveBatch(ctx, tx, "market", selection); e != catalog.ErrConflict {
		t.Fatal("non atomic selection", e)
	}
	still, e := Get(ctx, tx, "market", a.ID, 0, true)
	if e != nil || still.State != "published" {
		t.Fatal("partially archived")
	}
	if e = RemoveBatch(ctx, tx, "market", selection[:1]); e != nil {
		t.Fatal(e)
	}
	removed, _ := Get(ctx, tx, "market", a.ID, 0, true)
	if removed.Contact != "" || removed.State != "deleted" {
		t.Fatal("archive retained contact", removed)
	}
}
