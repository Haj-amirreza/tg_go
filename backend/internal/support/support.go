package support

import (
	"context"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/jackc/pgx/v5"
	"time"
	"unicode/utf8"
)

type Ticket struct {
	ID       int64  `json:"id,string"`
	User     int64  `json:"userId,string"`
	Subject  string `json:"subject"`
	Category string `json:"category"`
	State    string `json:"state"`
	Priority string `json:"priority"`
	Assigned *int64 `json:"assignedTo"`
	Revision int    `json:"revision"`
	Updated  int64  `json:"updatedAt"`
}
type Message struct {
	ID       int64  `json:"id,string"`
	Admin    *int64 `json:"adminId"`
	Body     string `json:"body"`
	Internal bool   `json:"internal"`
	Created  int64  `json:"createdAt"`
}
type Thread struct {
	Ticket   Ticket    `json:"ticket"`
	Messages []Message `json:"messages"`
	Page     int       `json:"page"`
	Pages    int       `json:"pages"`
}

func Limit(ctx context.Context, tx pgx.Tx, scope string, max int, window int64) error {
	now := time.Now().Unix()
	var hits int
	e := tx.QueryRow(ctx, `INSERT INTO student_limits(scope,window_start,hits) VALUES($1,$2,1) ON CONFLICT(scope) DO UPDATE SET hits=CASE WHEN student_limits.window_start+$3<=$2 THEN 1 ELSE student_limits.hits+1 END,window_start=CASE WHEN student_limits.window_start+$3<=$2 THEN $2 ELSE student_limits.window_start END RETURNING hits`, scope, now, window).Scan(&hits)
	if e != nil {
		return e
	}
	if hits > max {
		return catalog.ErrInvalid
	}
	return nil
}
func List(ctx context.Context, tx pgx.Tx, user int64, admin bool, state string, page int) ([]Ticket, error) {
	if page < 0 || page > 10000 {
		return nil, catalog.ErrInvalid
	}
	rows, e := tx.Query(ctx, `SELECT id,user_id,subject,category,state,priority,assigned_to,revision,updated_at FROM tickets WHERE ($1 OR user_id=$2) AND ($3='' OR state=$3) ORDER BY updated_at DESC,id DESC LIMIT 11 OFFSET $4`, admin, user, state, page*10)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Ticket{}
	for rows.Next() {
		var t Ticket
		if e = rows.Scan(&t.ID, &t.User, &t.Subject, &t.Category, &t.State, &t.Priority, &t.Assigned, &t.Revision, &t.Updated); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func Get(ctx context.Context, tx pgx.Tx, id, user int64, admin bool, page int) (Thread, error) {
	out := Thread{Messages: []Message{}}
	t := &out.Ticket
	e := tx.QueryRow(ctx, `SELECT id,user_id,subject,category,state,priority,assigned_to,revision,updated_at FROM tickets WHERE id=$1 AND ($2 OR user_id=$3)`, id, admin, user).Scan(&t.ID, &t.User, &t.Subject, &t.Category, &t.State, &t.Priority, &t.Assigned, &t.Revision, &t.Updated)
	if e == pgx.ErrNoRows {
		return out, catalog.ErrMissing
	}
	if e != nil {
		return out, e
	}
	var total int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM ticket_messages WHERE ticket_id=$1 AND ($2 OR NOT internal)`, id, admin).Scan(&total)
	if e != nil {
		return out, e
	}
	out.Pages = max(1, (total+4)/5)
	if page < 0 {
		page = out.Pages - 1
	}
	out.Page = min(max(0, page), out.Pages-1)
	rows, e := tx.Query(ctx, `SELECT id,admin_id,body,internal,created_at FROM ticket_messages WHERE ticket_id=$1 AND ($2 OR NOT internal) ORDER BY id LIMIT 5 OFFSET $3`, id, admin, out.Page*5)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		if e = rows.Scan(&m.ID, &m.Admin, &m.Body, &m.Internal, &m.Created); e != nil {
			return out, e
		}
		out.Messages = append(out.Messages, m)
	}
	return out, rows.Err()
}
func Create(ctx context.Context, tx pgx.Tx, user int64, body, category string) (int64, error) {
	if !catalog.Text(body, 5000) || utf8.RuneCountInString(body) < 3 || !catalog.Text(category, 100) {
		return 0, catalog.ErrInvalid
	}
	if e := Limit(ctx, tx, "ticket-new:"+idText(user), 5, 86400); e != nil {
		return 0, e
	}
	subject := []rune(body)
	if len(subject) > 120 {
		subject = subject[:120]
	}
	now := time.Now().Unix()
	var id int64
	e := tx.QueryRow(ctx, `INSERT INTO tickets(user_id,subject,category,created_at,updated_at) VALUES($1,$2,$3,$4,$4) RETURNING id`, user, string(subject), category, now).Scan(&id)
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO ticket_messages(ticket_id,body,created_at) VALUES($1,$2,$3)`, id, body, now)
	}
	return id, e
}
func StudentReply(ctx context.Context, tx pgx.Tx, user, id int64, action, body string) error {
	var owner int64
	e := tx.QueryRow(ctx, `SELECT user_id FROM tickets WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, user).Scan(&owner)
	if e == pgx.ErrNoRows {
		return catalog.ErrMissing
	}
	if e != nil {
		return e
	}
	if action != "reply" && action != "close" && action != "reopen" {
		return catalog.ErrInvalid
	}
	if action == "reply" && !catalog.Text(body, 5000) {
		return catalog.ErrInvalid
	}
	if e = Limit(ctx, tx, "ticket-reply:"+idText(user), 20, 3600); e != nil {
		return e
	}
	now := time.Now().Unix()
	if action == "reply" {
		if _, e = tx.Exec(ctx, `INSERT INTO ticket_messages(ticket_id,body,created_at) VALUES($1,$2,$3)`, id, body, now); e != nil {
			return e
		}
	}
	state := "open"
	if action == "close" {
		state = "closed"
	}
	_, e = tx.Exec(ctx, `UPDATE tickets SET state=$1,updated_at=$2,revision=revision+1 WHERE id=$3`, state, now, id)
	return e
}

type Reply struct {
	ID       int64  `json:"id,string"`
	Revision int    `json:"revision"`
	Body     string `json:"body"`
	Internal bool   `json:"internal"`
	State    string `json:"state"`
	Priority string `json:"priority"`
	Assigned *int64 `json:"assignedTo"`
}
type Event struct {
	Ticket, Message, User, Admin int64
	Body, Telegram               string
}

func AdminReply(ctx context.Context, tx pgx.Tx, actor int64, in Reply) (Event, error) {
	event := Event{Ticket: in.ID, Admin: actor, Body: in.Body}
	if in.State != "open" && in.State != "reviewing" && in.State != "answered" && in.State != "closed" || in.Priority != "low" && in.Priority != "normal" && in.Priority != "high" || utf8.RuneCountInString(in.Body) > 5000 {
		return event, catalog.ErrInvalid
	}
	if in.Assigned != nil {
		var ok bool
		e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins a JOIN roles r ON r.id=a.role_id WHERE a.id=$1 AND a.active AND (a.is_super OR r.permissions::jsonb ? 'tickets'))`, *in.Assigned).Scan(&ok)
		if e != nil {
			return event, e
		}
		if !ok {
			return event, catalog.ErrInvalid
		}
	}
	var revision int
	e := tx.QueryRow(ctx, `SELECT t.user_id,u.telegram_id,t.revision FROM tickets t JOIN users u ON u.id=t.user_id WHERE t.id=$1 FOR UPDATE OF t`, in.ID).Scan(&event.User, &event.Telegram, &revision)
	if e == pgx.ErrNoRows {
		return event, catalog.ErrMissing
	}
	if e != nil {
		return event, e
	}
	if revision != in.Revision {
		return event, catalog.ErrConflict
	}
	now := time.Now().Unix()
	_, e = tx.Exec(ctx, `UPDATE tickets SET state=$1,priority=$2,assigned_to=$3,revision=revision+1,updated_at=$4 WHERE id=$5`, in.State, in.Priority, in.Assigned, now, in.ID)
	if e != nil {
		return event, e
	}
	if in.Body != "" {
		e = tx.QueryRow(ctx, `INSERT INTO ticket_messages(ticket_id,admin_id,body,internal,created_at) VALUES($1,$2,$3,$4,$5) RETURNING id`, in.ID, actor, in.Body, in.Internal, now).Scan(&event.Message)
	}
	if in.Internal {
		event.Message = 0
	}
	return event, e
}
