package workqueue

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// A transport MUST classify a timeout after transmission as Unknown, never retryable.
type Outcome struct {
	NextPayload        json.RawMessage
	NextDelay          time.Duration
	RetryAfter         time.Duration
	Permanent, Unknown bool
	Deferred           bool
}
type Transport interface {
	Send(context.Context, Job) (Outcome, error)
}

// DeliverOne is injectable; the executable registers network delivery only in explicit sender mode.
func (q Queue) DeliverOne(ctx context.Context, transport Transport) (bool, error) {
	if transport == nil {
		return false, errors.New("transport required")
	}
	j, err := q.Claim(ctx, []string{"telegram.send"}, 30*time.Second)
	if err != nil || j == nil {
		return false, err
	}
	op, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, sendErr := transport.Send(op, *j)
	if len(result.NextPayload) > 0 && sendErr == nil && !result.Permanent && !result.Unknown && result.RetryAfter == 0 {
		if !json.Valid(result.NextPayload) || len(result.NextPayload) > 1<<20 || result.NextDelay < 0 || result.NextDelay > time.Minute {
			return true, q.Fail(ctx, *j, 0, true, false)
		}
		tag, err := q.Pool.Exec(ctx, `UPDATE work_jobs SET state='pending',payload=$3,attempts=0,available_at=now()+$4::interval,lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2 AND state='processing' AND lease_until>now()`, j.ID, j.Token, result.NextPayload, result.NextDelay.String())
		if err == nil && tag.RowsAffected() != 1 {
			err = ErrLease
		}
		return true, err
	}
	if result.Deferred && sendErr == nil {
		tag, err := q.Pool.Exec(ctx, `UPDATE work_jobs SET state='pending',attempts=GREATEST(0,attempts-1),available_at=now()+$3::interval,lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2 AND state='processing' AND lease_until>now()`, j.ID, j.Token, result.RetryAfter.String())
		if err == nil && tag.RowsAffected() != 1 {
			err = ErrLease
		}
		return true, err
	}
	if sendErr != nil || result.Unknown || result.Permanent || result.RetryAfter > 0 {
		if sendErr != nil && result.RetryAfter == 0 && !result.Permanent {
			result.Unknown = true
		}
		return true, q.Fail(ctx, *j, result.RetryAfter, result.Permanent, result.Unknown)
	}
	tx, err := q.Pool.Begin(ctx)
	if err != nil {
		return true, err
	}
	defer tx.Rollback(context.Background())
	if err = FinishTx(ctx, tx, *j); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}
