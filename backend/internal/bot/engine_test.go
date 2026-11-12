package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/testdb"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"
)

type flow struct {
	native bool
	sender int64
	t      *testing.T
	pool   *pgxpool.Pool
	next   int64
}

func newFlow(t *testing.T) *flow {
	p := testdb.Open(t)
	_, err := p.Exec(context.Background(), `INSERT INTO settings(name,value,updated_at) VALUES('bot_state','"on"',1)`)
	if err != nil {
		t.Fatal(err)
	}
	return &flow{t: t, pool: p, next: 100}
}
func (f *flow) send(text string, callback bool) []Delivery {
	f.t.Helper()
	f.next++
	sender := f.sender
	if sender == 0 {
		sender = 123456
	}
	from := From{ID: sender, First: "Test"}
	msg := Message{ID: f.next, From: from, Date: time.Now().Unix(), Text: text}
	msg.Chat.ID = from.ID
	msg.Chat.Type = "private"
	u := Update{ID: f.next, Message: &msg}
	if callback {
		u.Message = nil
		raw, _ := json.Marshal(map[string]any{"id": fmt.Sprint(f.next), "from": from, "message": msg, "data": text})
		u.Callback = new(struct {
			ID      string  `json:"id"`
			From    From    `json:"from"`
			Message Message `json:"message"`
			Data    string  `json:"data"`
		})
		json.Unmarshal(raw, u.Callback)
	}
	raw, _ := json.Marshal(u)
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = (Engine{RichEnabled: f.native, Username: "university_test_bot", AppKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"}).Handle(ctx, tx, workqueue.Job{Payload: raw}); err != nil {
		tx.Rollback(ctx)
		f.t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		f.t.Fatal(err)
	}
	rows, err := f.pool.Query(ctx, "SELECT payload FROM work_jobs WHERE dedupe_key LIKE $1 ORDER BY id", fmt.Sprintf("update:%d:%%", f.next))
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var raw []byte
		rows.Scan(&raw)
		var d Delivery
		if json.Unmarshal(raw, &d) != nil {
			f.t.Fatal("invalid reply")
		}
		if d.Method != "answerCallbackQuery" {
			out = append(out, d)
		}
	}
	if rows.Err() != nil {
		f.t.Fatal(rows.Err())
	}
	if len(out) == 0 {
		f.t.Fatal("no response", text)
	}
	return out
}
func body(out []Delivery) string { return fmt.Sprint(out[len(out)-1].Payload["text"]) }
func findData(t *testing.T, out []Delivery, label string) string {
	t.Helper()
	raw, _ := json.Marshal(out[len(out)-1].Payload["reply_markup"])
	var m Markup
	json.Unmarshal(raw, &m)
	for _, row := range m.Inline {
		for _, b := range row {
			if b.Text == label {
				return b.Data
			}
		}
	}
	t.Fatal("button missing", label)
	return ""
}

