package identity

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const sessionCookie = "unibot_session"
const preCookie = "unibot_login_csrf"

type actorKey struct{}

type HTTP struct {
	service   *Service
	filesRoot string
	origin    string
	secure    bool
	mux       *http.ServeMux
}

func NewHTTP(s *Service, origin string) (*HTTP, error) {
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("invalid APP_ORIGIN")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return nil, errors.New("HTTP auth is limited to loopback")
	}
	h := &HTTP{filesRoot: os.Getenv("PRIVATE_FILE_DIR"), service: s, origin: origin, secure: u.Scheme == "https", mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /api/v1/admin/files/media", h.mediaFile)
	h.mux.HandleFunc("POST /api/v1/admin/files/upload", h.uploadFile)
	h.mux.HandleFunc("GET /api/v1/auth/csrf", h.preflight)
	h.mux.HandleFunc("POST /api/v1/auth/login", h.login)
	h.mux.HandleFunc("POST /api/v1/auth/telegram", h.telegram)
	h.mux.HandleFunc("GET /api/v1/admin/session", h.current)
	h.mux.HandleFunc("GET /api/v1/admin/performance", h.performance)
	h.mux.HandleFunc("GET /api/v1/admin/queue/jobs", h.queueItems)
	h.mux.HandleFunc("POST /api/v1/admin/queue/retry", h.retryJob)
	h.mux.HandleFunc("POST /api/v1/admin/cache/clear", h.clearCache)
	h.mux.HandleFunc("POST /api/v1/admin/queue/control", h.pauseQueue)
	h.mux.HandleFunc("POST /api/v1/admin/logout", h.logout)
	h.mux.HandleFunc("GET /api/v1/admin/roles", h.roles)
	h.mux.HandleFunc("GET /api/v1/admin/catalog", h.catalogList)
	h.mux.HandleFunc("POST /api/v1/admin/catalog/save", h.saveContent)
	h.mux.HandleFunc("POST /api/v1/admin/catalog/archive", h.archiveContent)
	h.mux.HandleFunc("GET /api/v1/admin/books", h.bookList)
	h.mux.HandleFunc("POST /api/v1/admin/books/save", h.saveBook)
	h.mux.HandleFunc("POST /api/v1/admin/books/archive", h.archiveBooks)
	h.mux.HandleFunc("GET /api/v1/admin/books/categories", h.catalogCategories)
	h.mux.HandleFunc("GET /api/v1/admin/notices/categories", h.noticeCategories)
	h.mux.HandleFunc("POST /api/v1/admin/notices/category", h.noticeCategorySave)
	h.mux.HandleFunc("GET /api/v1/admin/notices/preview", h.noticePreview)
	h.mux.HandleFunc("POST /api/v1/admin/notices/dispatch", h.noticeDispatch)
	h.mux.HandleFunc("GET /api/v1/admin/notices/dispatches", h.noticeDispatches)
	h.mux.HandleFunc("POST /api/v1/admin/notices/cancel", h.noticeCancel)
	h.mux.HandleFunc("POST /api/v1/admin/studio/save", h.studioSave)
	h.mux.HandleFunc("GET /api/v1/admin/studio", h.studioGet)
	h.mux.HandleFunc("GET /api/v1/admin/studio/templates", h.studioTemplates)
	h.mux.HandleFunc("POST /api/v1/admin/studio/template", h.studioTemplateSave)
	h.mux.HandleFunc("POST /api/v1/admin/studio/template/delete", h.studioTemplateDelete)
	h.mux.HandleFunc("GET /api/v1/admin/tickets/assignees", h.ticketAssignees)
	h.mux.HandleFunc("GET /api/v1/admin/tickets", h.ticketList)
	h.mux.HandleFunc("GET /api/v1/admin/tickets/thread", h.ticketThread)
	h.mux.HandleFunc("POST /api/v1/admin/tickets/reply", h.ticketReply)
	h.mux.HandleFunc("GET /api/v1/admin/community", h.communityList)
	h.mux.HandleFunc("POST /api/v1/admin/community/save", h.communitySave)
	h.mux.HandleFunc("POST /api/v1/admin/community/archive", h.communityArchive)
	h.mux.HandleFunc("GET /api/v1/admin/leisure", h.leisureGet)
	h.mux.HandleFunc("POST /api/v1/admin/leisure/archive", h.leisureArchive)
	h.mux.HandleFunc("POST /api/v1/admin/leisure/save", h.leisureSave)
	h.mux.HandleFunc("GET /api/v1/admin/verifications", h.verificationList)
	h.mux.HandleFunc("POST /api/v1/admin/verifications/review", h.verificationReview)
	return h, nil
}
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
		a, err := h.actor(r)
		if err != nil {
			h.fail(w, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), actorKey{}, a))
	}
	h.mux.ServeHTTP(w, r)
}
func respond(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (h *HTTP) fail(w http.ResponseWriter, err error) {
	status, code := 401, "invalid_credentials"
	if errors.Is(err, ErrUnavailable) {
		status, code = 503, "identity_unavailable"
	}
	if errors.Is(err, ErrLimited) {
		status, code = 429, "try_later"
		w.Header().Set("Retry-After", "900")
	}
	respond(w, status, map[string]string{"code": code})
}
func (h *HTTP) cookie(w http.ResponseWriter, name, value string, life int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/api/v1", HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteStrictMode, MaxAge: life})
}
func cookie(r *http.Request, name string) string {
	v, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return v.Value
}
func (h *HTTP) preflight(w http.ResponseWriter, r *http.Request) {
	nonce, err := randomToken()
	if err != nil {
		h.fail(w, err)
		return
	}
	value := nonce + ":" + strconv.FormatInt(h.service.now().Unix(), 10)
	token := value + ":" + h.service.mac("pre:"+value)
	h.cookie(w, preCookie, token, 600)
	respond(w, 200, map[string]string{"csrf": token})
}
func (h *HTTP) originOK(r *http.Request) bool { return r.Header.Get("Origin") == h.origin }
func (h *HTTP) preCSRF(w http.ResponseWriter, r *http.Request) bool {
	value := cookie(r, preCookie)
	parts := strings.Split(value, ":")
	ok := false
	if len(parts) == 3 {
		stamp, err := strconv.ParseInt(parts[1], 10, 64)
		now := h.service.now().Unix()
		ok = err == nil && stamp <= now && stamp >= now-600 && len(parts[0]) == 64 && hmac.Equal([]byte(parts[2]), []byte(h.service.mac("pre:"+parts[0]+":"+parts[1])))
	}
	if !h.originOK(r) || !ok || !hmac.Equal([]byte(value), []byte(r.Header.Get("X-CSRF-Token"))) {
		respond(w, 403, map[string]string{"code": "csrf_invalid"})
		return false
	}
	return true
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		respond(w, 415, map[string]string{"code": "json_required"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		respond(w, 400, map[string]string{"code": "invalid_request"})
		return false
	}
	return true
}
func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	return ip
} // X-Forwarded-For is deliberately untrusted.
func (h *HTTP) signedIn(w http.ResponseWriter, token string, a Actor, mini bool) {
	life := 3600
	if mini {
		life = 1800
	}
	h.cookie(w, sessionCookie, token, life)
	h.cookie(w, preCookie, "", -1)
	respond(w, 200, a)
}
func (h *HTTP) login(w http.ResponseWriter, r *http.Request) {
	if !h.preCSRF(w, r) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		OTP      string `json:"otp"`
	}
	if !decode(w, r, &in) {
		return
	}
	token, a, err := h.service.Login(r.Context(), in.Username, in.Password, in.OTP, clientIP(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	h.signedIn(w, token, a, false)
}
func (h *HTTP) telegram(w http.ResponseWriter, r *http.Request) {
	if !h.preCSRF(w, r) {
		return
	}
	var in struct {
		Grant    string `json:"grant"`
		InitData string `json:"initData"`
		OTP      string `json:"otp"`
	}
	if !decode(w, r, &in) {
		return
	}
	token, a, err := h.service.Telegram(r.Context(), in.Grant, in.InitData, in.OTP, clientIP(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	h.signedIn(w, token, a, true)
}
func (h *HTTP) actor(r *http.Request) (Actor, error) {
	if a, ok := r.Context().Value(actorKey{}).(Actor); ok {
		return a, nil
	}
	return h.service.Current(r.Context(), cookie(r, sessionCookie))
}
func (h *HTTP) current(w http.ResponseWriter, r *http.Request) {
	a, err := h.actor(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	respond(w, 200, a)
}
func (h *HTTP) logout(w http.ResponseWriter, r *http.Request) {
	a, err := h.actor(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	if !h.originOK(r) || !hmac.Equal([]byte(a.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) {
		respond(w, 403, map[string]string{"code": "csrf_invalid"})
		return
	}
	if err = h.service.Logout(r.Context(), cookie(r, sessionCookie)); err != nil {
		h.fail(w, err)
		return
	}
	h.cookie(w, sessionCookie, "", -1)
	respond(w, 200, map[string]bool{"loggedOut": true})
}

type Role struct {
	ID          int64    `json:"id,string"`
	Name        string   `json:"name"`
	Level       int      `json:"level"`
	Permissions []string `json:"permissions"`
}

func (h *HTTP) roles(w http.ResponseWriter, r *http.Request) {
	a, err := h.actor(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	if !a.Can("admins") {
		respond(w, 403, map[string]string{"code": "forbidden"})
		return
	}
	rows, err := h.service.pool.Query(r.Context(), "SELECT id,name,level,permissions FROM roles WHERE $1 OR level>$2 ORDER BY level,id LIMIT 100", a.Super, a.Level)
	if err != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var role Role
		var raw string
		if rows.Scan(&role.ID, &role.Name, &role.Level, &raw) != nil || json.Unmarshal([]byte(raw), &role.Permissions) != nil {
			h.fail(w, ErrUnavailable)
			return
		}
		out = append(out, role)
	}
	if rows.Err() != nil {
		h.fail(w, ErrUnavailable)
		return
	}
	respond(w, 200, out)
}
