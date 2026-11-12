package workqueue

import (
	"context"
	"github.com/jackc/pgx/v5"
	"time"
)

type InternalHandler func(context.Context, pgx.Tx, Job) error

// Only registered internal handlers execute. Unported Telegram updates stay pending.
func (q Queue) Run(ctx context.Context, handlers map[string]InternalHandler) error {
	kinds := []string{}
	for k := range handlers {
		kinds = append(kinds, k)
	}
	wait := 250 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		op, cancel := context.WithTimeout(ctx, 10*time.Second)
		j, err := q.Claim(op, kinds, 30*time.Second)
		if err == nil && j != nil {
			if j.External {
				_ = q.Fail(op, *j, 0, false, true)
				cancel()
				continue
			}
			tx, e := q.Pool.Begin(op)
			if e == nil {
				e = handlers[j.Kind](op, tx, *j)
				if e == nil {
					e = FinishTx(op, tx, *j)
				}
				if e == nil {
					e = tx.Commit(op)
				} else {
					tx.Rollback(context.Background())
				}
			}
			if e != nil {
				_ = q.Fail(op, *j, 0, false, false)
			}
			cancel()
			wait = 250 * time.Millisecond
			continue
		}
		cancel()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		wait = min(wait*2, 500*time.Millisecond)
	}
}
