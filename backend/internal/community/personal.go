package community

import (
	"context"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/jackc/pgx/v5"
	"regexp"
	"time"
)

type Personal struct {
	ID       int64  `json:"id,string"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Day      string `json:"day"`
	Clock    string `json:"clock"`
	Done     bool   `json:"done"`
	Revision int    `json:"revision"`
}

func PersonalSave(ctx context.Context, tx pgx.Tx, user int64, p Personal) (Personal, error) {
	day, e := time.Parse("2006-01-02", p.Day)
	if e != nil || day.Format("2006-01-02") != p.Day || p.Kind != "schedule" && p.Kind != "meal" && p.Kind != "task" || !catalog.Text(p.Title, 200) || len([]rune(p.Body)) > 2000 || p.Clock != "" && !regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`).MatchString(p.Clock) {
		return p, catalog.ErrInvalid
	}
	var approved bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM student_verifications WHERE user_id=$1 AND status='approved')`, user).Scan(&approved)
	if e != nil {
		return p, e
	}
	if !approved {
		return p, catalog.ErrInvalid
	}
	now := time.Now().Unix()
	if p.ID == 0 {
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM personal_items WHERE user_id=$1`, user).Scan(&count); e != nil {
			return p, e
		}
		if count >= 2000 {
			return p, catalog.ErrInvalid
		}
		e = tx.QueryRow(ctx, `INSERT INTO personal_items(user_id,kind,title,body,day,clock,done,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8) RETURNING id,revision`, user, p.Kind, p.Title, p.Body, day, p.Clock, p.Done, now).Scan(&p.ID, &p.Revision)
	} else {
		e = tx.QueryRow(ctx, `UPDATE personal_items SET kind=$1,title=$2,body=$3,day=$4,clock=$5,done=$6,updated_at=$7,revision=revision+1 WHERE id=$8 AND user_id=$9 AND revision=$10 RETURNING revision`, p.Kind, p.Title, p.Body, day, p.Clock, p.Done, now, p.ID, user, p.Revision).Scan(&p.Revision)
		if e == pgx.ErrNoRows {
			e = catalog.ErrConflict
		}
	}
	return p, e
}
func PersonalList(ctx context.Context, tx pgx.Tx, user int64, day, kind string, page int) ([]Personal, error) {
	if page < 0 || page > 10000 {
		return nil, catalog.ErrInvalid
	}
	rows, e := tx.Query(ctx, `SELECT id,kind,title,body,day::text,clock,done,revision FROM personal_items WHERE user_id=$1 AND day=$2::date AND kind=$3 ORDER BY clock,id LIMIT 11 OFFSET $4`, user, day, kind, page*10)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Personal{}
	for rows.Next() {
		var p Personal
		if e = rows.Scan(&p.ID, &p.Kind, &p.Title, &p.Body, &p.Day, &p.Clock, &p.Done, &p.Revision); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
