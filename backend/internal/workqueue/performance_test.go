package workqueue

import (
	"context"
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/cache"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestControlledReadLoad(t *testing.T) {
	if os.Getenv("NEXT_LOAD_TEST") != "1" {
		t.Skip("explicit controlled load only")
	}
	q := setup(t)
	ctx := context.Background()
	type result struct {
		Concurrency int     `json:"concurrency"`
		Cached      bool    `json:"cached"`
		Queries     int64   `json:"queries"`
		Errors      int64   `json:"errors"`
		ElapsedMS   float64 `json:"elapsedMs"`
		P95MS       float64 `json:"p95Ms"`
		HeapBytes   uint64  `json:"heapBytes"`
		PoolMax     int32   `json:"poolMax"`
	}
	out := []result{}
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		for _, cached := range []bool{false, true} {
			store, _ := cache.New(8, 4096, "")
			var calls, failed atomic.Int64
			load := func(c context.Context) ([]byte, error) {
				calls.Add(1)
				var value string
				err := q.Pool.QueryRow(c, "SELECT value::text FROM runtime_metadata WHERE name='migration_stage'").Scan(&value)
				return []byte(value), err
			}
			start := make(chan struct{})
			latency := make([]time.Duration, n)
			var wg sync.WaitGroup
			wg.Add(n)
			begin := time.Now()
			for i := 0; i < n; i++ {
				go func(i int) {
					defer wg.Done()
					<-start
					at := time.Now()
					op, cancel := context.WithTimeout(ctx, 60*time.Second)
					defer cancel()
					var e error
					if cached {
						_, e = store.Get(op, "public-test-metadata", time.Minute, load)
					} else {
						_, e = load(op)
					}
					if e != nil {
						failed.Add(1)
					}
					latency[i] = time.Since(at)
				}(i)
			}
			close(start)
			wg.Wait()
			elapsed := time.Since(begin)
			sort.Slice(latency, func(i, j int) bool { return latency[i] < latency[j] })
			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			out = append(out, result{n, cached, calls.Load(), failed.Load(), float64(elapsed.Microseconds()) / 1000, float64(latency[n*95/100].Microseconds()) / 1000, mem.HeapAlloc, q.Pool.Stat().MaxConns()})
			if failed.Load() != 0 {
				t.Error("controlled workload errors")
			}
		}
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	path := os.Getenv("NEXT_LOAD_REPORT")
	if path == "" {
		path = filepath.Join(os.TempDir(), "unibot-read-load.json")
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
