package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/jackc/pgx/v5"
	"io"
	"mime"
	"net/http"
	"regexp"
)

var errDomainForbidden = errors.New("domain forbidden")

func parseCommand(raw []byte, in any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(in) != nil || d.Decode(new(any)) != io.EOF {
		return catalog.ErrInvalid
	}
	return nil
}
func (h *HTTP) command(w http.ResponseWriter, r *http.Request, permission string, fn func(context.Context, pgx.Tx, Actor, []byte) (any, error)) {
	if !h.mutation(w, r, permission) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`).MatchString(key) {
		respond(w, 400, map[string]string{"code": "request_key_required"})
		return
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		respond(w, 415, map[string]string{"code": "json_required"})
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 320<<10))
	if e != nil {
		respond(w, 413, map[string]string{"code": "request_too_large"})
		return
	}
	sum := sha256.Sum256(append([]byte(r.URL.Path+":"), raw...))
	digest := hex.EncodeToString(sum[:])
	a, _ := h.actor(r)
	tx, e := h.service.pool.Begin(r.Context())
	if e != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	defer tx.Rollback(context.Background())
	tag, e := tx.Exec(r.Context(), `INSERT INTO catalog_commands(request_key,actor_id,request_hash,result) VALUES($1,$2,$3,'') ON CONFLICT DO NOTHING`, key, a.ID, digest)
	if e != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	if tag.RowsAffected() == 0 {
		var actor int64
		var hash, result string
		e = tx.QueryRow(r.Context(), "SELECT actor_id,request_hash,result FROM catalog_commands WHERE request_key=$1", key).Scan(&actor, &hash, &result)
		if e != nil || actor != a.ID || hash != digest || result == "" {
			respond(w, 409, map[string]string{"code": "request_key_conflict"})
			return
		}
		if !json.Valid([]byte(result)) {
			h.fail(w, ErrUnavailable)
			return
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, result+"\n")
		return
	}
	value, e := fn(r.Context(), tx, a, raw)
	if e != nil {
		switch {
		case errors.Is(e, errDomainForbidden):
			respond(w, 403, map[string]string{"code": "forbidden"})
		case errors.Is(e, catalog.ErrInvalid):
			respond(w, 400, map[string]string{"code": "invalid_fields"})
		case errors.Is(e, catalog.ErrConflict):
			respond(w, 409, map[string]string{"code": "revision_conflict"})
		case errors.Is(e, catalog.ErrMissing):
			respond(w, 404, map[string]string{"code": "not_found"})
		default:
			h.fail(w, ErrUnavailable)
		}
		return
	}
	result, e := json.Marshal(value)
	if e == nil {
		_, e = tx.Exec(r.Context(), "UPDATE catalog_commands SET result=$1 WHERE request_key=$2", string(result), key)
	}
	if e != nil || tx.Commit(r.Context()) != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	respond(w, 200, value)
}
