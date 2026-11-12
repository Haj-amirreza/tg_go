package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Uploads remain private. Documents refer only to database IDs, never paths or public URLs.
func (h *HTTP) uploadFile(w http.ResponseWriter, r *http.Request) {
	if !h.mutation(w, r, "files") {
		return
	}
	if h.filesRoot == "" {
		respond(w, 503, map[string]string{"code": "private_storage_unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 21<<20)
	reader, e := r.MultipartReader()
	if e != nil {
		respond(w, 400, map[string]string{"code": "multipart_required"})
		return
	}
	part, e := reader.NextPart()
	if e != nil || part.FormName() != "file" {
		respond(w, 400, map[string]string{"code": "file_required"})
		return
	}
	name := filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
	if len(name) > 255 || name == "." || name == "" {
		respond(w, 400, map[string]string{"code": "invalid_name"})
		return
	}
	if e = os.MkdirAll(h.filesRoot, 0700); e != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	storage := randomFileName()
	path := filepath.Join(h.filesRoot, storage)
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	keep := false
	defer func() {
		f.Close()
		if !keep {
			os.Remove(path)
		}
	}()
	hash := sha256.New()
	head := make([]byte, 512)
	n, e := io.ReadFull(part, head)
	if e != nil && e != io.ErrUnexpectedEOF {
		respond(w, 400, map[string]string{"code": "empty_file"})
		return
	}
	head = head[:n]
	mime := http.DetectContentType(head)
	allowed := mime == "image/jpeg" || mime == "image/png" || mime == "image/gif" || mime == "image/webp" || mime == "audio/mpeg" || mime == "audio/ogg" || mime == "audio/wave" || mime == "application/pdf"
	if mime == "application/ogg" {
		mime = "audio/ogg"
		allowed = true
	}
	if !allowed {
		respond(w, 400, map[string]string{"code": "unsupported_media"})
		return
	}
	if _, e = io.MultiWriter(f, hash).Write(head); e != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	size, e := io.Copy(io.MultiWriter(f, hash), io.LimitReader(part, (20<<20)+1-int64(n)))
	size += int64(n)
	if e != nil || size > 20<<20 {
		respond(w, 413, map[string]string{"code": "file_too_large"})
		return
	}
	if f.Sync() != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	var id int64
	e = h.service.pool.QueryRow(r.Context(), `INSERT INTO files(original_name,storage_name,mime,size,checksum,created_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, name, storage, mime, size, hex.EncodeToString(hash.Sum(nil)), time.Now().Unix()).Scan(&id)
	if e != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	keep = true
	respond(w, 200, map[string]any{"id": strconv.FormatInt(id, 10), "name": name, "mime": mime, "size": size})
}
func randomFileName() string { s, _ := randomToken(); return s }

func (h *HTTP) mediaFile(w http.ResponseWriter, r *http.Request) {
	a, _ := h.actor(r)
	if !a.Can("content") && !a.Can("files") {
		respond(w, 403, map[string]string{"code": "forbidden"})
		return
	}
	id, e := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if e != nil || id < 1 || h.filesRoot == "" {
		http.NotFound(w, r)
		return
	}
	var storage, mime, name string
	e = h.service.pool.QueryRow(r.Context(), `SELECT storage_name,mime,original_name FROM files WHERE id=$1`, id).Scan(&storage, &mime, &name)
	if e != nil || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(storage) {
		http.NotFound(w, r)
		return
	}
	f, e := os.Open(filepath.Join(h.filesRoot, storage))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, name, stat.ModTime(), f)
}
