package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testService(t *testing.T) *Service {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("use a disposable *_test database")
	}
	ctx := context.Background()
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	token, _ := randomToken()
	schema := "identity_" + token[:12]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = owner.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); owner.Close() })
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.Tracer = postgres.QueryTracer()
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s, err := New(pool, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", "synthetic-bot-token")
	if err != nil {
		t.Fatal(err)
	}
	f := vector(t)
	s.now = func() time.Time { return time.Unix(f.Counter*30, 0) }
	_, err = pool.Exec(ctx, `INSERT INTO roles(id,name,permissions,level) VALUES(101,'مدیرکل','["admins","tickets"]',1),(102,'پشتیبانی','["tickets"]',30)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO admins(id,username,password_hash,name,role_id,is_super,created_at) VALUES(101,'fulladmin',$1,'مدیر تست',101,true,1),(102,'limited',$1,'پشتیبان تست',102,false,1)`, f.Bcrypt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO admin_telegram_links(admin_id,telegram_id,linked_at) VALUES(101,'123456',1)`)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestSessionRevocationAndPermissions(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	f := vector(t)
	token, a, err := s.Login(ctx, "fulladmin", f.Password, "", "1")
	if err != nil || !a.Can("admins") {
		t.Fatal("super login failed", err)
	}
	if _, err = s.Current(ctx, token); err != nil {
		t.Fatal(err)
	}
	var stored string
	s.pool.QueryRow(ctx, "SELECT token_hash FROM admin_sessions WHERE admin_id=101").Scan(&stored)
	if stored == token || stored != hash(token) {
		t.Fatal("raw bearer persisted")
	}
	s.pool.Exec(ctx, "UPDATE admins SET auth_version=auth_version+1 WHERE id=101")
	if _, err = s.Current(ctx, token); err == nil {
		t.Fatal("revoked session accepted")
	}
	token, a, err = s.Login(ctx, "limited", f.Password, "", "2")
	if err != nil || a.Can("admins") || !a.Can("tickets") {
		t.Fatal("limited policy failed")
	}
	s.pool.Exec(ctx, `UPDATE roles SET permissions='[]' WHERE id=102`)
	a, err = s.Current(ctx, token)
	if err != nil || a.Can("tickets") {
		t.Fatal("role refresh failed")
	}
	s.pool.Exec(ctx, "UPDATE admins SET active=false WHERE id=102")
	if _, err = s.Current(ctx, token); err == nil {
		t.Fatal("disabled admin accepted")
	}
}
func TestAtomicTOTPAndRateLimit(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	f := vector(t)
	s.pool.Exec(ctx, "UPDATE admins SET totp_secret=$1 WHERE id=101", f.Encrypted)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.Login(ctx, "fulladmin", f.Password, f.Code, "1")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("TOTP consumption not atomic", success)
	}
	for i := 0; i < 5; i++ {
		s.Login(ctx, "limited", "wrong", "", "2")
	}
	if _, _, err := s.Login(ctx, "limited", f.Password, "", "2"); err != ErrLimited {
		t.Fatal("persistent username limit failed", err)
	}
	s.now = func() time.Time { return time.Unix(f.Counter*30+901, 0) }
	if _, _, err := s.Login(ctx, "limited", f.Password, "", "2"); err != nil {
		t.Fatal("rate window did not expire", err)
	}
}
func TestTelegramBindingReplayAndRollback(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	grant, err := s.IssueGrant(ctx, "123456", "admins")
	if err != nil {
		t.Fatal(err)
	}
	wrong := signedLaunch(s.botToken, "999999", s.now())
	if _, _, err = s.Telegram(ctx, grant, wrong, "", "1"); err == nil {
		t.Fatal("wrong Telegram identity accepted")
	}
	data := signedLaunch(s.botToken, "123456", s.now())
	token, _, err := s.Telegram(ctx, grant, data, "", "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Telegram(ctx, grant, data, "", "1"); err == nil {
		t.Fatal("grant replay accepted")
	}
	grant2, _ := s.IssueGrant(ctx, "123456", "admins")
	if _, _, err = s.Telegram(ctx, grant2, data, "", "1"); err == nil {
		t.Fatal("signed launch replay accepted")
	}
	s.pool.Exec(ctx, "DELETE FROM admin_telegram_links WHERE admin_id=101")
	if _, err = s.Current(ctx, token); err == nil {
		t.Fatal("unlinked identity session accepted")
	}
	if _, err = s.IssueGrant(ctx, "123456", "admins"); err == nil {
		t.Fatal("unlinked grant issued")
	}
}
func TestHTTPOriginCSRFAndCookies(t *testing.T) {
	s := testService(t)
	h, err := NewHTTP(s, "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, csrf, origin string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:5555"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Origin", origin)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	pre := request("GET", "/api/v1/auth/csrf", "", "", "", nil)
	var v map[string]string
	json.Unmarshal(pre.Body.Bytes(), &v)
	cookies := pre.Result().Cookies()
	f := vector(t)
	raw, _ := json.Marshal(map[string]string{"username": "limited", "password": f.Password, "otp": ""})
	body := string(raw)
	for _, item := range []struct{ token, origin string }{{"", "https://admin.example.test"}, {v["csrf"], "https://evil.example.test"}} {
		if request("POST", "/api/v1/auth/login", body, item.token, item.origin, cookies).Code != 403 {
			t.Fatal("CSRF/origin bypass")
		}
	}
	login := request("POST", "/api/v1/auth/login", body, v["csrf"], "https://admin.example.test", cookies)
	if login.Code != 200 {
		t.Fatal("valid login rejected", login.Code)
	}
	var a Actor
	json.Unmarshal(login.Body.Bytes(), &a)
	var sessions []*http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == sessionCookie {
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || len(c.Value) != 64 {
				t.Fatal("unsafe cookie")
			}
			sessions = append(sessions, c)
		}
	}
	if len(sessions) != 1 {
		t.Fatal("session not issued")
	}
	if request("GET", "/api/v1/admin/roles", "", "", "", sessions).Code != 403 {
		t.Fatal("permission bypass")
	}
	if request("POST", "/api/v1/admin/logout", "", "", "https://admin.example.test", sessions).Code != 403 {
		t.Fatal("logout CSRF bypass")
	}
	if request("POST", "/api/v1/admin/logout", "", a.CSRF, "https://admin.example.test", sessions).Code != 200 {
		t.Fatal("logout failed")
	}
	if request("GET", "/api/v1/admin/session", "", "", "", sessions).Code != 401 {
		t.Fatal("logout not persisted")
	}
	if bytes.Contains(login.Body.Bytes(), []byte(f.Bcrypt)) || bytes.Contains(login.Body.Bytes(), []byte(f.Secret)) {
		t.Fatal("credential leaked")
	}
}
func TestOfflineBootstrapOnce(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	s.pool.Exec(ctx, "TRUNCATE admins CASCADE")
	input := BootstrapInput{Username: "operator", Password: "synthetic-password-only", Name: "مدیر تست", TelegramID: "123456"}
	if err := s.Bootstrap(ctx, input); err != nil {
		t.Fatal(err)
	}
	if s.Bootstrap(ctx, input) == nil {
		t.Fatal("bootstrap reused")
	}
	if _, _, err := s.Login(ctx, input.Username, input.Password, "", "1"); err != nil {
		t.Fatal(fmt.Sprint("bootstrap login failed: ", err))
	}
}

func TestMiniTOTPFailureRetainsGrantAndExpiry(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	f := vector(t)
	s.pool.Exec(ctx, "UPDATE admins SET totp_secret=$1 WHERE id=101", f.Encrypted)
	grant, _ := s.IssueGrant(ctx, "123456", "admins")
	data := signedLaunch(s.botToken, "123456", s.now())
	if _, _, err := s.Telegram(ctx, grant, data, "000000", "1"); err == nil {
		t.Fatal("missing Mini App TOTP accepted")
	}
	if _, _, err := s.Telegram(ctx, grant, data, f.Code, "1"); err != nil {
		t.Fatal("failed OTP consumed grant", err)
	}
	grant, _ = s.IssueGrant(ctx, "123456", "admins")
	s.pool.Exec(ctx, "UPDATE admin_access_grants SET expires_at=$1", s.now().Unix()-1)
	if _, _, err := s.Telegram(ctx, grant, data, f.Code, "1"); err == nil {
		t.Fatal("expired grant accepted")
	}
	if _, err := s.IssueGrant(ctx, "123456", "unknown"); err == nil {
		t.Fatal("unknown permission accepted")
	}
}
func TestSessionExpiryAndBootstrapValidation(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	f := vector(t)
	token, _, err := s.Login(ctx, "limited", f.Password, "", "1")
	if err != nil {
		t.Fatal(err)
	}
	s.pool.Exec(ctx, "UPDATE admin_sessions SET expires_at=$1", s.now().Unix()-1)
	if _, err = s.Current(ctx, token); err == nil {
		t.Fatal("expired session accepted")
	}
	if s.Bootstrap(ctx, BootstrapInput{Username: "@handle", Password: "short", Name: "x"}) == nil {
		t.Fatal("invalid bootstrap accepted")
	}
	for _, origin := range []string{"http://example.test", "https://example.test/", "https://example.test?secret=x", "bad"} {
		if _, err := NewHTTP(s, origin); err == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
}

func TestPerformancePermissionsAndCacheClear(t *testing.T) {
	s := testService(t)
	h, _ := NewHTTP(s, "https://admin.example.test")
	ctx := context.Background()
	f := vector(t)
	for _, name := range []string{"fulladmin", "limited"} {
		token, actor, err := s.Login(ctx, name, f.Password, "", name)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/api/v1/admin/performance", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		expected := 200
		if name == "limited" {
			expected = 403
		}
		if w.Code != expected {
			t.Fatal("performance permission", w.Code)
		}
		r = httptest.NewRequest("POST", "/api/v1/admin/cache/clear", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Origin", "https://admin.example.test")
		r.Header.Set("X-CSRF-Token", actor.CSRF)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatal("cache permission", w.Code)
		}
	}
}

func TestQueueAdministration(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	s.pool.Exec(ctx, `INSERT INTO work_jobs(dedupe_key,kind,payload,state) VALUES('dead','test','{"private":"DO_NOT_EXPOSE"}','dead'),('unknown','telegram.send','{}','uncertain')`)
	f := vector(t)
	token, a, err := s.Login(ctx, "fulladmin", f.Password, "", "admin-test")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := NewHTTP(s, "https://admin.example.test")
	request := func(method, path, body, csrf string) int {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://admin.example.test")
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "DO_NOT_EXPOSE") {
			t.Fatal("payload leaked")
		}
		return w.Code
	}
	if request("POST", "/api/v1/admin/queue/control", `{"paused":false}`, "") != 403 {
		t.Fatal("pause CSRF")
	}
	if request("POST", "/api/v1/admin/queue/control", `{"paused":false}`, a.CSRF) != 200 {
		t.Fatal("pause failed")
	}
	if request("POST", "/api/v1/admin/queue/retry", `{"id":"1"}`, a.CSRF) != 200 {
		t.Fatal("dead retry failed")
	}
	if request("POST", "/api/v1/admin/queue/retry", `{"id":"2"}`, a.CSRF) != 409 {
		t.Fatal("uncertain replay allowed")
	}
	if request("GET", "/api/v1/admin/queue/jobs?after=bad", "", "") != 400 {
		t.Fatal("invalid cursor")
	}
	if request("GET", "/api/v1/admin/queue/jobs", "", "") != 200 {
		t.Fatal("listing failed")
	}
	token, a, err = s.Login(ctx, "limited", f.Password, "", "limited-test")
	if err != nil {
		t.Fatal(err)
	}
	if request("GET", "/api/v1/admin/queue/jobs", "", "") != 403 || request("POST", "/api/v1/admin/queue/control", `{"paused":false}`, a.CSRF) != 403 {
		t.Fatal("queue permission bypass")
	}
}

func TestCatalogCommandsIdempotencyAndPermissions(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	f := vector(t)
	token, a, e := s.Login(ctx, "fulladmin", f.Password, "", "catalog")
	if e != nil {
		t.Fatal(e)
	}
	h, _ := NewHTTP(s, "https://admin.example.test")
	call := func(path, body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://admin.example.test")
		r.Header.Set("X-CSRF-Token", a.CSRF)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body := `{"id":"0","kind":"professors","title":"استاد تست","body":"دروس","metadata":{"office_phone":"۰۹۱۲۳۴۵۶۷۸۹"},"status":"pending","version":0,"publishedAt":null,"expiresAt":null}`
	first := call("/api/v1/admin/catalog/save", body, "catalog-command-00001")
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	again := call("/api/v1/admin/catalog/save", body, "catalog-command-00001")
	if again.Code != 200 || again.Body.String() != first.Body.String() {
		t.Fatal("command replay changed response")
	}
	var count int
	s.pool.QueryRow(ctx, "SELECT count(*) FROM contents").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate content")
	}
	if call("/api/v1/admin/catalog/save", strings.Replace(body, "استاد تست", "نام دیگر", 1), "catalog-command-00001").Code != 409 {
		t.Fatal("key misuse accepted")
	}
	token, a, e = s.Login(ctx, "limited", f.Password, "", "catalog-limited")
	if e != nil {
		t.Fatal(e)
	}
	if call("/api/v1/admin/catalog/save", body, "catalog-command-00002").Code != 403 {
		t.Fatal("content permission bypass")
	}
}
