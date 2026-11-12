package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/announcements"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/rich"
	"github.com/jackc/pgx/v5"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func (h *HTTP) domainRead(w http.ResponseWriter, r *http.Request, permission string, fn func(context.Context, pgx.Tx) (any, error)) {
	a, _ := h.actor(r)
	if !a.Can(permission) {
		respond(w, 403, map[string]string{"code": "forbidden"})
		return
	}
	tx, e := h.service.pool.Begin(r.Context())
	if e != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	defer tx.Rollback(context.Background())
	out, e := fn(r.Context(), tx)
	if e != nil {
		respond(w, 400, map[string]string{"code": "unavailable_item"})
		return
	}
	respond(w, 200, out)
}
func (h *HTTP) noticeCategories(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "content", func(ctx context.Context, tx pgx.Tx) (any, error) { return announcements.Categories(ctx, tx, 0, true) })
}
func (h *HTTP) noticeCategorySave(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in announcements.Category
		if parseCommand(raw, &in) != nil {
			return nil, catalog.ErrInvalid
		}
		return announcements.SaveCategory(ctx, tx, in)
	})
}
func (h *HTTP) noticePreview(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "campaigns", func(ctx context.Context, tx pgx.Tx) (any, error) {
		a, _ := h.actor(r)
		if !a.Can("publish") {
			return nil, catalog.ErrInvalid
		}
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		return announcements.Preview(ctx, tx, id, r.URL.Query().Get("mode"))
	})
}
func (h *HTTP) noticeDispatch(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "campaigns", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		if !a.Can("publish") {
			return nil, catalog.ErrInvalid
		}
		var p announcements.Plan
		if parseCommand(raw, &p) != nil {
			return nil, catalog.ErrInvalid
		}
		id, e := announcements.Dispatch(ctx, tx, p, a.ID)
		return map[string]string{"dispatchId": strconv.FormatInt(id, 10)}, e
	})
}
func (h *HTTP) noticeCancel(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "campaigns", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in struct {
			ID int64 `json:"id,string"`
		}
		if parseCommand(raw, &in) != nil {
			return nil, catalog.ErrInvalid
		}
		tag, e := tx.Exec(ctx, `UPDATE announcement_dispatches SET state='cancelled' WHERE id=$1 AND state<>'cancelled'`, in.ID)
		return map[string]int64{"cancelled": tag.RowsAffected()}, e
	})
}
func (h *HTTP) noticeDispatches(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "campaigns", func(ctx context.Context, tx pgx.Tx) (any, error) {
		rows, e := tx.Query(ctx, `SELECT d.id,c.title,d.audience,d.cursor_id,d.max_user_id,d.state FROM announcement_dispatches d JOIN contents c ON c.id=d.content_id ORDER BY d.id DESC LIMIT 30`)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, cursor, maxUser int64
			var title, mode, state string
			if e = rows.Scan(&id, &title, &mode, &cursor, &maxUser, &state); e != nil {
				return nil, e
			}
			out = append(out, map[string]any{"id": strconv.FormatInt(id, 10), "title": title, "mode": mode, "cursor": strconv.FormatInt(cursor, 10), "maxUser": strconv.FormatInt(maxUser, 10), "state": state})
		}
		return out, rows.Err()
	})
}

type StudioInput struct {
	Content    catalog.Content `json:"content"`
	Document   *rich.Document  `json:"document"`
	CategoryID int64           `json:"categoryId,string"`
	Mode       string          `json:"mode"`
}

