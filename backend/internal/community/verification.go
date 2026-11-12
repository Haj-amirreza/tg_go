package community

import (
	"context"
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/legacy"
	"github.com/jackc/pgx/v5"
	"time"
)

type Verification struct {
	User     int64             `json:"userId,string"`
	Status   string            `json:"status"`
	Revision int               `json:"revision"`
	Note     string            `json:"note"`
	Details  map[string]string `json:"details"`
}
type Review struct {
	User          int64  `json:"userId,string"`
	Revision      int    `json:"revision"`
	Status        string `json:"status"`
	Note          string `json:"note"`
	Checked       bool   `json:"checked"`
	SecondAccount bool   `json:"secondAccount"`
}

func Verifications(ctx context.Context, tx pgx.Tx, appKey string, page int) ([]Verification, error) {
	if page < 0 || page > 10000 {
		return nil, catalog.ErrInvalid
	}
	crypt, e := legacy.New(appKey)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT user_id,status,revision,review_note,details FROM student_verifications ORDER BY CASE WHEN status='pending' THEN 0 ELSE 1 END,updated_at DESC,user_id DESC LIMIT 11 OFFSET $1`, page*10)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Verification{}
	for rows.Next() {
		var v Verification
		var encrypted string
		if e = rows.Scan(&v.User, &v.Status, &v.Revision, &v.Note, &encrypted); e != nil {
			return nil, e
		}
		raw, e := crypt.Decrypt(encrypted)
		if e != nil {
			return nil, e
		}
		if json.Unmarshal(raw, &v.Details) != nil {
			return nil, catalog.ErrInvalid
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func ReviewVerification(ctx context.Context, tx pgx.Tx, actor int64, in Review) error {
	if in.Status != "approved" && in.Status != "rejected" && in.Status != "revoked" || len([]rune(in.Note)) > 1000 {
		return catalog.ErrInvalid
	}
	var state string
	var revision int
	var hash *string
	e := tx.QueryRow(ctx, `SELECT status,revision,student_hash FROM student_verifications WHERE user_id=$1 FOR UPDATE`, in.User).Scan(&state, &revision, &hash)
	if e == pgx.ErrNoRows {
		return catalog.ErrMissing
	}
	if e != nil {
		return e
	}
	if revision != in.Revision {
		return catalog.ErrConflict
	}
	if in.Status == "revoked" && state != "approved" || in.Status != "revoked" && state != "pending" {
		return catalog.ErrInvalid
	}
	if in.Status == "approved" {
		if !in.Checked || hash == nil || *hash == "" {
			return catalog.ErrInvalid
		}
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, *hash); e != nil {
			return e
		}
		var duplicate bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM student_verifications WHERE student_hash=$1 AND user_id<>$2 AND status='approved')`, *hash, in.User).Scan(&duplicate); e != nil {
			return e
		}
		if duplicate && !in.SecondAccount {
			return catalog.ErrConflict
		}
	}
	_, e = tx.Exec(ctx, `UPDATE student_verifications SET status=$1,revision=revision+1,review_note=$2,reviewed_by=$3,reviewed_at=$4,updated_at=$4 WHERE user_id=$5`, in.Status, in.Note, actor, time.Now().Unix(), in.User)
	return e
}
