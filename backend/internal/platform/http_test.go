package platform

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadinessAndAuth(t *testing.T) {
	for _, ready := range []bool{false, true} {
		h := Handler(func(context.Context) error {
			if ready {
				return nil
			}
			return errors.New("SECRET_DATABASE_URL")
		}, "test", "")
		for _, p := range []struct {
			path string
			code int
		}{{"/healthz", 200}, {"/readyz", map[bool]int{true: 200, false: 503}[ready]}, {"/api/v1/admin/users", 401}, {"/api/v1/system/status", 200}, {"/api/unknown", 404}} {
			r := httptest.NewRecorder()
			h.ServeHTTP(r, httptest.NewRequest("GET", p.path, nil))
			if r.Code != p.code || strings.Contains(r.Body.String(), "SECRET") {
				t.Fatalf("bad response: %s %d", p.path, r.Code)
			}
			if r.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing security header")
			}
		}
	}
}
func TestStaticDoesNotFallbackOnAPI(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("APP_SHELL"), 0600)
	h := Handler(func(context.Context) error { return nil }, "test", dir)
	for _, p := range []struct {
		path string
		code int
	}{{"/", 200}, {"/login", 200}, {"/panel", 200}, {"/admin", 200}, {"/admin/", 200}, {"/panel/catalog", 200}, {"/admin/login", 200}, {"/panel/support", 200}, {"/admin/.env", 404}, {"/panel/api", 404}, {"/admin/missing.js", 404}, {"/missing.js", 404}, {"/.env", 404}, {"/api/missing", 404}} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", p.path, nil))
		if r.Code != p.code {
			t.Fatalf("%s returned %d", p.path, r.Code)
		}
	}
}
func TestVersionedAssetsCacheWithoutStaleShellOrPrivateAPI(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "assets/app-Ab12_C34.js", "assets/app-Ab12_C34.css", "assets/manual.css"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler(func(context.Context) error { return nil }, "test", dir)
	for _, check := range []struct{ path, policy string }{
		{"/assets/app-Ab12_C34.js", "public, max-age=31536000, immutable"},
		{"/assets/app-Ab12_C34.css", "public, max-age=31536000, immutable"},
		{"/assets/manual.css", ""}, {"/assets/missing-Ab12_C34.js", ""},
		{"/", "no-cache"}, {"/login", "no-cache"}, {"/manage/studio", "no-cache"},
		{"/api/v1/admin/session", "no-store"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(method, check.path, nil))
			if response.Header().Get("Cache-Control") != check.policy {
				t.Fatalf("unexpected cache policy for %s %s", method, check.path)
			}
		}
	}
}

func TestGracefulShutdown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeListener(ctx, l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hung")
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	Wait(ctx)
}