func (h *HTTP) studioSave(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in StudioInput
		if parseCommand(raw, &in) != nil || !catalogKinds[in.Content.Kind] {
			return nil, catalog.ErrInvalid
		}
		if in.Document != nil {
			if e := rich.Validate(*in.Document); e != nil {
				return nil, e
			}
			if e := rich.ValidateFiles(ctx, tx, *in.Document); e != nil {
				return nil, e
			}
		}
		for _, b := range in.DocumentOrEmpty().Blocks {
			if b.Type == "audio" && b.Voice {
				var storage, mime string
				if e := tx.QueryRow(ctx, `SELECT storage_name,mime FROM files WHERE id=$1`, b.File).Scan(&storage, &mime); e != nil {
					return nil, e
				}
				if mime != "audio/ogg" || h.filesRoot == "" || filepath.Base(storage) != storage {
					return nil, catalog.ErrInvalid
				}
				file, e := os.Open(filepath.Join(h.filesRoot, storage))
				if e != nil {
					return nil, catalog.ErrInvalid
				}
				head := make([]byte, 4096)
				n, _ := file.Read(head)
				file.Close()
				if !bytes.Contains(head[:n], []byte("OpusHead")) {
					return nil, catalog.ErrInvalid
				}
			}
		}
		c, e := catalog.Save(ctx, tx, in.Content, a.ID, a.Can("publish"))
		if e != nil {
			return nil, e
		}
		if c.Kind == "announcements" {
			if e = announcements.Assign(ctx, tx, c.ID, in.CategoryID, in.Mode); e != nil {
				return nil, e
			}
		}
		if in.Document != nil {
			document, _ := json.Marshal(in.Document)
			_, e = tx.Exec(ctx, `INSERT INTO advanced_contents(content_id,document,updated_at) VALUES($1,$2,$3) ON CONFLICT(content_id) DO UPDATE SET document=EXCLUDED.document,revision=advanced_contents.revision+1,updated_at=EXCLUDED.updated_at`, c.ID, string(document), time.Now().Unix())
			if e == nil {
				_, e = tx.Exec(ctx, `INSERT INTO advanced_revisions(content_id,version,document,created_at) VALUES($1,$2,$3,$4)`, c.ID, c.Version, string(document), time.Now().Unix())
			}
			if e != nil {
				return nil, e
			}
			_, e = tx.Exec(ctx, `DELETE FROM advanced_files WHERE content_id=$1`, c.ID)
			if e != nil {
				return nil, e
			}
			fileRows := [][]any{}
			for id := range rich.Files(*in.Document) {
				fileRows = append(fileRows, []any{c.ID, id})
			}
			if len(fileRows) > 0 {
				_, e = tx.CopyFrom(ctx, pgx.Identifier{"advanced_files"}, []string{"content_id", "file_id"}, pgx.CopyFromRows(fileRows))
			}
		} else {
			_, e = tx.Exec(ctx, `DELETE FROM advanced_contents WHERE content_id=$1`, c.ID)
		}
		return c, e
	})
}
func (h *HTTP) studioGet(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "content", func(ctx context.Context, tx pgx.Tx) (any, error) {
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		c, e := catalog.Get(ctx, tx, id)
		if e != nil {
			return nil, e
		}
		out := map[string]any{"content": c, "document": nil, "categoryId": "0", "mode": "bank"}
		var raw string
		e = tx.QueryRow(ctx, `SELECT document FROM advanced_contents WHERE content_id=$1`, id).Scan(&raw)
		if e == nil {
			out["document"] = json.RawMessage(raw)
		} else if e != pgx.ErrNoRows {
			return nil, e
		}
		if c.Kind == "announcements" {
			var category int64
			var mode string
			e = tx.QueryRow(ctx, `SELECT COALESCE(category_id,0),delivery_mode FROM announcement_items WHERE content_id=$1`, id).Scan(&category, &mode)
			if e == nil {
				out["categoryId"] = strconv.FormatInt(category, 10)
				out["mode"] = mode
			} else if e != pgx.ErrNoRows {
				return nil, e
			}
		}
		return out, nil
	})
}
func (h *HTTP) studioTemplates(w http.ResponseWriter, r *http.Request) {
	h.domainRead(w, r, "content", func(ctx context.Context, tx pgx.Tx) (any, error) {
		rows, e := tx.Query(ctx, `SELECT id,title,kind,document FROM content_templates ORDER BY id DESC LIMIT 100`)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int64
			var title, kind, document string
			if e = rows.Scan(&id, &title, &kind, &document); e != nil {
				return nil, e
			}
			out = append(out, map[string]any{"id": strconv.FormatInt(id, 10), "title": title, "kind": kind, "document": json.RawMessage(document)})
		}
		return out, rows.Err()
	})
}
func (h *HTTP) studioTemplateSave(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in struct {
			Title    string        `json:"title"`
			Kind     string        `json:"kind"`
			Document rich.Document `json:"document"`
		}
		if parseCommand(raw, &in) != nil || !catalog.Text(in.Title, 200) || !catalogKinds[in.Kind] || rich.Validate(in.Document) != nil {
			return nil, catalog.ErrInvalid
		}
		if e := rich.ValidateFiles(ctx, tx, in.Document); e != nil {
			return nil, e
		}
		doc, _ := json.Marshal(in.Document)
		var id int64
		e := tx.QueryRow(ctx, `INSERT INTO content_templates(title,kind,document,created_by,created_at) VALUES($1,$2,$3,$4,$5) RETURNING id`, in.Title, in.Kind, string(doc), a.ID, time.Now().Unix()).Scan(&id)
		return map[string]string{"id": strconv.FormatInt(id, 10)}, e
	})
}
func (h *HTTP) studioTemplateDelete(w http.ResponseWriter, r *http.Request) {
	h.command(w, r, "content", func(ctx context.Context, tx pgx.Tx, a Actor, raw []byte) (any, error) {
		var in struct {
			ID int64 `json:"id,string"`
		}
		if parseCommand(raw, &in) != nil {
			return nil, catalog.ErrInvalid
		}
		tag, e := tx.Exec(ctx, `DELETE FROM content_templates WHERE id=$1`, in.ID)
		return map[string]int64{"deleted": tag.RowsAffected()}, e
	})
}

func (in StudioInput) DocumentOrEmpty() rich.Document {
	if in.Document == nil {
		return rich.Document{}
	}
	return *in.Document
}
