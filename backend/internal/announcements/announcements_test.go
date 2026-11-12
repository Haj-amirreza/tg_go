package announcements

import (
	"context"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/testdb"
	"testing"
)

func TestBroadcastCapAndStalePreview(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	tx, _ := p.Begin(ctx)
	defer tx.Rollback(ctx)
	tx.Exec(ctx, `INSERT INTO roles(name,permissions) VALUES('test','[]')`)
	var actor int64
	tx.QueryRow(ctx, `INSERT INTO admins(username,password_hash,name,role_id,created_at) SELECT 'test','x','test',id,1 FROM roles RETURNING id`).Scan(&actor)
	c, e := catalog.Save(ctx, tx, catalog.Content{Kind: "announcements", Title: "ظرفیت", Status: "published", Metadata: map[string]any{}}, actor, true)
	if e != nil {
		t.Fatal(e)
	}
	tx.Exec(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) SELECT (1000000+n)::text,1,1 FROM generate_series(1,1500)n`)
	plan, e := Preview(ctx, tx, c.ID, "all")
	if e != nil || plan.Count != 1500 {
		t.Fatal(plan, e)
	}
	tx.Exec(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) VALUES('9999999',1,1)`)
	if _, e = Preview(ctx, tx, c.ID, "all"); e != catalog.ErrInvalid {
		t.Fatal("oversized broadcast accepted", e)
	}
	if _, e = Dispatch(ctx, tx, plan, actor); e == nil {
		t.Fatal("changed audience accepted")
	}
	if bank, e := Preview(ctx, tx, c.ID, "bank"); e != nil || bank.Count != 0 {
		t.Fatal(bank, e)
	}
}
