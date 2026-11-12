package bot

import (
	"context"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/community"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

func guardPairs(values map[string]int) ([]int64, []int, error) {
	if len(values) > 100 {
		return nil, nil, catalog.ErrInvalid
	}
	ids := []int64{}
	versions := []int{}
	for key, version := range values {
		id, e := strconv.ParseInt(key, 10, 64)
		if e != nil || id < 1 || version < 1 {
			return nil, nil, catalog.ErrInvalid
		}
		ids = append(ids, id)
		versions = append(versions, version)
	}
	return ids, versions, nil
}
func validateDomains(ctx context.Context, tx pgx.Tx, g Guard, approved bool) error {
	if g.Leisure > 0 {
		var revision int64
		if e := tx.QueryRow(ctx, `SELECT revision FROM catalog_generations WHERE scope='leisure'`).Scan(&revision); e != nil {
			return e
		}
		if revision != g.Leisure {
			return catalog.ErrMissing
		}
	}
	if g.Approved && !approved {
		return catalog.ErrMissing
	}
	if g.Actor > 0 {
		var allowed bool
		e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins a JOIN roles r ON r.id=a.role_id WHERE a.id=$1 AND a.active AND ($2='' OR a.is_super OR r.permissions::jsonb ? $2) AND (NOT $3 OR EXISTS(SELECT 1 FROM admin_telegram_links l JOIN users u ON u.telegram_id=l.telegram_id WHERE l.admin_id=a.id AND u.id=$4)))`, g.Actor, g.Permission, g.AdminView, g.User).Scan(&allowed)
		if e != nil {
			return e
		}
		if !allowed {
			return catalog.ErrMissing
		}
	}
	if g.Ticket > 0 {
		var ok bool
		e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tickets t WHERE t.id=$1 AND t.user_id=$2 AND ($3::bigint=0 OR EXISTS(SELECT 1 FROM ticket_messages m WHERE m.id=$3 AND m.ticket_id=t.id AND NOT m.internal AND m.admin_id=$4)))`, g.Ticket, g.User, g.TicketMessage, g.Actor).Scan(&ok)
		if e != nil {
			return e
		}
		if !ok {
			return catalog.ErrMissing
		}
	}
	for kind, values := range g.Community {
		table := community.Table(kind)
		if table == "" {
			return catalog.ErrInvalid
		}
		ids, versions, e := guardPairs(values)
		if e != nil {
			return e
		}
		public := "a.state='published'"
		stateField := "state"
		if kind == "channels" {
			public = "a.status='approved'"
			stateField = "status"
		}
		if kind == "market" {
			public += " AND a.expires_at>" + strconv.FormatInt(time.Now().Unix(), 10)
		}
		var count int
		e = tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` a JOIN unnest($1::bigint[],$2::integer[]) AS expected(id,revision) ON a.id=expected.id AND a.revision=expected.revision WHERE a.`+stateField+`<>'deleted' AND (a.user_id=$3 OR (`+public+`))`, ids, versions, g.User).Scan(&count)
		if e != nil {
			return e
		}
		if count != len(ids) {
			return catalog.ErrMissing
		}
	}
	if len(g.Personal) > 0 {
		ids, versions, e := guardPairs(g.Personal)
		if e != nil {
			return e
		}
		var count int
		e = tx.QueryRow(ctx, `SELECT count(*) FROM personal_items p JOIN unnest($1::bigint[],$2::integer[]) AS expected(id,revision) ON p.id=expected.id AND p.revision=expected.revision WHERE p.user_id=$3`, ids, versions, g.User).Scan(&count)
		if e != nil {
			return e
		}
		if count != len(ids) {
			return catalog.ErrMissing
		}
	}
	return nil
}
