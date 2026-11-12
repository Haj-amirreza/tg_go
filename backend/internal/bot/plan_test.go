package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/rich"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeFallbackRemainsOrderedAndRechecksAccess(t *testing.T) {
	for _, scenario := range []string{"fallback", "revoked", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFlow(t)
			f.native = true
			ctx := context.Background()
			actor := adminFlow(f, `["content","publish"]`)
			f.pool.Exec(ctx, `UPDATE work_jobs SET state='completed'`)
			tx, _ := f.pool.Begin(ctx)
			c, e := catalog.Save(ctx, tx, catalog.Content{Kind: "announcements", Title: "جدول پیشرفته", Body: "", Status: "published", Metadata: map[string]any{}}, actor, true)
			if e != nil {
				t.Fatal(e)
			}
			doc := rich.Document{Schema: 1, Rich: true, Blocks: []rich.Block{{Type: "table", Enabled: true, Rows: [][]string{{"الف", "ب"}, {"یک", "دو"}}, Header: true}}}
			raw, _ := json.Marshal(doc)
			if _, e = tx.Exec(ctx, `INSERT INTO advanced_contents(content_id,document,updated_at) VALUES($1,$2,1)`, c.ID, string(raw)); e != nil {
				t.Fatal(e)
			}
			tx.Commit(ctx)
			out := f.send(fmt.Sprintf("view:%d", c.ID), false)
			if out[len(out)-1].Method != "plan" {
				t.Fatal("native plan not created")
			}
			f.send("menu", false)
			f.pool.Exec(ctx, `UPDATE queue_control SET paused=false`)
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				calls = append(calls, method)
				if method == "sendRichMessage" {
					var payload map[string]any
					json.NewDecoder(r.Body).Decode(&payload)
					richRaw, _ := json.Marshal(payload["rich_message"])
					if !strings.Contains(string(richRaw), `"type":"table"`) {
						t.Error("table not sent natively")
					}
					status := 400
					if scenario == "unknown" {
						status = 500
					}
					w.WriteHeader(status)
					fmt.Fprintf(w, `{"ok":false,"error_code":%d}`, status)
					return
				}
				w.Write([]byte(`{"ok":true,"result":{"message_id":10}}`))
			}))
			defer server.Close()
			sender := SecureTransport{Pool: f.pool, Next: TelegramTransport{Pool: f.pool, Token: "synthetic", Endpoint: server.URL, SkipLimits: true}}
			q := workqueue.Queue{Pool: f.pool}
			if worked, e := q.DeliverOne(ctx, sender); e != nil || !worked {
				t.Fatal(worked, e)
			}
			var id int64
			var state string
			f.pool.QueryRow(ctx, `SELECT id,state FROM work_jobs WHERE payload->>'method'='plan' ORDER BY id LIMIT 1`).Scan(&id, &state)
			if scenario == "unknown" {
				if state != "uncertain" {
					t.Fatal("ambiguous native request retried", state)
				}
				if worked, e := q.DeliverOne(ctx, sender); e != nil || worked {
					t.Fatal("uncertain stream overtaken", worked, e)
				}
				if len(calls) != 1 {
					t.Fatal(calls)
				}
				return
			}
			if state != "pending" {
				t.Fatal("fallback was not durable", state)
			}
			if scenario == "revoked" {
				f.pool.Exec(ctx, `UPDATE contents SET status='archived' WHERE id=$1`, c.ID)
			}
			if worked, e := q.DeliverOne(ctx, sender); e != nil || !worked {
				t.Fatal(worked, e)
			}
			f.pool.QueryRow(ctx, `SELECT state FROM work_jobs WHERE id=$1`, id).Scan(&state)
			if scenario == "revoked" {
				if state != "dead" || len(calls) != 1 {
					t.Fatal("revoked fallback sent", state, calls)
				}
			} else if state != "completed" || len(calls) != 2 || calls[1] != "sendMessage" {
				t.Fatal("fallback did not finish", state, calls)
			}
			if worked, e := q.DeliverOne(ctx, sender); e != nil || !worked {
				t.Fatal(worked, e)
			}
			if calls[len(calls)-1] != "sendMessage" {
				t.Fatal("newer reply missing", calls)
			}
		})
	}
}
