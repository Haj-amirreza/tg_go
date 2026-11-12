package workqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func setup(t *testing.T) Queue {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("disposable DB required")
	}
	ctx := context.Background()
	owner, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	seed := make([]byte, 8)
	if _, e = rand.Read(seed); e != nil {
		t.Fatal(e)
	}
	schema := "queue_" + hex.EncodeToString(seed)
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, e = owner.Exec(ctx, "CREATE SCHEMA "+quoted); e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Close(); owner.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); owner.Close() })
	if e = postgres.Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	pool.Exec(ctx, "UPDATE queue_control SET paused=false")
	return Queue{pool}
}
func add(t *testing.T, q Queue, key, stream string, external bool) {
	t.Helper()
	ctx := context.Background()
	tx, e := q.Pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = Enqueue(ctx, tx, Input{Key: key, Kind: "test", Stream: stream, External: external, Payload: json.RawMessage(`{}`)}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestIngressAtomicDedupAndValidation(t *testing.T) {
	q := setup(t)
	h := q.Ingress(strings.Repeat("s", 32))
	for _, body := range []string{`{}`, `{"update_id":-1}`, `{"update_id":1.5}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("X-Telegram-Bot-Api-Secret-Token", strings.Repeat("s", 32))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"update_id":42,"message":{"from":{"id":123}}}`))
		r.Header.Set("X-Telegram-Bot-Api-Secret-Token", strings.Repeat("s", 32))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	var count int
	q.Pool.QueryRow(context.Background(), "SELECT count(*) FROM work_jobs").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate ingress")
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"update_id":43}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("secret bypass")
	}
}
func TestConcurrencyLeaseAndOrdering(t *testing.T) {
	q := setup(t)
	ctx := context.Background()
	add(t, q, "a", "user:1", false)
	add(t, q, "b", "user:1", false)
	var wg sync.WaitGroup
	out := make(chan *Job, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := q.Claim(ctx, []string{"test"}, 30*time.Second)
			if e != nil {
				t.Error(e)
			}
			out <- j
		}()
	}
	wg.Wait()
	close(out)
	var first *Job
	for j := range out {
		if j != nil {
			if first != nil {
				t.Fatal("same stream concurrently claimed")
			}
			first = j
		}
	}
	if first == nil {
		t.Fatal("no job")
	}
	q.Pool.Exec(ctx, "UPDATE work_jobs SET lease_until=now()-interval '1 second' WHERE id=$1", first.ID)
	if e := q.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	j, e := q.Claim(ctx, []string{"test"}, 30*time.Second)
	if e != nil || j == nil || j.ID != first.ID {
		t.Fatal("recovery failed", e)
	}
	tx, _ := q.Pool.Begin(ctx)
	if FinishTx(ctx, tx, *first) != ErrLease {
		t.Fatal("stale token accepted")
	}
	tx.Rollback(ctx)
	tx, _ = q.Pool.Begin(ctx)
	if e = FinishTx(ctx, tx, *j); e != nil {
		t.Fatal(e)
	}
	tx.Commit(ctx)
	next, _ := q.Claim(ctx, []string{"test"}, 30*time.Second)
	if next == nil || next.ID == j.ID {
		t.Fatal("stream blocked")
	}
}
func TestOutboxRollbackPauseRetryAndAmbiguity(t *testing.T) {
	q := setup(t)
	ctx := context.Background()
	tx, _ := q.Pool.Begin(ctx)
	Enqueue(ctx, tx, Input{Key: "rollback", Kind: "test", Payload: json.RawMessage(`{}`)})
	tx.Rollback(ctx)
	j, _ := q.Claim(ctx, []string{"test"}, time.Minute)
	if j != nil {
		t.Fatal("rollback persisted")
	}
	add(t, q, "external", "u", true)
	q.Pool.Exec(ctx, "UPDATE queue_control SET paused=true")
	j, _ = q.Claim(ctx, []string{"test"}, time.Minute)
	if j != nil {
		t.Fatal("pause ignored")
	}
	q.Pool.Exec(ctx, "UPDATE queue_control SET paused=false")
	j, e := q.Claim(ctx, []string{"test"}, time.Minute)
	if e != nil || j == nil {
		t.Fatal(e)
	}
	if e = q.Heartbeat(ctx, *j, time.Minute); e != nil {
		t.Fatal(e)
	}
	if e = q.Fail(ctx, *j, 20*time.Second, false, false); e != nil {
		t.Fatal(e)
	}
	again, _ := q.Claim(ctx, []string{"test"}, time.Minute)
	if again != nil {
		t.Fatal("retry_after ignored")
	}
	q.Pool.Exec(ctx, "UPDATE work_jobs SET available_at=now()-interval '1 second'")
	j, _ = q.Claim(ctx, []string{"test"}, time.Minute)
	q.Pool.Exec(ctx, "UPDATE work_jobs SET lease_until=now()-interval '1 second'")
	q.Recover(ctx)
	var state string
	q.Pool.QueryRow(ctx, "SELECT state FROM work_jobs WHERE id=$1", j.ID).Scan(&state)
	if state != "uncertain" {
		t.Fatal("unsafe external replay")
	}
}

type fakeTransport struct {
	result Outcome
	err    error
}

func (f fakeTransport) Send(context.Context, Job) (Outcome, error) { return f.result, f.err }
func TestInjectedDeliveryAndDeadLetter(t *testing.T) {
	q := setup(t)
	ctx := context.Background()
	tx, _ := q.Pool.Begin(ctx)
	Enqueue(ctx, tx, Input{Key: "send", Kind: "telegram.send", External: true, Payload: json.RawMessage(`{}`)})
	tx.Commit(ctx)
	ok, e := q.DeliverOne(ctx, fakeTransport{Outcome{Unknown: true}, context.DeadlineExceeded})
	if e != nil || !ok {
		t.Fatal(e)
	}
	var state string
	q.Pool.QueryRow(ctx, "SELECT state FROM work_jobs WHERE dedupe_key='send'").Scan(&state)
	if state != "uncertain" {
		t.Fatal("ambiguous delivery retried")
	}
	add(t, q, "internal", "", false)
	q.Pool.Exec(ctx, "UPDATE work_jobs SET max_attempts=1 WHERE dedupe_key='internal'")
	j, _ := q.Claim(ctx, []string{"test"}, time.Minute)
	if e = q.Fail(ctx, *j, 0, false, false); e != nil {
		t.Fatal(e)
	}
	q.Pool.QueryRow(ctx, "SELECT state FROM work_jobs WHERE id=$1", j.ID).Scan(&state)
	if state != "dead" {
		t.Fatal("retry budget ignored")
	}
}
func TestLocalRateDeferralPreservesAttemptBudget(t *testing.T) {
	q := setup(t)
	ctx := context.Background()
	tx, _ := q.Pool.Begin(ctx)
	if e := Enqueue(ctx, tx, Input{Key: "deferred", Kind: "telegram.send", Payload: json.RawMessage(`{}`), MaxAttempts: 1}); e != nil {
		t.Fatal(e)
	}
	tx.Commit(ctx)
	worked, e := q.DeliverOne(ctx, fakeTransport{result: Outcome{Deferred: true, RetryAfter: time.Second}})
	if e != nil || !worked {
		t.Fatal(worked, e)
	}
	var attempts int
	var state string
	q.Pool.QueryRow(ctx, `SELECT state,attempts FROM work_jobs WHERE dedupe_key='deferred'`).Scan(&state, &attempts)
	if state != "pending" || attempts != 0 {
		t.Fatal("deferral consumed retry", state, attempts)
	}
}
