package identity

import (
	"context"
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/rich"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStudioCommandsAndCategoryVersioning(t *testing.T) {
	s := testService(t)
	f := vector(t)
	token, a, e := s.Login(context.Background(), "fulladmin", f.Password, "", "studio")
	if e != nil {
		t.Fatal(e)
	}
	h, _ := NewHTTP(s, "https://admin.example.test")
	call := func(path string, v any, key string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(v)
		r := httptest.NewRequest("POST", "/api/v1/admin/"+path, strings.NewReader(string(raw)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Origin", "https://admin.example.test")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", a.CSRF)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	v := map[string]any{"content": map[string]any{"id": "0", "kind": "announcements", "title": "اعلان غنی", "body": "", "metadata": map[string]any{"access": "public"}, "status": "published", "version": 0, "publishedAt": nil, "expiresAt": nil}, "document": rich.Document{Schema: 1, Rich: true, Blocks: []rich.Block{{Type: "text", Enabled: true, Text: "<b>سلام</b>"}}}, "categoryId": "1", "mode": "bank"}
	first := call("studio/save", v, "studio_create_00001")
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	second := call("studio/save", v, "studio_create_00001")
	if first.Body.String() != second.Body.String() {
		t.Fatal("studio replay differs")
	}
	v["document"] = rich.Document{Schema: 1, Blocks: []rich.Block{{Type: "text", Text: "<img onerror='x'>"}}}
	if w := call("studio/save", v, "studio_invalid_0001"); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	c := map[string]any{"id": "1", "name": "فرهنگی نو", "active": true, "revision": 1, "following": false}
	if w := call("notices/category", c, "category_update_001"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("notices/category", c, "category_update_002"); w.Code != 409 {
		t.Fatal("stale category", w.Code)
	}
	var n int
	s.pool.QueryRow(context.Background(), `SELECT count(*) FROM announcement_dispatches`).Scan(&n)
	if n != 0 {
		t.Fatal("bank publish broadcasted")
	}
}
