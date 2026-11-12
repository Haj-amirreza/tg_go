// Package workqueue keeps durable writes on the same transaction as their outbox jobs.
package workqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLease = errors.New("queue lease lost")

type Queue struct{ Pool *pgxpool.Pool }
type Input struct {
	Key, Kind, Stream string
	Payload           json.RawMessage
	External          bool
	Priority          int
	MaxAttempts       int
	Available         time.Time
}
type Job struct {
	ID       int64
	Kind     string
	Payload  json.RawMessage
	Token    string
	Attempts int
	External bool
}

func Enqueue(ctx context.Context, tx pgx.Tx, in Input) error {
	if len(in.Key) == 0 || len(in.Key) > 200 || len(in.Kind) == 0 || len(in.Kind) > 80 || len(in.Stream) > 100 || !json.Valid(in.Payload) || len(in.Payload) > 1<<20 || in.Priority < -100 || in.Priority > 100 {
		return errors.New("invalid queue input")
	}
	if in.Kind == "telegram.send" {
		in.External = true
	}
	if in.MaxAttempts == 0 {
		in.MaxAttempts = 5
	}
	if in.MaxAttempts < 1 || in.MaxAttempts > 20 {
		return errors.New("invalid retry budget")
	}
	if in.Available.IsZero() {
		in.Available = time.Now()
	}
	_, err := tx.Exec(ctx, `INSERT INTO work_jobs(dedupe_key,kind,payload,stream_key,external_effect,priority,max_attempts,available_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(dedupe_key) DO NOTHING`, in.Key, in.Kind, []byte(in.Payload), in.Stream, in.External, in.Priority, in.MaxAttempts, in.Available)
	return err
}
func (q Queue) Claim(ctx context.Context, kinds []string, lease time.Duration) (*Job, error) {
	if lease < time.Second || lease > 5*time.Minute {
		return nil, errors.New("invalid lease")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)
	row := q.Pool.QueryRow(ctx, `WITH candidate AS (
 SELECT j.id FROM work_jobs j WHERE j.state='pending' AND j.kind=ANY($1) AND j.available_at<=now()
 AND NOT (SELECT paused FROM queue_control WHERE id=true)
 AND (j.stream_key='' OR NOT EXISTS(SELECT 1 FROM work_jobs older WHERE older.stream_key=j.stream_key AND older.id<j.id AND older.state IN ('pending','processing','uncertain')))
 ORDER BY j.priority DESC,j.available_at,j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1)
 UPDATE work_jobs j SET state='processing',attempts=attempts+1,lease_token=$2,lease_until=now()+$3::interval FROM candidate c WHERE j.id=c.id
 RETURNING j.id,j.kind,j.payload,j.attempts,j.external_effect`, kinds, token, lease.String())
	var j Job
	j.Token = token
	err := row.Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts, &j.External)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}
func (q Queue) Heartbeat(ctx context.Context, j Job, lease time.Duration) error {
	if lease < time.Second || lease > 5*time.Minute {
		return ErrLease
	}
	tag, err := q.Pool.Exec(ctx, `UPDATE work_jobs SET lease_until=now()+$3::interval WHERE id=$1 AND lease_token=$2 AND state='processing' AND lease_until>now()`, j.ID, j.Token, lease.String())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLease
	}
	return nil
}

// FinishTx also permits internal handlers to commit their data and completion atomically.
func FinishTx(ctx context.Context, tx pgx.Tx, j Job) error {
	tag, err := tx.Exec(ctx, `UPDATE work_jobs SET state='completed',lease_token=NULL,lease_until=NULL,finished_at=now(),error_code=NULL WHERE id=$1 AND lease_token=$2 AND state='processing' AND lease_until>now()`, j.ID, j.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLease
	}
	return nil
}
func (q Queue) Fail(ctx context.Context, j Job, retryAfter time.Duration, permanent, uncertain bool) error {
	if retryAfter < 0 || retryAfter > 24*time.Hour {
		return errors.New("invalid retry delay")
	}
	state := "pending"
	code := "retry"
	if permanent {
		state = "dead"
		code = "permanent"
	}
	if uncertain {
		state = "uncertain"
		code = "delivery_unknown"
	}
	if retryAfter == 0 {
		retryAfter = time.Second * time.Duration(1<<min(j.Attempts, 8))
	}
	tag, err := q.Pool.Exec(ctx, `UPDATE work_jobs SET state=CASE WHEN $3='pending' AND attempts>=max_attempts THEN 'dead' ELSE $3 END,error_code=$4,available_at=now()+$5::interval,lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2 AND state='processing' AND lease_until>now()`, j.ID, j.Token, state, code, retryAfter.String())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLease
	}
	return nil
}
func (q Queue) Recover(ctx context.Context) error {
	_, err := q.Pool.Exec(ctx, `UPDATE work_jobs SET state=CASE WHEN external_effect THEN 'uncertain' WHEN attempts>=max_attempts THEN 'dead' ELSE 'pending' END,lease_token=NULL,lease_until=NULL,error_code='lease_expired' WHERE id IN (SELECT id FROM work_jobs WHERE state='processing' AND lease_until<now() ORDER BY lease_until FOR UPDATE SKIP LOCKED LIMIT 100)`)
	return err
}
