package identity

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Haj-amirreza/tg_go/backend/internal/platform"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Explicit local browser harness. Normal test runs skip it; testService rejects real DB names.
func TestBrowserServer(t *testing.T) {
	if os.Getenv("NEXT_BROWSER_SERVER") != "1" {
		t.Skip("local browser harness disabled")
	}
	s := testService(t)
	f := vector(t)
	s.now = time.Now
	if _, err := s.pool.Exec(context.Background(), "UPDATE admins SET totp_secret=$1 WHERE id=101", f.Encrypted); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	raw, _ := json.Marshal(map[string]string{"database": cfg.ConnConfig.Database, "username": "fulladmin", "password": f.Password, "totp_secret": f.Secret})
	file := os.Getenv("NEXT_BROWSER_FIXTURE")
	if file == "" {
		file = filepath.Join(os.TempDir(), "identity-browser-fixture.json")
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal("cannot write private browser fixture")
	}
	defer os.Remove(file)
	_, err := s.pool.Exec(context.Background(), `INSERT INTO work_jobs(dedupe_key,kind,payload,state) VALUES('browser-dead','maintenance.noop','{"private":"DO_NOT_EXPOSE"}','dead'),('browser-uncertain','telegram.send','{}','uncertain')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(context.Background(), `INSERT INTO university_books(source_key,title,details,search_text,category) SELECT lpad(n::text,64,'0'),'رایانه آزمایشی '||n,'{"نویسنده":"نمونه"}','رایانه آزمایشی '||n,'engineering' FROM generate_series(1,23)n`)
	if err != nil {
		t.Fatal(err)
	}
	var studentID, ticketID int64
	if err = s.pool.QueryRow(context.Background(), `INSERT INTO users(telegram_id,first_name,created_at,last_seen) VALUES('778899001','دانشجوی آزمایشی',1,1) RETURNING id`).Scan(&studentID); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(context.Background(), `INSERT INTO tickets(user_id,subject,category,created_at,updated_at) VALUES($1,'پرسش مرورگر','سؤال دانشجویی',1,1) RETURNING id`, studentID).Scan(&ticketID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(context.Background(), `INSERT INTO ticket_messages(ticket_id,body,created_at) VALUES($1,'مهلت ثبت نام تا چه روزی است؟',1)`, ticketID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(context.Background(), `INSERT INTO student_channels(user_id,title,url,description,created_at) VALUES($1,'کانال مرورگر','https://t.me/browser_student_channel','توضیح آزمایشی کانال',1)`, studentID); err != nil {
		t.Fatal(err)
	}
	privateDetails, _ := s.crypto.Encrypt([]byte(`{"full_name":"دانشجوی آزمایشی","phone":"+989120000001","student_number":"40012345"}`))
	if _, err = s.pool.Exec(context.Background(), `INSERT INTO student_verifications(user_id,status,phase,details,student_hash,created_at,updated_at) VALUES($1,'pending','complete',$2,'synthetic-student-hash',1,1)`, studentID, privateDetails); err != nil {
		t.Fatal(err)
	}
	origin := os.Getenv("NEXT_BASE_URL")
	if origin == "" {
		origin = "http://127.0.0.1:8082"
	}
	t.Setenv("PRIVATE_FILE_DIR", t.TempDir())
	auth, err := NewHTTP(s, origin)
	if err != nil {
		t.Fatal(err)
	}
	static, err := filepath.Abs("../../../frontend/admin/dist")
	if err != nil {
		t.Fatal(err)
	}
	address := os.Getenv("NEXT_TEST_HTTP_ADDR")
	if address == "" {
		address = "127.0.0.1:8082"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	t.Log("local browser harness ready; synthetic credentials were not printed")
	if err = platform.Serve(ctx, address, platform.HandlerWithIdentity(func(ctx context.Context) error { return postgres.Ready(ctx, s.pool) }, "test", static, auth)); err != nil {
		t.Fatal(err)
	}
}
