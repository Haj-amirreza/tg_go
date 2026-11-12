package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"net/http"
	"sync/atomic"
	"time"
)

type traceKey struct{}

var queries, queryNanos, slow, requests, requestQueries, maxRequestQueries, queryErrors atomic.Uint64

type counterKey struct{}

type tracer struct{}

func QueryTracer() pgx.QueryTracer { return tracer{} }

func (tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	if counter, ok := ctx.Value(counterKey{}).(*atomic.Uint64); ok {
		counter.Add(1)
	}
	return context.WithValue(ctx, traceKey{}, time.Now())
}
func (tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if data.Err != nil {
		queryErrors.Add(1)
	}
	if start, ok := ctx.Value(traceKey{}).(time.Time); ok {
		elapsed := time.Since(start)
		queries.Add(1)
		queryNanos.Add(uint64(elapsed))
		if elapsed > 200*time.Millisecond {
			slow.Add(1)
		}
	}
}
func Metrics() map[string]uint64 {
	return map[string]uint64{"queryErrors": queryErrors.Load(), "requests": requests.Load(), "requestQueries": requestQueries.Load(), "maxRequestQueries": maxRequestQueries.Load(), "queries": queries.Load(), "queryNanos": queryNanos.Load(), "slowQueries": slow.Load()}
}

func Track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter := &atomic.Uint64{}
		defer func() {
			n := counter.Load()
			requests.Add(1)
			requestQueries.Add(n)
			for old := maxRequestQueries.Load(); n > old; old = maxRequestQueries.Load() {
				if maxRequestQueries.CompareAndSwap(old, n) {
					break
				}
			}
		}()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), counterKey{}, counter)))
	})
}
