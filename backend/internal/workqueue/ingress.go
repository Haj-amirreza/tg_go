package workqueue

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

func (q Queue) Ingress(secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		if len(secret) < 32 {
			w.WriteHeader(503)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(secret)) != 1 {
			w.WriteHeader(403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(413)
			return
		}
		var update struct {
			ID      *int64 `json:"update_id"`
			Message struct {
				From struct {
					ID int64 `json:"id"`
				} `json:"from"`
			} `json:"message"`
			Callback struct {
				From struct {
					ID int64 `json:"id"`
				} `json:"from"`
			} `json:"callback_query"`
		}
		if json.Unmarshal(raw, &update) != nil || update.ID == nil || *update.ID < 0 {
			w.WriteHeader(400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		tx, err := q.Pool.Begin(ctx)
		if err != nil {
			w.WriteHeader(503)
			return
		}
		defer tx.Rollback(context.Background())
		tag, err := tx.Exec(ctx, "INSERT INTO telegram_updates(update_id) VALUES($1) ON CONFLICT DO NOTHING", *update.ID)
		if err == nil && tag.RowsAffected() == 1 {
			stream := ""
			id := update.Message.From.ID
			if update.Callback.From.ID != 0 {
				id = update.Callback.From.ID
			}
			if id > 0 {
				stream = "user:" + strconv.FormatInt(id, 10)
			}
			err = Enqueue(ctx, tx, Input{Key: "update:" + strconv.FormatInt(*update.ID, 10), Kind: "telegram.update", Stream: stream, Payload: raw})
		}
		if err != nil || tx.Commit(ctx) != nil {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	})
}
