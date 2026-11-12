package identity

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"net/http"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

func (h *HTTP) performance(w http.ResponseWriter, r *http.Request) {
	a, _ := h.actor(r)
	if !a.Can("health") {
		respond(w, 403, map[string]string{"code": "forbidden"})
		return
	}
	raw, err := h.service.cache.Get(r.Context(), "queue-counts", time.Second, func(_ctx context.Context) ([]byte, error) {
		rows, err := h.service.pool.Query(_ctx, "SELECT state,count(*) FROM work_jobs GROUP BY state")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		counts := map[string]int64{}
		for rows.Next() {
			var state string
			var n int64
			if err = rows.Scan(&state, &n); err != nil {
				return nil, err
			}
			counts[state] = n
		}
		if rows.Err() != nil {
			return nil, rows.Err()
		}
		return json.Marshal(counts)
	})
	if err != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	var counts map[string]int64
	json.Unmarshal(raw, &counts)
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	pool := h.service.pool.Stat()
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	cpuMicros := usage.Utime.Sec*1000000 + usage.Utime.Usec + usage.Stime.Sec*1000000 + usage.Stime.Usec
	respond(w, 200, map[string]any{"cpuMicros": cpuMicros, "queue": counts, "database": postgres.Metrics(), "pool": map[string]int32{"total": pool.TotalConns(), "idle": pool.IdleConns(), "acquired": pool.AcquiredConns(), "max": pool.MaxConns()}, "cache": h.service.cache.Stats(), "heapBytes": memory.HeapAlloc, "goroutines": runtime.NumGoroutine(), "queueSnapshotSeconds": 1})
}
func (h *HTTP) mutation(w http.ResponseWriter, r *http.Request, permission string) bool {
	a, _ := h.actor(r)
	if permission != "" && !a.Can(permission) {
		respond(w, 403, map[string]string{"code": "forbidden"})
		return false
	}
	if !h.originOK(r) || !hmac.Equal([]byte(a.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) {
		respond(w, 403, map[string]string{"code": "csrf_invalid"})
		return false
	}
	return true
}
func (h *HTTP) clearCache(w http.ResponseWriter, r *http.Request) {
	if !h.mutation(w, r, "health") {
		return
	}
	if h.service.cache.Clear() != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	respond(w, 200, map[string]bool{"cleared": true})
}
func (h *HTTP) pauseQueue(w http.ResponseWriter, r *http.Request) {
	if !h.mutation(w, r, "queue") {
		return
	}
	var in struct {
		Paused *bool `json:"paused"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Paused == nil {
		respond(w, 400, map[string]string{"code": "paused_required"})
		return
	}
	tx, err := h.service.pool.Begin(r.Context())
	if err != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(r.Context(), "UPDATE queue_control SET paused=$1 WHERE id=true", *in.Paused)
	if err == nil {
		a, _ := h.actor(r)
		_, err = tx.Exec(r.Context(), "INSERT INTO identity_audit(admin_id,action,created_at,target) VALUES($1,'queue.pause',$2,$3)", a.ID, time.Now().Unix(), strconv.FormatBool(*in.Paused))
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	respond(w, 200, map[string]bool{"paused": *in.Paused})
}

type QueueItem struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	State    string  `json:"state"`
	Attempts int     `json:"attempts"`
	Error    *string `json:"error"`
}

func (h *HTTP) queueItems(w http.ResponseWriter, r *http.Request) {
	a, _ := h.actor(r)
	if !a.Can("queue") {
		respond(w, 403, map[string]string{"code": "forbidden"})
		return
	}
	after := int64(0)
	if r.URL.Query().Get("after") != "" {
		var err error
		after, err = strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if err != nil || after < 0 {
			respond(w, 400, map[string]string{"code": "invalid_cursor"})
			return
		}
	}
	rows, err := h.service.pool.Query(r.Context(), "SELECT id::text,kind,state,attempts,error_code FROM work_jobs WHERE id>$1 ORDER BY id LIMIT 20", after)
	if err != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	defer rows.Close()
	items := []QueueItem{}
	for rows.Next() {
		var item QueueItem
		if rows.Scan(&item.ID, &item.Kind, &item.State, &item.Attempts, &item.Error) != nil {
			h.fail(w, ErrUnavailable)
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	respond(w, 200, items)
}
func (h *HTTP) retryJob(w http.ResponseWriter, r *http.Request) {
	if !h.mutation(w, r, "queue") {
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &in) {
		return
	}
	id, err := strconv.ParseInt(in.ID, 10, 64)
	if err != nil || id < 1 {
		respond(w, 400, map[string]string{"code": "invalid_id"})
		return
	}
	tx, err := h.service.pool.Begin(r.Context())
	if err != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	defer tx.Rollback(context.Background())
	tag, err := tx.Exec(r.Context(), "UPDATE work_jobs SET state='pending',attempts=0,available_at=now(),error_code=NULL WHERE id=$1 AND state='dead'", id)
	if err != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	if tag.RowsAffected() != 1 {
		respond(w, 409, map[string]string{"code": "not_dead"})
		return
	}
	a, _ := h.actor(r)
	_, err = tx.Exec(r.Context(), "INSERT INTO identity_audit(admin_id,action,created_at,target) VALUES($1,'queue.retry',$2,$3)", a.ID, time.Now().Unix(), in.ID)
	if err != nil || tx.Commit(r.Context()) != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	respond(w, 200, map[string]bool{"retried": true})
}
