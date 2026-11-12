package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Haj-amirreza/tg_go/backend/internal/announcements"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strconv"
	"strings"
)

var ErrRevoked = errors.New("delivery eligibility revoked")

type SecureTransport struct {
	Pool *pgxpool.Pool
	Next workqueue.Transport
}

// The outer transport only rechecks eligibility; the network transport remains injectable.
func (s SecureTransport) Send(ctx context.Context, j workqueue.Job) (workqueue.Outcome, error) {
	if s.Next == nil {
		return workqueue.Outcome{Permanent: true}, errors.New("transport unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(j.Payload))
	decoder.UseNumber()
	var d Delivery
	if decoder.Decode(&d) != nil || d.Guard.User < 1 {
		return workqueue.Outcome{Permanent: true}, ErrRevoked
	}
	active, err := activeDelivery(d)
	if err != nil {
		return workqueue.Outcome{Permanent: true}, ErrRevoked
	}
	d = active
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return workqueue.Outcome{RetryAfter: 2e9}, e
	}
	defer tx.Rollback(context.Background())
	var telegram, status, tags string
	var blocked, approved, admin bool
	e = tx.QueryRow(ctx, `SELECT telegram_id,status,delivery_blocked,tags,EXISTS(SELECT 1 FROM student_verifications v WHERE v.user_id=users.id AND v.status='approved'),EXISTS(SELECT 1 FROM admin_telegram_links l JOIN admins a ON a.id=l.admin_id WHERE l.telegram_id=users.telegram_id AND a.active) FROM users WHERE id=$1`, d.Guard.User).Scan(&telegram, &status, &blocked, &tags, &approved, &admin)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return workqueue.Outcome{RetryAfter: 2e9}, e
	}
	if e != nil || status != "active" || blocked {
		return workqueue.Outcome{Permanent: true}, ErrRevoked
	}
	if d.Method != "answerCallbackQuery" {
		chat := fmtNumber(d.Payload["chat_id"])
		if chat != telegram {
			return workqueue.Outcome{Permanent: true}, ErrRevoked
		}
	}
	rows, e := tx.Query(ctx, "SELECT name,value FROM settings WHERE name IN ('bot_state','emergency','modules','maintenance_modules') AND NOT is_secret")
	if e != nil {
		return workqueue.Outcome{RetryAfter: 2e9}, e
	}
	botState := "off"
	emergency := false
	modules := map[string]bool{}
	maintenance := []string{}
	for rows.Next() {
		var name, value string
		if e = rows.Scan(&name, &value); e != nil {
			rows.Close()
			return workqueue.Outcome{RetryAfter: 2e9}, e
		}
		switch name {
		case "bot_state":
			json.Unmarshal([]byte(value), &botState)
		case "emergency":
			var in struct {
				Active bool `json:"active"`
			}
			json.Unmarshal([]byte(value), &in)
			emergency = in.Active
		case "modules":
			json.Unmarshal([]byte(value), &modules)
		case "maintenance_modules":
			json.Unmarshal([]byte(value), &maintenance)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return workqueue.Outcome{RetryAfter: 2e9}, e
	}
	if emergency || botState == "off" && !admin {
		return workqueue.Outcome{Permanent: true}, ErrRevoked
	}
	module := "content"
	if strings.HasPrefix(d.Guard.Route, "u:people") || strings.HasPrefix(d.Guard.Route, "u:professor") {
		module = "directory"
	}
	if strings.HasPrefix(d.Guard.Route, "ubooks:") || d.Guard.Route == "u:library" {
		module = "library"
	}
	if d.Guard.Module != "" {
		module = d.Guard.Module
	}
	if enabled, ok := modules[module]; ok && !enabled && module != "feedback" {
		return workqueue.Outcome{Permanent: true}, ErrRevoked
	}
	if botState == "maintenance" {
		for _, m := range maintenance {
			if m == module {
				return workqueue.Outcome{Permanent: true}, ErrRevoked
			}
		}
	}
	tagList := strings.Fields(strings.NewReplacer(",", " ", "،", " ", ";", " ").Replace(tags))
	if e = catalog.ValidateSnapshot(ctx, tx, d.Guard.Content, d.Guard.Books, tagList, approved); e != nil {
		if errors.Is(e, catalog.ErrMissing) || errors.Is(e, catalog.ErrInvalid) {
			return workqueue.Outcome{Permanent: true}, ErrRevoked
		}
		return workqueue.Outcome{RetryAfter: 2e9}, e
	}
	if strings.HasPrefix(d.Guard.Route, "u:links:group") && !approved {
		return workqueue.Outcome{Permanent: true}, ErrRevoked
	}
	if err := validateDomains(ctx, tx, d.Guard, approved); err != nil {
		if err == catalog.ErrMissing || err == catalog.ErrInvalid {
			return workqueue.Outcome{Permanent: true}, ErrRevoked
		}
		return workqueue.Outcome{RetryAfter: 2e9}, err
	}
	if d.Guard.Dispatch > 0 {
		ok, err := announcements.DeliveryAllowed(ctx, tx, d.Guard.Dispatch, d.Guard.User)
		if err != nil {
			return workqueue.Outcome{RetryAfter: 2e9}, err
		}
		if !ok {
			return workqueue.Outcome{Permanent: true}, ErrRevoked
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return workqueue.Outcome{RetryAfter: 2e9}, e
	}
	return s.Next.Send(ctx, j)
}
func fmtNumber(v any) string {
	switch n := v.(type) {
	case json.Number:
		return n.String()
	case string:
		return n
	case int64:
		return strconv.FormatInt(n, 10)
	}
	return ""
}
