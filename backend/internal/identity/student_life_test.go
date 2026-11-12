package identity

import (
	"context"
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/community"
	"github.com/Haj-amirreza/tg_go/backend/internal/support"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDomainPermissionsAndTicketReplyIdempotency(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	f := vector(t)
	full, actor, e := s.Login(ctx, "fulladmin", f.Password, "", "student-full")
	if e != nil {
		t.Fatal(e)
	}
	limited, restricted, e := s.Login(ctx, "limited", f.Password, "", "student-limited")
	if e != nil {
		t.Fatal(e)
	}
	h, _ := NewHTTP(s, "https://admin.example.test")
	call := func(path string, payload any, key, token, csrf string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(payload)
		r := httptest.NewRequest("POST", "/api/v1/admin/"+path, strings.NewReader(string(raw)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://admin.example.test")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	ad := community.Ad{Kind: "channels", Title: "کانال آزمایشی", Body: "متن نمونه", URL: "@student_test_channel", State: "approved"}
	if w := call("community/save", ad, "student_denied_0001", limited, restricted.CSRF); w.Code != 403 {
		t.Fatal("limited content access", w.Code, w.Body.String())
	}
	if w := call("community/save", ad, "student_full_00001", full, actor.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var user, id int64
	s.pool.QueryRow(ctx, `INSERT INTO users(telegram_id,created_at,last_seen) VALUES('667788',1,1) RETURNING id`).Scan(&user)
	s.pool.QueryRow(ctx, `INSERT INTO tickets(user_id,subject,category,created_at,updated_at) VALUES($1,'پرسش نمونه','عمومی',1,1) RETURNING id`, user).Scan(&id)
	reply := support.Reply{ID: id, Revision: 1, Body: "پاسخ عمومی", State: "answered", Priority: "normal"}
	first := call("tickets/reply", reply, "ticket_reply_00001", limited, restricted.CSRF)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	again := call("tickets/reply", reply, "ticket_reply_00001", limited, restricted.CSRF)
	if first.Body.String() != again.Body.String() {
		t.Fatal("reply replay differs")
	}
	var count int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM ticket_messages WHERE ticket_id=$1`, id).Scan(&count)
	if count != 1 {
		t.Fatal("duplicated reply", count)
	}
	s.pool.QueryRow(ctx, `SELECT count(*) FROM work_jobs WHERE dedupe_key LIKE 'ticket:%'`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicated outbox", count)
	}
	reply.Revision = 2
	reply.Internal = true
	reply.Body = "یادداشت داخلی"
	if w := call("tickets/reply", reply, "ticket_note_000001", limited, restricted.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.pool.QueryRow(ctx, `SELECT count(*) FROM work_jobs WHERE dedupe_key LIKE 'ticket:%'`).Scan(&count)
	if count != 1 {
		t.Fatal("internal note enqueued")
	}
	if w := call("tickets/reply", reply, "ticket_stale_00001", limited, restricted.CSRF); w.Code != 409 {
		t.Fatal("stale reply accepted", w.Code)
	}
}
