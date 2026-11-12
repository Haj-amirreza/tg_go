package identity

import (
	"context"
	"github.com/Haj-amirreza/tg_go/backend/internal/bot"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/community"
	"github.com/Haj-amirreza/tg_go/backend/internal/leisure"
	"github.com/Haj-amirreza/tg_go/backend/internal/support"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
)

func pageNumber(r *http.Request) int {
	n, e := strconv.Atoi(r.URL.Query().Get("page"))
	if r.URL.Query().Get("page") == "" {
		return 0
	}
	if e != nil {
		return -2
	}
	return n
}
func (h *HTTP) ticketList(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "tickets", func(ctx context.Context, tx pgx.Tx) (any, error) {
		return support.List(ctx, tx, 0, true, r.URL.Query().Get("state"), pageNumber(r))
	})
}
func (h *HTTP) ticketThread(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "tickets", func(ctx context.Context, tx pgx.Tx) (any, error) {
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		page := pageNumber(r)
		if r.URL.Query().Get("page") == "" {
			page = -1
		}
		return support.Get(ctx, tx, id, 0, true, page)
	})
}
func (h *HTTP) ticketReply(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "tickets", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in support.Reply
		if parseCommand(raw, &in) != nil {
			return nil, catalog.ErrInvalid
		}
		event, e := support.AdminReply(ctx, tx, a.ID, in)
		if e != nil {
			return nil, e
		}
		if e = bot.QueueTicketReply(ctx, tx, event); e != nil {
			return nil, e
		}
		return support.Get(ctx, tx, in.ID, 0, true, -1)
	})
}
func communityPermission(kind string) string {
	if kind == "channels" {
		return "content"
	}
	return "market"
}
func (h *HTTP) communityList(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	h.domainRead(w, r, communityPermission(kind), func(ctx context.Context, tx pgx.Tx) (any, error) {
		return community.List(ctx, tx, kind, 0, false, true, r.URL.Query().Get("state"), pageNumber(r))
	})
}
func (h *HTTP) communitySave(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in community.Ad
		if parseCommand(raw, &in) != nil {
			return nil, catalog.ErrInvalid
		}
		if !a.Can(communityPermission(in.Kind)) {
			return nil, errDomainForbidden
		}
		return community.Save(ctx, tx, in, 0, true)
	})
}
func (h *HTTP) communityArchive(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in struct {
			Kind  string             `json:"kind"`
			Items []catalog.Revision `json:"items"`
		}
		if parseCommand(raw, &in) != nil {
			return nil, catalog.ErrInvalid
		}
		if !a.Can(communityPermission(in.Kind)) {
			return nil, errDomainForbidden
		}
		return map[string]int{"archived": len(in.Items)}, community.RemoveBatch(ctx, tx, in.Kind, in.Items)
	})
}
func (h *HTTP) leisureGet(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "content", func(ctx context.Context, tx pgx.Tx) (any, error) {
		records, revision, e := leisure.Load(ctx, tx, nil)
		if e != nil {
			return nil, e
		}
		return map[string]any{"records": records, "revision": revision}, nil
	})
}
func (h *HTTP) leisureSave(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in leisure.Change
		if parseCommand(raw, &in) != nil {
			return nil, catalog.ErrInvalid
		}
		revision, e := leisure.Save(ctx, tx, in)
		return map[string]int64{"revision": revision}, e
	})
}
func (h *HTTP) verificationList(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "users", func(ctx context.Context, tx pgx.Tx) (any, error) {
		return community.Verifications(ctx, tx, h.service.key, pageNumber(r))
	})
}
func (h *HTTP) verificationReview(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "users", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in community.Review
		if parseCommand(raw, &in) != nil {
			return nil, catalog.ErrInvalid
		}
		e := community.ReviewVerification(ctx, tx, a.ID, in)
		return map[string]bool{"saved": e == nil}, e
	})
}
func (h *HTTP) ticketAssignees(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "tickets", func(ctx context.Context, tx pgx.Tx) (any, error) {
		rows, e := tx.Query(ctx, `SELECT a.id::text,a.name FROM admins a JOIN roles r ON r.id=a.role_id WHERE a.active AND (a.is_super OR r.permissions::jsonb ? 'tickets') ORDER BY a.name,a.id LIMIT 200`)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []map[string]string{}
		for rows.Next() {
			var id, name string
			if e = rows.Scan(&id, &name); e != nil {
				return nil, e
			}
			out = append(out, map[string]string{"id": id, "name": name})
		}
		return out, rows.Err()
	})
}

func (h *HTTP) leisureArchive(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in struct {
			Kind     string   `json:"kind"`
			IDs      []string `json:"ids"`
			Revision int64    `json:"revision"`
		}
		if parseCommand(raw, &in) != nil || len(in.IDs) < 1 || len(in.IDs) > 100 {
			return nil, catalog.ErrInvalid
		}
		changes := map[string]map[string]any{}
		for _, id := range in.IDs {
			if _, exists := changes[id]; exists {
				return nil, catalog.ErrInvalid
			}
			changes[id] = nil
		}
		revision, e := leisure.Apply(ctx, tx, in.Kind, changes, in.Revision)
		return map[string]int64{"revision": revision}, e
	})
}
