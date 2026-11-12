package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/community"
	"github.com/Haj-amirreza/tg_go/backend/internal/legacy"
	"github.com/Haj-amirreza/tg_go/backend/internal/support"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"strings"
	"testing"
	"time"
)

func approveFlow(f *flow) int64 {
	f.send("menu", true)
	ctx := context.Background()
	var user int64
	f.pool.QueryRow(ctx, `SELECT id FROM users WHERE telegram_id=$1`, fmt.Sprint(func() int64 {
		if f.sender == 0 {
			return 123456
		}
		return f.sender
	}())).Scan(&user)
	_, e := f.pool.Exec(ctx, `INSERT INTO student_verifications(user_id,status,details,created_at,updated_at) VALUES($1,'approved','{}',1,1) ON CONFLICT(user_id) DO UPDATE SET status='approved'`, user)
	if e != nil {
		f.t.Fatal(e)
	}
	return user
}
func adminFlow(f *flow, permissions string) int64 {
	f.send("menu", true)
	ctx := context.Background()
	var role, actor int64
	e := f.pool.QueryRow(ctx, `INSERT INTO roles(name,permissions) VALUES($1,$2) RETURNING id`, fmt.Sprintf("role-%d", f.next), permissions).Scan(&role)
	if e != nil {
		f.t.Fatal(e)
	}
	e = f.pool.QueryRow(ctx, `INSERT INTO admins(username,password_hash,name,role_id,created_at) VALUES($1,'x','Manager',$2,1) RETURNING id`, fmt.Sprintf("admin%d", f.next), role).Scan(&actor)
	if e != nil {
		f.t.Fatal(e)
	}
	id := f.sender
	if id == 0 {
		id = 123456
	}
	_, e = f.pool.Exec(ctx, `INSERT INTO admin_telegram_links(admin_id,telegram_id,linked_at) VALUES($1,$2,1)`, actor, fmt.Sprint(id))
	if e != nil {
		f.t.Fatal(e)
	}
	return actor
}
func TestStudentChannelPreviewApprovalAndRevocation(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	if !strings.Contains(body(f.send("fun:channels", true)), "تأیید") {
		t.Fatal("channel allowed without approval")
	}
	if strings.Contains(body(f.send("fun:fortune", true)), "تأیید دانشجویی نیاز دارد") {
		t.Fatal("fortune was protected")
	}
	user := approveFlow(f)
	f.send("sch:new", true)
	for _, v := range []string{"کانال دانشجویی آزمایش", "@my_test_channel"} {
		f.send(v, false)
	}
	preview := f.send("یادداشت‌های دانشگاهی", false)
	confirm := findData(t, preview, "ارسال برای تأیید مدیر")
	var count int
	f.pool.QueryRow(ctx, `SELECT count(*) FROM student_channels`).Scan(&count)
	if count != 0 {
		t.Fatal("preview wrote data")
	}
	f.send(confirm, true)
	f.send(confirm, true)
	f.pool.QueryRow(ctx, `SELECT count(*) FROM student_channels`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate channel", count)
	}
	if strings.Contains(body(f.send("fun:channels", true)), "my_test_channel") {
		t.Fatal("pending channel public")
	}
	tx, _ := f.pool.Begin(ctx)
	items, _ := community.List(ctx, tx, "channels", 0, false, true, "pending", 0)
	a := items[0]
	a.State = "approved"
	a, e := community.Save(ctx, tx, a, user, true)
	if e != nil {
		t.Fatal(e)
	}
	tx.Commit(ctx)
	out := f.send("fun:channels", true)
	if !strings.Contains(body(out), "https://t.me/my_test_channel") {
		t.Fatal("approved channel missing")
	}
	tx, _ = f.pool.Begin(ctx)
	g := out[len(out)-1].Guard
	if e = validateDomains(ctx, tx, g, true); e != nil {
		t.Fatal(e)
	}
	if e = community.Remove(ctx, tx, "channels", a.ID, user, a.Revision, true); e != nil {
		t.Fatal(e)
	}
	if e = validateDomains(ctx, tx, g, true); e != catalog.ErrMissing {
		t.Fatal("removed channel not revoked", e)
	}
	tx.Rollback(ctx)
}
func TestSupportOwnershipInternalNotesAndReplay(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	f.send("support:question", true)
	f.send("ثبت نام چه روزی است؟", false)
	var id, user int64
	f.pool.QueryRow(ctx, `SELECT id,user_id FROM tickets LIMIT 1`).Scan(&id, &user)
	f.sender = 222333
	if !strings.Contains(body(f.send(fmt.Sprintf("support:ticket:%d", id), true)), "معتبر نیست") {
		t.Fatal("ticket ownership bypass")
	}
	f.sender = 123456
	f.send(fmt.Sprintf("support:close:%d", id), true)
	f.send(fmt.Sprintf("support:reply:%d", id), true)
	f.send("پیام پس از بازکردن گفتگو", false)
	f.sender = 555666
	actor := adminFlow(f, `["tickets"]`)
	thread := f.send(fmt.Sprintf("ac:ticket:%d", id), true)
	reply := findData(t, thread, "پاسخ به دانشجو")
	f.send(reply, true)
	preview := f.send("مهلت ثبت نام تا پنجشنبه است.", false)
	confirm := findData(t, preview, "تأیید پاسخ")
	f.send(confirm, true)
	f.send(confirm, true)
	var replies int
	f.pool.QueryRow(ctx, `SELECT count(*) FROM ticket_messages WHERE admin_id=$1`, actor).Scan(&replies)
	if replies != 1 {
		t.Fatal("duplicate admin reply", replies)
	}
	tx, _ := f.pool.Begin(ctx)
	th, e := support.Get(ctx, tx, id, 0, true, -1)
	if e != nil {
		t.Fatal(e)
	}
	event, e := support.AdminReply(ctx, tx, actor, support.Reply{ID: id, Revision: th.Ticket.Revision, Body: "یادداشت محرمانه", Internal: true, State: "reviewing", Priority: "high"})
	if e != nil || event.Message != 0 {
		t.Fatal(event, e)
	}
	if e = QueueTicketReply(ctx, tx, event); e != nil {
		t.Fatal(e)
	}
	student, e := support.Get(ctx, tx, id, user, false, -1)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range student.Messages {
		if m.Internal || strings.Contains(m.Body, "محرمانه") {
			t.Fatal("internal leak")
		}
	}
	tx.Rollback(ctx)
	f.sender = 123456
	if strings.Contains(body(f.send(fmt.Sprintf("support:ticket:%d", id), true)), "محرمانه") {
		t.Fatal("internal leaked to bot")
	}
}
func TestXOAndCharadesReplayExpiryAndOwnership(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	out := f.send("fun:xo:new", true)
	move := findData(t, out, "▫️")
	f.send(move, true)
	var state string
	f.pool.QueryRow(ctx, `SELECT conversation FROM users WHERE telegram_id='123456'`).Scan(&state)
	f.send(move, true)
	var after string
	f.pool.QueryRow(ctx, `SELECT conversation FROM users WHERE telegram_id='123456'`).Scan(&after)
	var a, b map[string]any
	json.Unmarshal([]byte(state), &a)
	json.Unmarshal([]byte(after), &b)
	ra, _ := json.Marshal(a["leisure_xo"])
	rb, _ := json.Marshal(b["leisure_xo"])
	if string(ra) != string(rb) {
		t.Fatal("stale move changed board")
	}
	f.sender = 777888
	if !strings.Contains(body(f.send(move, true)), "قدیمی") {
		t.Fatal("foreign game accepted")
	}
	f.sender = 123456
	ready := f.send("fun:charades:new:easy", true)
	start := findData(t, ready, "▶️ شروع اجرا")
	playing := f.send(start, true)
	correct := findData(t, playing, "✅ درست حدس زدیم")
	f.send(correct, true)
	f.send(correct, true)
	f.pool.QueryRow(ctx, `SELECT conversation FROM users WHERE telegram_id='123456'`).Scan(&state)
	json.Unmarshal([]byte(state), &a)
	game := object(a["leisure_charades"])
	scores, _ := game["scores"].([]any)
	if len(scores) != 2 || number(scores[0]) != 1 {
		t.Fatal("duplicate score", game)
	}
	ready = f.send("fun:charades:new:easy", true)
	playing = f.send(findData(t, ready, "▶️ شروع اجرا"), true)
	correct = findData(t, playing, "✅ درست حدس زدیم")
	f.pool.QueryRow(ctx, `SELECT conversation FROM users WHERE telegram_id='123456'`).Scan(&state)
	json.Unmarshal([]byte(state), &a)
	game = object(a["leisure_charades"])
	game["deadline"] = time.Now().Unix() - 1
	raw, _ := json.Marshal(a)
	f.pool.Exec(ctx, `UPDATE users SET conversation=$1 WHERE telegram_id='123456'`, string(raw))
	f.send(correct, true)
	f.pool.QueryRow(ctx, `SELECT conversation FROM users WHERE telegram_id='123456'`).Scan(&state)
	json.Unmarshal([]byte(state), &a)
	game = object(a["leisure_charades"])
	scores, _ = game["scores"].([]any)
	if number(scores[0]) != 0 {
		t.Fatal("late score awarded")
	}
}
func TestCustomFacultyAndCourseNavigation(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	actor := adminFlow(f, `["content","publish"]`)
	_, e := f.pool.Exec(ctx, `INSERT INTO taxonomy(id,kind,name,parent_id) VALUES(1,'faculty','دانشکده آزمون',NULL),(2,'major','رشته آزمون',1)`)
	if e != nil {
		t.Fatal(e)
	}
	tx, _ := f.pool.Begin(ctx)
	_, e = catalog.Save(ctx, tx, catalog.Content{Kind: "professors", Title: "استاد دسته‌بندی", Body: "متن", Status: "published", Metadata: map[string]any{"topic_ids": []string{"2"}}}, actor, true)
	if e != nil {
		tx.Rollback(ctx)
		t.Fatal(e)
	}
	tx.Commit(ctx)
	out := f.send("u:people:professors:0", true)
	if findData(t, out, "دانشکده آزمون") != "u:people:professors:1" {
		t.Fatal("faculty missing")
	}
	out = f.send("u:people:professors:1", true)
	if findData(t, out, "رشته آزمون") != "u:people:professors:2" {
		t.Fatal("major missing")
	}
	if !strings.Contains(body(f.send("u:people:professors:2", true)), "استاد دسته‌بندی") {
		t.Fatal("scoped professor missing")
	}
	_, e = f.pool.Exec(ctx, `INSERT INTO contents(kind,title,body,metadata,status,topic_id,created_at,updated_at) VALUES('links','کانال درس عمومی','متن','{"link_type":"channel","link_category":"courses","course_level":"general"}','published',NULL,1,1),('links','کانال درس تخصصی','متن','{"link_type":"channel","link_category":"courses","course_level":"specialized"}','published',2,1,1)`)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(body(f.send("u:links:channel:courses:general:0", true)), "کانال درس عمومی") {
		t.Fatal("general course missing")
	}
	if !strings.Contains(body(f.send("u:links:channel:courses:specialized:2:0", true)), "کانال درس تخصصی") {
		t.Fatal("specialized course missing")
	}
}
func TestLateDeliveryApprovalAndAdminRoleRevocation(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	user := approveFlow(f)
	out := f.send("market", true)
	raw, _ := json.Marshal(out[len(out)-1])
	next := &recordedTransport{}
	transport := SecureTransport{Pool: f.pool, Next: next}
	if _, e := transport.Send(ctx, workqueue.Job{Payload: raw}); e != nil {
		t.Fatal(e)
	}
	f.pool.Exec(ctx, `UPDATE student_verifications SET status='revoked' WHERE user_id=$1`, user)
	if result, e := transport.Send(ctx, workqueue.Job{Payload: raw}); e == nil || !result.Permanent || next.calls != 1 {
		t.Fatal("approval revocation ignored", result, e)
	}
	actor := adminFlow(f, `["tickets"]`)
	out = f.send("ac:tickets:0", true)
	raw, _ = json.Marshal(out[len(out)-1])
	if _, e := transport.Send(ctx, workqueue.Job{Payload: raw}); e != nil {
		t.Fatal(e)
	}
	f.pool.Exec(ctx, `UPDATE roles SET permissions='[]' WHERE id=(SELECT role_id FROM admins WHERE id=$1)`, actor)
	if result, e := transport.Send(ctx, workqueue.Job{Payload: raw}); e == nil || !result.Permanent || next.calls != 2 {
		t.Fatal("role revocation ignored", result, e)
	}
}
func TestVerificationOwnContactEncryptionAndRedaction(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	f.send("verify", true)
	contact := func(owner int64) {
		f.next++
		m := Message{ID: f.next, From: From{ID: 123456}, Contact: &Contact{User: owner, Phone: "+989120000001"}}
		m.Chat.ID = 123456
		m.Chat.Type = "private"
		raw, _ := json.Marshal(Update{ID: f.next, Message: &m})
		tx, _ := f.pool.Begin(ctx)
		var job int64
		if e := tx.QueryRow(ctx, `INSERT INTO work_jobs(dedupe_key,kind,payload) VALUES($1,'telegram.update',$2) RETURNING id`, fmt.Sprintf("contact:%d", f.next), raw).Scan(&job); e != nil {
			t.Fatal(e)
		}
		if e := (Engine{AppKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"}).Handle(ctx, tx, workqueue.Job{ID: job, Payload: raw}); e != nil {
			t.Fatal(e)
		}
		if e := tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		var payload string
		if e := f.pool.QueryRow(ctx, `SELECT payload::text FROM work_jobs WHERE id=$1`, job).Scan(&payload); e != nil || strings.Contains(payload, "989120000001") || !strings.Contains(payload, "redacted") {
			t.Fatal("sensitive inbox retained", e)
		}
	}
	contact(999999)
	var phase string
	f.pool.QueryRow(ctx, `SELECT phase FROM student_verifications`).Scan(&phase)
	if phase != "phone" {
		t.Fatal("foreign contact accepted")
	}
	contact(123456)
	f.send("دانشجوی نمونه", false)
	f.send("۴۰۰۱۲۳۴۵", false)
	var status, encrypted, state string
	f.pool.QueryRow(ctx, `SELECT status,details FROM student_verifications`).Scan(&status, &encrypted)
	crypt, _ := legacy.New("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	raw, e := crypt.Decrypt(encrypted)
	if e != nil || status != "pending" || !strings.Contains(string(raw), "40012345") {
		t.Fatal(status, e)
	}
	f.pool.QueryRow(ctx, `SELECT conversation FROM users WHERE telegram_id='123456'`).Scan(&state)
	if strings.Contains(state, "40012345") || strings.Contains(encrypted, "989120000001") {
		t.Fatal("unencrypted identity details")
	}
}
