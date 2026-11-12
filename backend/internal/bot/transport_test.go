package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelegramTransportClassifiesWithoutLeakingToken(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	cases := []struct {
		body                      string
		code                      int
		retry, permanent, unknown bool
	}{{`{"ok":true,"result":{}}`, 200, false, false, false}, {`{"ok":false,"error_code":429,"parameters":{"retry_after":3}}`, 429, true, false, false}, {`{"ok":false,"error_code":400}`, 400, false, true, false}, {`{"ok":false,"error_code":500}`, 500, false, false, true}, {`not-json`, 200, false, false, true}}
	for _, c := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/botsynthetic-token/sendMessage" {
				t.Error("invalid method")
			}
			w.WriteHeader(c.code)
			w.Write([]byte(c.body))
		}))
		raw, _ := json.Marshal(Delivery{Method: "sendMessage", Payload: map[string]any{"chat_id": 123456, "text": "سلام"}})
		out, e := (TelegramTransport{Pool: f.pool, Token: "synthetic-token", Endpoint: server.URL, SkipLimits: true}).Send(ctx, workqueue.Job{Payload: raw})
		server.Close()
		if (out.RetryAfter > 0) != c.retry || out.Permanent != c.permanent || out.Unknown != c.unknown {
			t.Fatal(c, out, e)
		}
		if e != nil && strings.Contains(e.Error(), "synthetic-token") {
			t.Fatal("token leak")
		}
	}
	if delay, e := (TelegramTransport{Pool: f.pool}).reserve(ctx, "123", false); e != nil || delay != 0 {
		t.Fatal(delay, e)
	}
	if delay, e := (TelegramTransport{Pool: f.pool}).reserve(ctx, "123", false); e != nil || delay <= 0 || delay > time.Second {
		t.Fatal(delay, e)
	}
}
func TestMediaIsUploadedOnceThenReusesTelegramFileID(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	root := t.TempDir()
	content := []byte("synthetic file used only by local HTTP stub")
	sum := sha256.Sum256(content)
	storage := strings.Repeat("a", 64)
	if e := os.WriteFile(filepath.Join(root, storage), content, 0600); e != nil {
		t.Fatal(e)
	}
	var file int64
	e := f.pool.QueryRow(ctx, `INSERT INTO files(original_name,storage_name,mime,size,checksum,created_at) VALUES('sample.png',$1,'image/png',$2,$3,1) RETURNING id`, storage, len(content), hex.EncodeToString(sum[:])).Scan(&file)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Error("first upload missing")
			}
		} else {
			var payload map[string]any
			if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["photo"] != "cached_photo_id" {
				t.Error("Telegram file ID not reused", payload)
			}
		}
		w.Write([]byte(`{"ok":true,"result":{"photo":[{"file_id":"cached_photo_id","file_unique_id":"unique_photo"}]}}`))
	}))
	defer server.Close()
	transport := TelegramTransport{Pool: f.pool, Token: "synthetic-media-token", Endpoint: server.URL, FileRoot: root, SkipLimits: true}
	raw, _ := json.Marshal(Delivery{Method: "sendPhoto", Payload: map[string]any{"chat_id": 123456, "photo": fmt.Sprintf("unibot-file:%d:photo", file)}})
	for i := 0; i < 2; i++ {
		if outcome, e := transport.Send(ctx, workqueue.Job{Payload: raw}); e != nil || outcome.Permanent || outcome.Unknown {
			t.Fatal(outcome, e)
		}
		if i == 0 {
			if e = os.Remove(filepath.Join(root, storage)); e != nil {
				t.Fatal(e)
			}
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