func TestUnchangedConversationAvoidsExtraWrite(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `CREATE TABLE conversation_writes(id bigint);
CREATE FUNCTION record_conversation_write() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO conversation_writes VALUES(NEW.id); RETURN NEW; END $$;
CREATE TRIGGER record_conversation_write AFTER UPDATE OF conversation ON users FOR EACH ROW EXECUTE FUNCTION record_conversation_write()`)
	if err != nil {
		t.Fatal(err)
	}
	f.send("ubooks:search", true)
	f.send("u:library", true)
	f.send("u:library", true)
	var writes, interactions int
	var state string
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_writes`).Scan(&writes); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, `SELECT interactions,conversation FROM users WHERE telegram_id='123456'`).Scan(&interactions, &state); err != nil {
		t.Fatal(err)
	}
	if writes != 2 || interactions != 3 || state != "{}" {
		t.Fatal("conversation or activity tracking changed", writes, interactions, state)
	}
}

func TestProfessorSubmissionReviewAndReplay(t *testing.T) {
	f := newFlow(t)
	f.send("button:contacts", true)
	f.send("button:professors", true)
	f.send("u:professor:new", true)
	for _, v := range []string{"دکتر آزمایش یکتا", "مدار الکتریکی و ریاضی", "دانشکده مهندسی", "برق", "۰۹۱۲۳۴۵۶۷۸۹", "@example_teacher"} {
		f.send(v, false)
	}
	if !strings.Contains(body(f.send("invalid-email", false)), "معتبر") {
		t.Fatal("invalid email accepted")
	}
	preview := f.send("teacher@example.edu", false)
	if !strings.Contains(body(preview), "پیش‌نمایش") {
		t.Fatal("no preview")
	}
	confirm := findData(t, preview, "ثبت نهایی")
	f.send(confirm, true)
	f.send(confirm, true)
	var count int
	var state, metadata string
	f.pool.QueryRow(context.Background(), "SELECT count(*) FROM contents WHERE title='دکتر آزمایش یکتا'").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate form")
	}
	f.pool.QueryRow(context.Background(), "SELECT status,metadata FROM contents LIMIT 1").Scan(&state, &metadata)
	if state != "pending" || !strings.Contains(metadata, "09123456789") {
		t.Fatal("submission fields lost")
	}
	f.send("u:professor:search", true)
	if !strings.Contains(body(f.send("آزمایش", false)), "موردی پیدا نشد") {
		t.Fatal("pending leaked")
	}
	f.pool.Exec(context.Background(), "UPDATE contents SET status='published',version=2")
	f.send("u:professor:search", true)
	if !strings.Contains(body(f.send("آزمایش", false)), "دکتر آزمایش") {
		t.Fatal("published search missing")
	}
}
func TestBooksNormalizationPagingAndDetails(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		_, e := f.pool.Exec(ctx, "INSERT INTO university_books(source_key,title,details,search_text,category) VALUES($1,$2,$3,$4,'engineering')", fmt.Sprintf("%064d", i), fmt.Sprintf("کتاب رایانه %d", i), `{"نویسنده":"آزمایش","شماره":"۱"}`, catalog.Normalize(fmt.Sprintf("کتاب رایانه %d آزمایش", i)))
		if e != nil {
			t.Fatal(e)
		}
	}
	page := f.send("ubooks:home", true)
	if strings.Count(body(page), "?start=book_") != 10 {
		t.Fatal("not ten hyperlinks")
	}
	next := findData(t, page, "بعدی ←")
	out := f.send(next, true)
	if out[len(out)-1].Method != "editMessageText" || strings.Count(body(out), "?start=book_") != 10 {
		t.Fatal("pagination does not edit")
	}
	details := f.send("/start book_1", false)
	if details[len(details)-1].Method != "sendMessage" || !strings.Contains(body(details), "نویسنده") {
		t.Fatal("deep link details")
	}
	f.send("ubooks:search", true)
	if !strings.Contains(body(f.send("رايانه", false)), "25 نتیجه") {
		t.Fatal("Persian normalization")
	}
	if !strings.Contains(body(f.send("ubooks:page:deadbeef:1", true)), "منقضی") {
		t.Fatal("stale query accepted")
	}
}
func TestMenuVisibilityAndHistory(t *testing.T) {
	f := newFlow(t)
	root := f.send("menu", false)
	raw, _ := json.Marshal(root)
	for _, label := range []string{"منوی مدیر", "بازارچه دانشجویی", "استادشناسی", "معرفی کانال دانشجویان"} {
		if strings.Contains(string(raw), label) {
			t.Fatal("unexpected root", label)
		}
	}
	if !strings.Contains(string(raw), "شماره‌ها و اساتید") || !strings.Contains(string(raw), "danger") {
		t.Fatal("palette or contacts missing")
	}
	var deleted int
	f.pool.QueryRow(context.Background(), "SELECT count(*) FROM work_jobs WHERE payload->>'method'='deleteMessage'").Scan(&deleted)
	if deleted != 0 {
		t.Fatal("history deleted")
	}
}

type recordedTransport struct{ calls int }

func (r *recordedTransport) Send(context.Context, workqueue.Job) (workqueue.Outcome, error) {
	r.calls++
	return workqueue.Outcome{}, nil
}
func TestDeliveryRechecksPublishedVersionAndBlockedUser(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	f.pool.Exec(ctx, `INSERT INTO contents(kind,title,body,metadata,status,created_at,updated_at) VALUES('professors','تست','متن','{}','published',1,1)`)
	out := f.send("view:1", true)
	raw, _ := json.Marshal(out[len(out)-1])
	next := &recordedTransport{}
	secure := SecureTransport{Pool: f.pool, Next: next}
	if _, e := secure.Send(ctx, workqueue.Job{Payload: raw}); e != nil || next.calls != 1 {
		t.Fatal("eligible delivery denied", e)
	}
	f.pool.Exec(ctx, "UPDATE contents SET version=2 WHERE id=1")
	result, e := secure.Send(ctx, workqueue.Job{Payload: raw})
	if e == nil || !result.Permanent || next.calls != 1 {
		t.Fatal("stale record delivered")
	}
	root := f.send("menu", false)
	raw, _ = json.Marshal(root[len(root)-1])
	f.pool.Exec(ctx, "UPDATE users SET delivery_blocked=true")
	if _, e = secure.Send(ctx, workqueue.Job{Payload: raw}); e == nil || next.calls != 1 {
		t.Fatal("blocked user delivered")
	}
}

func TestRuntimeRoleAndDisabledModules(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	var exists bool
	f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='unibot_app')").Scan(&exists)
	if !exists {
		t.Skip("runtime role not installed")
	}
	schema := f.pool.Config().ConnConfig.RuntimeParams["search_path"]
	tx, e := f.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "GRANT USAGE ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" TO unibot_app"); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "SET LOCAL ROLE unibot_app"); e != nil {
		t.Fatal(e)
	}
	msg := Message{Text: "menu", From: From{ID: 5555}, Date: time.Now().Unix()}
	msg.Chat.ID = 5555
	msg.Chat.Type = "private"
	raw, _ := json.Marshal(Update{ID: 700, Message: &msg})
	if e = (Engine{}).Handle(ctx, tx, workqueue.Job{Payload: raw}); e != nil {
		t.Fatal("runtime privileges", e)
	}
	tx.Rollback(ctx)
	f.pool.Exec(ctx, `INSERT INTO settings(name,value,updated_at) VALUES('modules','{"directory":false}',1)`)
	if !strings.Contains(body(f.send("u:professor:new", true)), "دسترس نیست") {
		t.Fatal("disabled form started")
	}
	var n int
	f.pool.QueryRow(ctx, "SELECT count(*) FROM contents").Scan(&n)
	if n != 0 {
		t.Fatal("disabled module wrote data")
	}
}
