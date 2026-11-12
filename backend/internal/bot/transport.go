package bot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var ErrTelegram = errors.New("telegram request failed")

type TelegramTransport struct {
	Pool                      *pgxpool.Pool
	Token, FileRoot, Endpoint string
	Client                    *http.Client
	SkipLimits                bool
}

// reserve defers locally without consuming the retry budget or sleeping inside a lease.
func (t TelegramTransport) reserve(ctx context.Context, chat string, callback bool) (time.Duration, error) {
	if t.SkipLimits {
		return 0, nil
	}
	tx, e := t.Pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(context.Background())
	scopes := []string{"global"}
	if !callback {
		scopes = append(scopes, "chat:"+chat)
	}
	for _, scope := range scopes {
		if _, e = tx.Exec(ctx, `INSERT INTO telegram_send_limits(scope,ready_at) VALUES($1,now()) ON CONFLICT DO NOTHING`, scope); e != nil {
			return 0, e
		}
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return 0, e
	}
	delay := time.Duration(0)
	for _, scope := range scopes {
		var ready time.Time
		if e = tx.QueryRow(ctx, `SELECT ready_at FROM telegram_send_limits WHERE scope=$1 FOR UPDATE`, scope).Scan(&ready); e != nil {
			return 0, e
		}
		delay = max(delay, ready.Sub(now))
	}
	if delay > 0 {
		return delay, nil
	}
	for i, scope := range scopes {
		wait := 40 * time.Millisecond
		if i == 1 {
			wait = time.Second
		}
		if _, e = tx.Exec(ctx, `UPDATE telegram_send_limits SET ready_at=$1 WHERE scope=$2`, now.Add(wait), scope); e != nil {
			return 0, e
		}
	}
	return 0, tx.Commit(ctx)
}
func (t TelegramTransport) Send(ctx context.Context, j workqueue.Job) (workqueue.Outcome, error) {
	var d Delivery
	decoder := json.NewDecoder(bytes.NewReader(j.Payload))
	decoder.UseNumber()
	if decoder.Decode(&d) != nil {
		return workqueue.Outcome{Permanent: true}, ErrTelegram
	}
	original := d
	active, err := activeDelivery(d)
	if err != nil {
		return workqueue.Outcome{Permanent: true}, ErrTelegram
	}
	d = active
	allowed := map[string]bool{"sendRichMessage": true, "sendRichMessageDraft": true, "sendMessageDraft": true, "sendMessage": true, "editMessageText": true, "editMessageReplyMarkup": true, "answerCallbackQuery": true, "sendPhoto": true, "sendAudio": true, "sendVoice": true, "sendDocument": true, "sendMediaGroup": true, "sendChatAction": true}
	if !allowed[d.Method] || t.Token == "" {
		return workqueue.Outcome{Permanent: true}, ErrTelegram
	}
	delay, e := t.reserve(ctx, fmtNumber(d.Payload["chat_id"]), d.Method == "answerCallbackQuery")
	if e != nil {
		return workqueue.Outcome{RetryAfter: time.Second}, ErrTelegram
	}
	if delay > 0 {
		return workqueue.Outcome{Deferred: true, RetryAfter: delay}, nil
	}
	body, contentType, e := t.prepare(ctx, d.Payload)
	if e != nil {
		return workqueue.Outcome{Permanent: true}, ErrTelegram
	}
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = "https://api.telegram.org"
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/bot"+t.Token+"/"+d.Method, body)
	if e != nil {
		return workqueue.Outcome{Permanent: true}, ErrTelegram
	}
	req.Header.Set("Content-Type", contentType)
	var wrote atomic.Bool
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{WroteHeaders: func() { wrote.Store(true) }}))
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, e := client.Do(req)
	if e != nil {
		if wrote.Load() {
			return workqueue.Outcome{Unknown: true}, ErrTelegram
		}
		return workqueue.Outcome{RetryAfter: 2 * time.Second}, ErrTelegram
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if e != nil {
		return workqueue.Outcome{Unknown: true}, ErrTelegram
	}
	var result struct {
		Result     json.RawMessage `json:"result"`
		OK         bool            `json:"ok"`
		Code       int             `json:"error_code"`
		Parameters struct {
			Retry int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return workqueue.Outcome{Unknown: true}, ErrTelegram
	}
	if result.OK && response.StatusCode == 200 {
		t.remember(ctx, d, result.Result)
		return advancePlan(original, false)
	}
	code := result.Code
	if code == 0 {
		code = response.StatusCode
	}
	if original.Method == "plan" && (code == 400 || code == 404) {
		if len(d.Fallback) > 0 {
			return advancePlan(original, true)
		}
		if d.Optional {
			return advancePlan(original, false)
		}
	}
	if code == 429 {
		return workqueue.Outcome{RetryAfter: time.Duration(max(1, min(result.Parameters.Retry, 86400))) * time.Second}, ErrTelegram
	}
	if code == 403 && d.Method != "answerCallbackQuery" {
		_, e = t.Pool.Exec(ctx, `UPDATE users SET delivery_blocked=true WHERE id=$1`, d.Guard.User)
		if e != nil {
			return workqueue.Outcome{Permanent: true}, ErrTelegram
		}
	}
	if code >= 400 && code < 500 {
		return workqueue.Outcome{Permanent: true}, ErrTelegram
	}
	return workqueue.Outcome{Unknown: true}, ErrTelegram
}

var fileReference = regexp.MustCompile(`^unibot-file:([1-9][0-9]*):(photo|audio|voice|document)$`)

func (t TelegramTransport) prepare(ctx context.Context, payload map[string]any) (io.Reader, string, error) {
	files := map[string]string{}
	botSum := sha256.Sum256([]byte(t.Token))
	botHash := hex.EncodeToString(botSum[:])
	var walk func(any) (any, error)
	walk = func(v any) (any, error) {
		switch n := v.(type) {
		case string:
			if !strings.HasPrefix(n, "unibot-file:") {
				return n, nil
			}
			m := fileReference.FindStringSubmatch(n)
			if m == nil {
				return nil, ErrTelegram
			}
			id, _ := strconv.ParseInt(m[1], 10, 64)
			var saved string
			e := t.Pool.QueryRow(ctx, `SELECT telegram_file_id FROM telegram_media WHERE file_id=$1 AND bot_hash=$2 AND kind=$3`, id, botHash, m[2]).Scan(&saved)
			if e == nil {
				return saved, nil
			}
			var storage, checksum string
			var size int64
			e = t.Pool.QueryRow(ctx, `SELECT storage_name,checksum,size FROM files WHERE id=$1`, id).Scan(&storage, &checksum, &size)
			if e != nil || t.FileRoot == "" || storage != filepath.Base(storage) || !regexp.MustCompile(`^[a-f0-9]{32,64}$`).MatchString(storage) || size > 20<<20 {
				return nil, ErrTelegram
			}
			path := filepath.Join(t.FileRoot, storage)
			f, e := os.Open(path)
			if e != nil {
				return nil, ErrTelegram
			}
			h := sha256.New()
			count, e := io.Copy(h, io.LimitReader(f, (20<<20)+1))
			f.Close()
			if e != nil || count != size || hex.EncodeToString(h.Sum(nil)) != checksum {
				return nil, ErrTelegram
			}
			key := "file" + m[1]
			files[key] = path
			return "attach://" + key, nil
		case []any:
			for i, x := range n {
				value, e := walk(x)
				if e != nil {
					return nil, e
				}
				n[i] = value
			}
			return n, nil
		case map[string]any:
			for k, x := range n {
				value, e := walk(x)
				if e != nil {
					return nil, e
				}
				n[k] = value
			}
			return n, nil
		}
		return v, nil
	}
	// Decode through JSON so typed markup/media also receive the same traversal.
	raw, _ := json.Marshal(payload)
	var normalized map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&normalized) != nil {
		return nil, "", ErrTelegram
	}
	if _, e := walk(normalized); e != nil {
		return nil, "", e
	}
	if len(files) == 0 {
		raw, e := json.Marshal(normalized)
		return bytes.NewReader(raw), "application/json", e
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, v := range normalized {
		var value string
		if s, ok := v.(string); ok {
			value = s
		} else {
			b, _ := json.Marshal(v)
			value = string(b)
		}
		if e := writer.WriteField(key, value); e != nil {
			return nil, "", e
		}
	}
	for key, path := range files {
		part, e := writer.CreateFormFile(key, filepath.Base(path))
		if e != nil {
			return nil, "", e
		}
		f, e := os.Open(path)
		if e != nil {
			return nil, "", e
		}
		_, e = io.Copy(part, io.LimitReader(f, 20<<20))
		f.Close()
		if e != nil {
			return nil, "", e
		}
	}
	e := writer.Close()
	return &buf, writer.FormDataContentType(), e
}

// Cache writes are best effort AFTER a confirmed send; failure must never resend a message.
func (t TelegramTransport) remember(ctx context.Context, d Delivery, raw json.RawMessage) {
	sum := sha256.Sum256([]byte(t.Token))
	botHash := hex.EncodeToString(sum[:])
	save := func(reference, kind string, sent map[string]any) {
		m := fileReference.FindStringSubmatch(reference)
		if m == nil {
			return
		}
		var media map[string]any
		if kind == "photo" {
			photos, _ := sent["photo"].([]any)
			if len(photos) > 0 {
				media, _ = photos[len(photos)-1].(map[string]any)
			}
		} else {
			media, _ = sent[kind].(map[string]any)
		}
		if media == nil {
			return
		}
		id, _ := media["file_id"].(string)
		unique, _ := media["file_unique_id"].(string)
		if id == "" || len(id) > 512 || len(unique) > 200 {
			return
		}
		file, _ := strconv.ParseInt(m[1], 10, 64)
		_, _ = t.Pool.Exec(ctx, `INSERT INTO telegram_media(file_id,bot_hash,kind,telegram_file_id,unique_id,updated_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(file_id,bot_hash,kind) DO UPDATE SET telegram_file_id=EXCLUDED.telegram_file_id,unique_id=EXCLUDED.unique_id,updated_at=EXCLUDED.updated_at`, file, botHash, kind, id, unique, time.Now().Unix())
	}
	if d.Method == "sendMediaGroup" {
		var sent []map[string]any
		var source []map[string]any
		encoded, _ := json.Marshal(d.Payload["media"])
		if json.Unmarshal(raw, &sent) != nil || json.Unmarshal(encoded, &source) != nil {
			return
		}
		for i, item := range source {
			if i >= len(sent) {
				break
			}
			reference, _ := item["media"].(string)
			save(reference, "photo", sent[i])
		}
		return
	}
	kind := map[string]string{"sendPhoto": "photo", "sendAudio": "audio", "sendVoice": "voice", "sendDocument": "document"}[d.Method]
	if kind == "" {
		return
	}
	reference, _ := d.Payload[kind].(string)
	var sent map[string]any
	if json.Unmarshal(raw, &sent) == nil {
		save(reference, kind, sent)
	}
}
