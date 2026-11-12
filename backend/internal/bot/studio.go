package bot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/rich"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"github.com/jackc/pgx/v5"
	"html"
	"strconv"
	"strings"
	"time"
)

func loadDocument(ctx context.Context, tx pgx.Tx, id int64) (*rich.Document, error) {
	var raw string
	e := tx.QueryRow(ctx, `SELECT document FROM advanced_contents WHERE content_id=$1`, id).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var doc rich.Document
	if json.Unmarshal([]byte(raw), &doc) != nil || rich.Validate(doc) != nil {
		return nil, errors.New("stored block document invalid")
	}
	return &doc, nil
}
func (s *session) sendDocument(c catalog.Content, doc *rich.Document) error {
	if doc == nil || !doc.Rich || !s.engine.RichEnabled {
		return s.standardDocument(c, doc)
	}
	steps := []Delivery{}
	previous := s.planned
	s.planned = &steps
	e := s.standardDocument(c, doc)
	s.planned = previous
	if e != nil {
		return e
	}
	audio := []Delivery{}
	fallback := []Delivery{}
	for _, step := range steps {
		if step.Method == "sendAudio" || step.Method == "sendVoice" {
			audio = append(audio, step)
		} else {
			fallback = append(fallback, step)
		}
	}
	nativeDoc := *doc
	if details, _ := directoryPresentation(c.Metadata); details != "" {
		nativeDoc.Blocks = append(append([]rich.Block{}, doc.Blocks...), rich.Block{Type: "text", Enabled: true, Text: details})
	}
	native := rich.Native(nativeDoc)
	blocks, _ := native["blocks"].([]map[string]any)
	native["blocks"] = append([]map[string]any{{"type": "heading", "size": 2, "text": c.Title}}, blocks...)
	guard := Guard{Content: map[string]int{strconv.FormatInt(c.ID, 10): c.Version}, Dispatch: s.dispatchID, User: s.user, Module: s.module(), Route: s.route}
	payload := map[string]any{"chat_id": s.chat, "rich_message": native}
	if len(steps) > 0 {
		payload["reply_markup"] = steps[len(steps)-1].Payload["reply_markup"]
	}
	audio = append(audio, Delivery{Method: "sendRichMessage", Payload: payload, Guard: guard, Fallback: fallback})
	plan := Delivery{Method: "plan", Payload: map[string]any{"chat_id": s.chat}, Guard: guard, Steps: audio}
	raw, e := json.Marshal(plan)
	if e != nil {
		return e
	}
	s.sequence++
	key := fmt.Sprintf("update:%d:%d", s.update.ID, s.sequence)
	if s.keyPrefix != "" {
		key = fmt.Sprintf("%s:%d", s.keyPrefix, s.sequence)
	}
	priority := 10
	if s.dispatchID > 0 {
		priority = -10
	}
	return workqueue.Enqueue(s.ctx, s.tx, workqueue.Input{Key: key, Kind: "telegram.send", Stream: fmt.Sprintf("user:%d", s.user), Payload: raw, External: true, Priority: priority})
}
func (s *session) standardDocument(c catalog.Content, doc *rich.Document) error {
	guard := Guard{Content: map[string]int{strconv.FormatInt(c.ID, 10): c.Version}, Dispatch: s.dispatchID}
	if doc == nil {
		return s.reply("<b>"+html.EscapeString(c.Title)+"</b>\n\n"+html.EscapeString(c.Body), [][]Button{{b("منوی اصلی", "menu")}}, guard)
	}
	directory, rows := directoryPresentation(c.Metadata)
	values := map[string]int64{}
	rs, e := s.tx.Query(s.ctx, `SELECT block_key,value FROM interaction_values WHERE user_id=$1 AND content_id=$2`, s.user, c.ID)
	if e != nil {
		return e
	}
	for rs.Next() {
		var k string
		var v int64
		if e = rs.Scan(&k, &v); e != nil {
			rs.Close()
			return e
		}
		values[k] = v
	}
	e = rs.Err()
	rs.Close()
	if e != nil {
		return e
	}
	actions := [][]any{}
	for bi, block := range doc.Blocks {
		if !block.Enabled {
			continue
		}
		if block.Type != "buttons" {
			continue
		}
		row := []Button{}
		for ii, item := range block.Items {
			if item.Disabled || item.Audience != "" && !hasTag(s.tags, item.Audience) {
				continue
			}
			if item.Break && len(row) > 0 {
				rows = append(rows, row)
				row = nil
			}
			button := Button{Text: item.Icon + item.Text, Style: item.Style}
			if button.Style == "default" {
				button.Style = ""
			}
			switch item.Action {
			case "url":
				button.URL = item.Value
			case "copy":
				button.Copy = &CopyText{Text: item.Value}
			default:
				key := interactionKey(bi, ii, c.Version, item)
				value := values[key]
				if item.Action == "toggle" {
					button.Text = "☐ " + button.Text
					if value != 0 {
						button.Text = "☑ " + item.Text
					}
				}
				if item.Action == "increment" || item.Action == "decrement" {
					button.Text += fmt.Sprintf(" · %d", value)
				}
				tok := token() + token()
				button.Data = "ra:" + tok
				actions = append(actions, []any{tok, s.user, c.ID, c.Version, bi, ii, time.Now().Unix() + 86400})
			}
			row = append(row, button)
			if len(row) >= block.Columns {
				rows = append(rows, row)
				row = nil
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	if len(actions) > 0 {
		if _, e = s.tx.CopyFrom(s.ctx, pgx.Identifier{"message_actions"}, []string{"token", "user_id", "content_id", "content_version", "block_index", "item_index", "expires_at"}, pgx.CopyFromRows(actions)); e != nil {
			return e
		}
	}
	texts := []string{"<b>" + html.EscapeString(c.Title) + "</b>"}
	if directory != "" {
		texts = append(texts, directory)
	}
	for _, block := range doc.Blocks {
		if !block.Enabled {
			continue
		}
		body := rich.Text(block)
		if strings.TrimSpace(body) != "" {
			texts = append(texts, body)
		}
		switch block.Type {
		case "image", "audio":
			method, param, kind := "sendPhoto", "photo", "photo"
			if block.Type == "audio" {
				method, param, kind = "sendAudio", "audio", "audio"
				if block.Voice {
					method, param, kind = "sendVoice", "voice", "voice"
				}
			}
			if e = s.emit(method, map[string]any{"chat_id": s.chat, param: fmt.Sprintf("unibot-file:%d:%s", block.File, kind)}, guard, 10); e != nil {
				return e
			}
		case "slideshow":
			media := []map[string]any{}
			for _, slide := range block.Slides {
				media = append(media, map[string]any{"type": "photo", "media": fmt.Sprintf("unibot-file:%d:photo", slide.File), "caption": slide.Caption})
			}
			if e = s.emit("sendMediaGroup", map[string]any{"chat_id": s.chat, "media": media}, guard, 10); e != nil {
				return e
			}
		case "quiz":
			if e = s.reply("برای شروع آزمون از بخش آموزش استفاده کن.", nil, guard); e != nil {
				return e
			}
		}
	}
	rows = append(rows, []Button{b("منوی اصلی", "menu")})
	return s.reply(strings.Join(texts, "\n\n"), rows, guard)
}
func interactionKey(b, i, v int, item rich.Item) string {
	sum := sha256.Sum256([]byte(item.Value))
	value := fmt.Sprintf("%x", sum[:8])
	if item.Action == "toggle" {
		return fmt.Sprintf("%d:toggle:%d:%d", b, v, i)
	}
	if item.Action == "increment" || item.Action == "decrement" {
		return fmt.Sprintf("%d:quantity:%d:%s", b, v, value)
	}
	return fmt.Sprintf("%d:choice:%d:%s", b, v, value)
}
func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
func (s *session) richAction() error {
	if !strings.HasPrefix(s.route, "ra:") {
		return s.reply("دکمهٔ پیام قبلی نیاز به تازه‌سازی دارد؛ مطلب را دوباره باز کن.", [][]Button{{b("منوی اصلی", "menu")}}, Guard{})
	}
	tok := strings.TrimPrefix(s.route, "ra:")
	var content int64
	var version, bi, ii int
	e := s.tx.QueryRow(s.ctx, `UPDATE message_actions SET consumed=true WHERE token=$1 AND user_id=$2 AND NOT consumed AND expires_at>$3 RETURNING content_id,content_version,block_index,item_index`, tok, s.user, time.Now().Unix()).Scan(&content, &version, &bi, &ii)
	if e == pgx.ErrNoRows {
		return s.reply("این دکمه قبلاً استفاده شده یا منقضی است؛ مطلب را دوباره باز کن.", nil, Guard{})
	}
	if e != nil {
		return e
	}
	c, e := catalog.ReadPublished(s.ctx, s.tx, content, s.tags, s.approved)
	if e != nil || c.Version != version {
		return s.reply("مطلب تغییر کرده؛ دوباره آن را باز کن.", nil, Guard{})
	}
	doc, e := loadDocument(s.ctx, s.tx, content)
	if e != nil {
		return e
	}
	if doc == nil || bi < 0 || bi >= len(doc.Blocks) || ii < 0 || ii >= len(doc.Blocks[bi].Items) {
		return catalog.ErrInvalid
	}
	item := doc.Blocks[bi].Items[ii]
	if item.Disabled || item.Audience != "" && !hasTag(s.tags, item.Audience) {
		return nil
	}
	switch item.Action {
	case "menu":
		s.route = item.Value
		return s.dispatch()
	case "content":
		s.route = "view:" + item.Value
		return s.dispatch()
	case "quiz", "calculator":
		return s.reply("این فعالیت در بخش آموزش در دسترس قرار می‌گیرد.", nil, Guard{})
	case "toggle", "increment", "decrement", "confirm", "cancel":
		key := interactionKey(bi, ii, version, item)
		_, e = s.tx.Exec(s.ctx, `INSERT INTO interaction_values(user_id,content_id,block_key,value,updated_at) VALUES($1,$2,$3,0,$4) ON CONFLICT DO NOTHING`, s.user, content, key, time.Now().Unix())
		if e != nil {
			return e
		}
		var value int64
		e = s.tx.QueryRow(s.ctx, `SELECT value FROM interaction_values WHERE user_id=$1 AND content_id=$2 AND block_key=$3 FOR UPDATE`, s.user, content, key).Scan(&value)
		if e != nil {
			return e
		}
		switch item.Action {
		case "toggle":
			value = 1 - value
		case "confirm":
			value = 1
		case "cancel":
			value = 0
		case "increment":
			value = max(item.Min, min(item.Max, value+item.Step))
		case "decrement":
			value = max(item.Min, min(item.Max, value-item.Step))
		}
		_, e = s.tx.Exec(s.ctx, `UPDATE interaction_values SET value=$1,updated_at=$2 WHERE user_id=$3 AND content_id=$4 AND block_key=$5`, value, time.Now().Unix(), s.user, content, key)
		if e != nil {
			return e
		}
	}
	if _, e = s.tx.Exec(s.ctx, `UPDATE message_actions SET consumed=true WHERE user_id=$1 AND content_id=$2 AND content_version=$3 AND NOT consumed`, s.user, c.ID, c.Version); e != nil {
		return e
	}
	return s.sendDocument(c, doc)
}
