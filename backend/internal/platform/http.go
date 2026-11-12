package platform

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Status struct {
	Service             string `json:"service"`
	Stage               int    `json:"stage"`
	Environment         string `json:"environment"`
	DatabaseReady       bool   `json:"databaseReady"`
	AuthenticationReady bool   `json:"authenticationReady"`
	BotConnected        bool   `json:"botConnected"`
}
type Checker func(context.Context) error

var versionedAsset = regexp.MustCompile(`^assets/[^/]+-[A-Za-z0-9_-]{8,}\.(js|css)$`)

func Handler(check Checker, environment, static string) http.Handler {
	return HandlerWithIdentity(check, environment, static, nil)
}

func HandlerWithIdentity(check Checker, environment, static string, auth http.Handler) http.Handler {
	return HandlerWithBot(check, environment, static, auth, nil)
}

func HandlerWithBot(check Checker, environment, static string, auth http.Handler, connected func(context.Context) bool) http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, code int, value any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if check(ctx) != nil {
			write(w, 503, map[string]string{"status": "not_ready"})
			return
		}
		write(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /api/v1/system/status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		ready := check(ctx) == nil
		bot := ready && connected != nil && connected(ctx)
		write(w, 200, Status{"university-bot", 4, environment, ready, auth != nil, bot})
	})
	if auth != nil {
		mux.Handle("/api/v1/admin/", auth)
		mux.Handle("/api/v1/auth/", auth)
	} else {
		locked := func(w http.ResponseWriter, r *http.Request) {
			write(w, 401, map[string]string{"code": "authentication_not_ready"})
		}
		mux.HandleFunc("/api/v1/admin/", locked)
		mux.HandleFunc("/api/v1/auth/", locked)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { write(w, 404, map[string]string{"code": "not_found"}) })
	if static != "" {
		files := http.FileServer(http.Dir(static))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if !fs.ValidPath(path) || strings.HasPrefix(path, ".") || strings.Contains(path, "/.") {
				http.NotFound(w, r)
				return
			}
			info, err := os.Stat(filepath.Join(static, filepath.FromSlash(path)))
			if err == nil && !info.IsDir() {
				if versionedAsset.MatchString(path) {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else if strings.HasSuffix(path, ".html") {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
			if path == "login" || path == "manage" || path == "manage/catalog" || path == "manage/studio" || path == "manage/support" || path == "manage/student-life" || path == "manage/leisure" {
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeFile(w, r, filepath.Join(static, "index.html"))
				return
			}
			http.NotFound(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'self' https://web.telegram.org; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}
