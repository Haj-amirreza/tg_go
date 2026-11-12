// Package announcements owns category subscriptions and bounded broadcast plans.
package announcements

import (
	"context"
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

const MaxAudience = 1500

type Category struct {
	ID        int64  `json:"id,string"`
	Name      string `json:"name"`
	Active    bool   `json:"active"`
	Revision  int    `json:"revision"`
	Following bool   `json:"following"`
}
type Plan struct {
	ContentID  int64  `json:"contentId,string"`
	Version    int    `json:"version"`
	CategoryID int64  `json:"categoryId,string"`
	Mode       string `json:"mode"`
	Count      int    `json:"count"`
	MaxUser    int64  `json:"maxUser,string"`
}

func Categories(ctx context.Context, tx pgx.Tx, user int64, all bool) ([]Category, error) {
	rows, e := tx.Query(ctx, `SELECT c.id,c.name,c.active,c.revision,EXISTS(SELECT 1 FROM announcement_subscriptions s WHERE s.category_id=c.id AND s.user_id=$1) FROM announcement_categories c WHERE $2 OR c.active OR EXISTS(SELECT 1 FROM announcement_subscriptions s WHERE s.category_id=c.id AND s.user_id=$1) ORDER BY name,id`, user, all)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if e = rows.Scan(&c.ID, &c.Name, &c.Active, &c.Revision, &c.Following); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func SaveCategory(ctx context.Context, tx pgx.Tx, c Category) (Category, error) {
	if !catalog.Text(c.Name, 100) {
		return c, catalog.ErrInvalid
	}
	var e error
	if c.ID == 0 {
		e = tx.QueryRow(ctx, `INSERT INTO announcement_categories(name,active,created_at) VALUES($1,$2,$3) RETURNING id,revision`, c.Name, c.Active, time.Now().Unix()).Scan(&c.ID, &c.Revision)
	} else {
		e = tx.QueryRow(ctx, `UPDATE announcement_categories SET name=$1,active=$2,revision=revision+1 WHERE id=$3 AND revision=$4 RETURNING revision`, c.Name, c.Active, c.ID, c.Revision).Scan(&c.Revision)
		if e == pgx.ErrNoRows {
			e = catalog.ErrConflict
		}
	}
	return c, e
}
func Follow(ctx context.Context, tx pgx.Tx, user, category int64, on bool) error {
	if !on {
		_, e := tx.Exec(ctx, `DELETE FROM announcement_subscriptions WHERE user_id=$1 AND category_id=$2`, user, category)
		return e
	}
	tag, e := tx.Exec(ctx, `INSERT INTO announcement_subscriptions SELECT $1,id,$3 FROM announcement_categories WHERE id=$2 AND active ON CONFLICT DO NOTHING`, user, category, time.Now().Unix())
	if e == nil && tag.RowsAffected() == 0 {
		var ok bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM announcement_subscriptions WHERE user_id=$1 AND category_id=$2)`, user, category).Scan(&ok)
		if e == nil && !ok {
			return catalog.ErrMissing
		}
	}
	return e
}
func Assign(ctx context.Context, tx pgx.Tx, id, category int64, mode string) error {
	if mode != "bank" && mode != "all" && mode != "followers" || mode == "followers" && category < 1 {
		return catalog.ErrInvalid
	}
	if category > 0 {
		var ok bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM announcement_categories WHERE id=$1 AND active)`, category).Scan(&ok); e != nil {
			return e
		}
		if !ok {
			return catalog.ErrMissing
		}
	}
	_, e := tx.Exec(ctx, `INSERT INTO announcement_items(content_id,category_id,delivery_mode) VALUES($1,NULLIF($2,0),$3) ON CONFLICT(content_id) DO UPDATE SET category_id=EXCLUDED.category_id,delivery_mode=EXCLUDED.delivery_mode`, id, category, mode)
	return e
}
func Preview(ctx context.Context, tx pgx.Tx, id int64, mode string) (Plan, error) {
	p := Plan{ContentID: id, Mode: mode}
	if mode != "bank" && mode != "all" && mode != "followers" {
		return p, catalog.ErrInvalid
	}
	c, e := catalog.Get(ctx, tx, id)
	if e != nil {
		return p, e
	}
	if c.Kind != "announcements" || !catalog.Visible(c, nil, true, time.Now().Unix()) || catalog.MetaText(c.Metadata, "audience_tag") != "" {
		return p, catalog.ErrInvalid
	}
	p.Version = c.Version
	e = tx.QueryRow(ctx, `SELECT COALESCE((SELECT category_id FROM announcement_items WHERE content_id=$1),0),COALESCE(MAX(id),0) FROM users`, id).Scan(&p.CategoryID, &p.MaxUser)
	if e != nil {
		return p, e
	}
	if mode == "bank" {
		return p, nil
	}
	if mode == "followers" && p.CategoryID == 0 {
		return p, catalog.ErrInvalid
	}
	e = tx.QueryRow(ctx, `SELECT count(*) FROM users u WHERE u.id<=$1 AND u.status='active' AND NOT u.delivery_blocked AND ($2='all' OR EXISTS(SELECT 1 FROM announcement_subscriptions s JOIN announcement_categories c ON c.id=s.category_id AND c.active WHERE s.user_id=u.id AND s.category_id=$3)) `, p.MaxUser, mode, p.CategoryID).Scan(&p.Count)
	if e == nil && p.Count > MaxAudience {
		return p, catalog.ErrInvalid
	}
	return p, e
}
func Dispatch(ctx context.Context, tx pgx.Tx, p Plan, actor int64) (int64, error) {
	// Serialize version and membership snapshot. A stale preview never silently changes audience.
	var locked int64
	if e := tx.QueryRow(ctx, `SELECT id FROM contents WHERE id=$1 FOR UPDATE`, p.ContentID).Scan(&locked); e != nil {
		return 0, e
	}
	fresh, e := Preview(ctx, tx, p.ContentID, p.Mode)
	if e != nil {
		return 0, e
	}
	if fresh != p {
		return 0, catalog.ErrConflict
	}
	if p.Mode == "bank" {
		return 0, nil
	}
	var id int64
	e = tx.QueryRow(ctx, `INSERT INTO announcement_dispatches(content_id,content_version,category_id,audience,max_user_id,created_by,created_at) VALUES($1,$2,NULLIF($3,0),$4,$5,$6,$7) ON CONFLICT(content_id,content_version,audience) DO NOTHING RETURNING id`, p.ContentID, p.Version, p.CategoryID, p.Mode, p.MaxUser, actor, time.Now().Unix()).Scan(&id)
	if e == pgx.ErrNoRows {
		return 0, catalog.ErrConflict
	}
	if e != nil {
		return 0, e
	}
	raw, _ := json.Marshal(id)
	e = workqueue.Enqueue(ctx, tx, workqueue.Input{Key: "notice:expand:" + strconv.FormatInt(id, 10) + ":0", Kind: "notice.expand", Payload: raw})
	return id, e
}

// DeliveryAllowed is deliberately fresh, including the publisher's current role.
func DeliveryAllowed(ctx context.Context, tx pgx.Tx, id, user int64) (bool, error) {
	var ok bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM announcement_dispatches d JOIN contents c ON c.id=d.content_id JOIN admins a ON a.id=d.created_by JOIN roles r ON r.id=a.role_id WHERE d.id=$1 AND d.state<>'cancelled' AND c.status='published' AND c.version=d.content_version AND a.active AND (a.is_super OR (r.permissions::jsonb ? 'publish' AND r.permissions::jsonb ? 'campaigns')) AND (d.audience='all' OR EXISTS(SELECT 1 FROM announcement_subscriptions s JOIN announcement_categories ac ON ac.id=s.category_id AND ac.active WHERE s.user_id=$2 AND s.category_id=d.category_id)))`, id, user).Scan(&ok)
	return ok, e
}
