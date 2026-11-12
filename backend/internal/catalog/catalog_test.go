package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/cache"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/Haj-amirreza/tg_go/backend/internal/testdb"
	"os"
	"testing"
)

func TestVisibilityAndEditorialConcurrency(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	p.Exec(ctx, "INSERT INTO roles(id,name,permissions) VALUES(1,'test','[]'); INSERT INTO admins(id,username,password_hash,name,role_id,created_at) VALUES(1,'test','unused','test',1,1)")
	tx, _ := p.Begin(ctx)
	c, e := Save(ctx, tx, Content{Kind: "professors", Title: "آزمایش", Status: "pending", Metadata: map[string]any{}}, 1, false)
	if e != nil {
		t.Fatal(e)
	}
	tx.Commit(ctx)
	tx, _ = p.Begin(ctx)
	c.Status = "published"
	if _, e = Save(ctx, tx, c, 1, false); e == nil {
		t.Fatal("published without permission")
	}
	tx.Rollback(ctx)
	tx, _ = p.Begin(ctx)
	c, e = Save(ctx, tx, c, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	tx.Commit(ctx)
	stale := c
	tx, _ = p.Begin(ctx)
	c.Title = "نام تازه"
	c, e = Save(ctx, tx, c, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	tx.Commit(ctx)
	tx, _ = p.Begin(ctx)
	if _, e = Save(ctx, tx, stale, 1, true); e != ErrConflict {
		t.Fatal("stale update accepted", e)
	}
	tx.Rollback(ctx)
	c.Metadata["audience_tag"] = "engineering"
	if Visible(c, nil, false, 100) || !Visible(c, []string{"engineering"}, false, 100) {
		t.Fatal("tag visibility")
	}
}
func TestBookGenerationInvalidationAndSearchEscape(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	store, _ := cache.New(8, 40960, "")
	p.Exec(ctx, `INSERT INTO university_books(source_key,title,details,search_text,category) VALUES($1,'کتاب درصد','{}','کتاب درصد 100%','general')`, fmt.Sprintf("%064d", 1))
	tx, _ := p.Begin(ctx)
	one, e := CachedBooks(ctx, tx, store, "100%", "", 0)
	if e != nil || one.Total != 1 {
		t.Fatal(e)
	}
	tx.Commit(ctx)
	tx, _ = p.Begin(ctx)
	two, e := CachedBooks(ctx, tx, store, "100%", "", 0)
	tx.Commit(ctx)
	if e != nil || two.Total != 1 || store.Stats().Hits != 1 {
		t.Fatal("cache miss")
	}
	p.Exec(ctx, "UPDATE university_books SET deleted=true; UPDATE catalog_generations SET revision=revision+1 WHERE scope='books'")
	tx, _ = p.Begin(ctx)
	deleted, e := CachedBooks(ctx, tx, store, "100%", "", 0)
	tx.Commit(ctx)
	if e != nil || deleted.Total != 0 {
		raw, _ := json.Marshal(deleted)
		t.Fatal("cached deletion stale", string(raw))
	}
	if Normalize("كیف ۱۲٣\u200cرايانه") != "کیف 123 رایانه" {
		t.Fatal("normalization")
	}
}

func TestBundledBookSeedIsOfflineAndDoesNotOverwrite(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	file, e := os.Open("../../../database/seed/university-books.jsonl.gz")
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	tx, _ := p.Begin(ctx)
	count, e := SeedBooks(ctx, tx, file)
	if e != nil {
		tx.Rollback(ctx)
		t.Fatal(e)
	}
	if count < 100 {
		t.Fatal("unexpected bundled seed size", count)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	p.Exec(ctx, "UPDATE university_books SET deleted=true,title='تغییر مدیر' WHERE id=(SELECT min(id) FROM university_books)")
	file.Seek(0, 0)
	tx, _ = p.Begin(ctx)
	count, e = SeedBooks(ctx, tx, file)
	if e != nil || count != 0 {
		t.Fatal("seed was destructive", count, e)
	}
	tx.Commit(ctx)
	var n int
	p.QueryRow(ctx, "SELECT count(*) FROM university_books WHERE deleted AND title='تغییر مدیر'").Scan(&n)
	if n != 1 {
		t.Fatal("edited/deleted book resurrected")
	}
}

func TestBulkArchiveIsAtomicAndBoundedQueries(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	p.Exec(ctx, "INSERT INTO roles(id,name,permissions) VALUES(1,'test','[]'); INSERT INTO admins(id,username,password_hash,name,role_id,created_at) VALUES(1,'test','unused','test',1,1)")
	tx, _ := p.Begin(ctx)
	items := []Revision{}
	for i := 0; i < 15; i++ {
		c, e := Save(ctx, tx, Content{Kind: "contacts", Title: fmt.Sprint("مخاطب ", i), Status: "draft", Metadata: map[string]any{}}, 1, false)
		if e != nil {
			t.Fatal(e)
		}
		items = append(items, Revision{ID: fmt.Sprint(c.ID), Version: c.Version})
	}
	tx.Commit(ctx)
	tx, _ = p.Begin(ctx)
	before := postgres.Metrics()["queries"]
	if e := Archive(ctx, tx, items, false, 1, false); e != nil {
		t.Fatal(e)
	}
	used := postgres.Metrics()["queries"] - before
	if used > 5 {
		t.Fatal("archive query count scales with rows", used)
	}
	tx.Rollback(ctx)
	items[0].Version = 99
	tx, _ = p.Begin(ctx)
	if Archive(ctx, tx, items, false, 1, false) != ErrConflict {
		t.Fatal("stale bulk operation accepted")
	}
	tx.Rollback(ctx)
	var n int
	p.QueryRow(ctx, "SELECT count(*) FROM contents WHERE status='archived'").Scan(&n)
	if n != 0 {
		t.Fatal("partial archive committed")
	}
}
