package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
)

var catalogKinds = map[string]bool{"professors": true, "contacts": true, "handles": true, "links": true, "guides": true, "resources": true, "handouts": true, "formulas": true, "faq": true, "news": true, "announcements": true}

func (h *HTTP) catalogList(w http.ResponseWriter, r *http.Request) {
	a, _ := h.actor(r)
	if !a.Can("content") {
		respond(w, 403, map[string]string{"code": "forbidden"})
		return
	}
	kind := r.URL.Query().Get("kind")
	if !catalogKinds[kind] {
		respond(w, 400, map[string]string{"code": "invalid_kind"})
		return
	}
	page, e := strconv.Atoi(r.URL.Query().Get("page"))
	if r.URL.Query().Get("page") == "" {
		page = 0
		e = nil
	}
	if e != nil || page < 0 || page > 10000 {
		respond(w, 400, map[string]string{"code": "invalid_page"})
		return
	}
	rows, e := h.service.pool.Query(r.Context(), `SELECT id::text,kind,title,body,metadata,status,version,published_at,expires_at FROM contents WHERE (kind=$1 OR ($1='contacts' AND kind='handles')) ORDER BY id DESC LIMIT 20 OFFSET $2`, kind, page*20)
	if e != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	defer rows.Close()
	out := []catalog.Content{}
	for rows.Next() {
		var c catalog.Content
		var raw string
		var id string
		e = rows.Scan(&id, &c.Kind, &c.Title, &c.Body, &raw, &c.Status, &c.Version, &c.PublishedAt, &c.ExpiresAt)
		if e != nil || json.Unmarshal([]byte(raw), &c.Metadata) != nil {
			h.fail(w, ErrUnavailable)
			return
		}
		c.ID, _ = strconv.ParseInt(id, 10, 64)
		out = append(out, c)
	}
	if rows.Err() != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	respond(w, 200, out)
}
func (h *HTTP) saveContent(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var c catalog.Content
		if parseCommand(raw, &c) != nil || !catalogKinds[c.Kind] {
			return nil, catalog.ErrInvalid
		}
		return catalog.Save(ctx, tx, c, a.ID, a.Can("publish"))
	})
}
func (h *HTTP) archiveContent(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		items, e := catalog.ParseArchive(raw)
		if e != nil {
			return nil, e
		}
		e = catalog.Archive(ctx, tx, items, false, a.ID, a.Can("publish"))
		return map[string]int{"archived": len(items)}, e
	})
}
func (h *HTTP) bookList(w http.ResponseWriter, r *http.Request) {
	a, _ := h.actor(r)
	if !a.Can("content") {
		respond(w, 403, map[string]string{"code": "forbidden"})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	tx, e := h.service.pool.Begin(r.Context())
	if e != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	defer tx.Rollback(context.Background())
	out, e := catalog.Books(r.Context(), tx, r.URL.Query().Get("query"), r.URL.Query().Get("category"), page)
	if e != nil {
		respond(w, 400, map[string]string{"code": "invalid_search"})
		return
	}
	respond(w, 200, out)
}
func (h *HTTP) saveBook(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in struct {
			catalog.Book
			Deleted bool `json:"deleted"`
		}
		if parseCommand(raw, &in) != nil || !catalog.Text(in.Title, 2000) || !catalog.CategoryOK(in.Category) || in.Details == nil {
			return nil, catalog.ErrInvalid
		}
		details, e := json.Marshal(in.Details)
		if e != nil || len(details) > 40000 {
			return nil, catalog.ErrInvalid
		}
		search := catalog.Normalize(in.Title + " " + string(details))
		if in.ID == 0 {
			sum := sha256.Sum256([]byte(r.Header.Get("Idempotency-Key")))
			e = tx.QueryRow(ctx, `INSERT INTO university_books(source_key,title,details,search_text,category) VALUES($1,$2,$3,$4,$5) RETURNING id`, hex.EncodeToString(sum[:]), in.Title, string(details), search, in.Category).Scan(&in.ID)
			in.Revision = 1
		} else {
			tag, err := tx.Exec(ctx, `UPDATE university_books SET title=$1,details=$2,search_text=$3,category=$4,deleted=$5,revision=revision+1 WHERE id=$6 AND revision=$7`, in.Title, string(details), search, in.Category, in.Deleted, in.ID, in.Revision)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() != 1 {
				return nil, catalog.ErrConflict
			}
			in.Revision++
		}
		if e != nil {
			return nil, e
		}
		_, e = tx.Exec(ctx, "UPDATE catalog_generations SET revision=revision+1 WHERE scope='books'")
		return in, e
	})
}
func (h *HTTP) catalogCategories(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, catalog.Categories)
}

func (h *HTTP) archiveBooks(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		items, e := catalog.ParseArchive(raw)
		if e != nil {
			return nil, e
		}
		e = catalog.Archive(ctx, tx, items, true, a.ID, a.Can("publish"))
		return map[string]int{"archived": len(items)}, e
	})
}
