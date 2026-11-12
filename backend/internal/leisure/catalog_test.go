package leisure

import (
	"context"
	"github.com/Haj-amirreza/tg_go/backend/internal/cache"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/testdb"
	"testing"
)

func TestOverlayCacheInvalidationAndStaleRevision(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	tx, _ := p.Begin(ctx)
	defer tx.Rollback(ctx)
	store, _ := cache.New(8, 1<<20, "")
	before, gen, e := Load(ctx, tx, store)
	if e != nil || len(before["poem"]) == 0 {
		t.Fatal(e)
	}
	id := "custom_123456abcdef"
	next, e := Save(ctx, tx, Change{Kind: "quote", ID: id, Row: map[string]any{"text": "جملهٔ تازه"}, Revision: gen})
	if e != nil {
		t.Fatal(e)
	}
	after, g, e := Load(ctx, tx, store)
	if e != nil || g != next || after["quote"][id] == nil {
		t.Fatal("cache stale", e)
	}
	if _, e = Save(ctx, tx, Change{Kind: "quote", ID: id, Revision: gen}); e != catalog.ErrConflict {
		t.Fatal("stale overlay accepted", e)
	}
	if _, e = Save(ctx, tx, Change{Kind: "quote", ID: id, Revision: next}); e != nil {
		t.Fatal(e)
	}
	after, _, e = Load(ctx, tx, store)
	if e != nil || after["quote"][id] != nil {
		t.Fatal("deleted item resurrected", e)
	}
}
func TestBotNeverMissesForcedWinOrBlock(t *testing.T) {
	for _, b := range []string{"OO.XX....", "XX..O....", "X.O.X.O.."} {
		after := BotMove(b)
		if len(after) != 9 {
			t.Fatal(after)
		}
		if b == "OO.XX...." && Result(after) != "O" {
			t.Fatal("missed win", after)
		}
		if b == "XX..O...." && after[2] != 'O' {
			t.Fatal("missed block", after)
		}
	}
	if v := BotMove("XXXOO...."); v != "XXXOO...." {
		t.Fatal("played after end")
	}
}
func TestConcurrentOverlayOnlyOneRevisionWins(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			tx, e := p.Begin(ctx)
			if e != nil {
				results <- e
				return
			}
			defer tx.Rollback(ctx)
			_, e = Save(ctx, tx, Change{Kind: "quote", ID: "custom_abcdef123456", Row: map[string]any{"text": "جملهٔ هم‌زمان"}, Revision: 1})
			if e == nil {
				e = tx.Commit(ctx)
			}
			results <- e
		}()
	}
	close(start)
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			success++
		} else if e == catalog.ErrConflict {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}
