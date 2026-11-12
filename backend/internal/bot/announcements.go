package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/announcements"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"github.com/jackc/pgx/v5"
	"html"
	"strconv"
	"strings"
	"time"
)

func (s *session) notices() error {
	parts := strings.Split(s.route, ":")
	if s.route == "u:news" || s.route == "notice:home" {
		return s.reply("<b>اخبار و اطلاعیه‌ها</b>\nبانک اطلاعیه‌ها را ببین یا دسته‌های موردعلاقه‌ات را دنبال کن.", [][]Button{{{Text: "نمایش تمام اطلاعیه‌ها", Data: "notice:bank:0:0", Style: "primary"}}, {b("دسته‌بندی اطلاعیه‌ها", "notice:categories"), {Text: "دنبال‌کردن دسته‌ها", Data: "notice:follows", Style: "success"}}, {b("منوی اصلی", "menu")}}, Guard{})
	}
	if len(parts) == 4 && parts[1] == "follow" {
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		if e := announcements.Follow(s.ctx, s.tx, s.user, id, parts[3] == "on"); e != nil {
			return s.reply("دسته در دسترس نیست.", nil, Guard{})
		}
		s.route = "notice:follows"
	}
	if s.route == "notice:categories" || s.route == "notice:follows" {
		categories, e := announcements.Categories(s.ctx, s.tx, s.user, false)
		if e != nil {
			return e
		}
		rows := [][]Button{}
		for _, c := range categories {
			label := c.Name
			route := fmt.Sprintf("notice:bank:%d:0", c.ID)
			style := "primary"
			if s.route == "notice:follows" {
				label = "☐ " + label
				mode := "on"
				style = "success"
				if c.Following {
					label = "☑ " + c.Name
					mode = "off"
					style = "danger"
				}
				route = fmt.Sprintf("notice:follow:%d:%s", c.ID, mode)
			}
			rows = append(rows, []Button{{Text: label, Data: route, Style: style}})
		}
		rows = append(rows, []Button{b("بازگشت", "notice:home")})
		return s.reply("دستهٔ موردنظر را انتخاب کن؛ ارسال‌های سراسری مستقل از اشتراک هستند.", rows, Guard{})
	}
	if len(parts) == 3 && parts[1] == "view" {
		s.route = "view:" + parts[2]
		return s.dispatch()
	}
	if len(parts) != 4 || parts[1] != "bank" {
		return s.reply("از منوی اطلاعیه‌ها انتخاب کن.", [][]Button{{b("بانک اطلاعیه‌ها", "notice:home")}}, Guard{})
	}
	category, e := strconv.ParseInt(parts[2], 10, 64)
	page, pe := strconv.Atoi(parts[3])
	if e != nil || pe != nil || category < 0 || page < 0 || page > 10000 {
		return s.reply("صفحه معتبر نیست.", nil, Guard{})
	}
	now := time.Now().Unix()
	rows, e := s.tx.Query(s.ctx, `SELECT c.id,c.title,c.version FROM contents c LEFT JOIN announcement_items i ON i.content_id=c.id WHERE c.kind='announcements' AND c.status='published' AND (c.published_at IS NULL OR c.published_at<=$1) AND (c.expires_at IS NULL OR c.expires_at>$1) AND ($2::bigint=0 OR (i.category_id=$2 AND EXISTS(SELECT 1 FROM announcement_categories WHERE id=$2 AND active))) AND ($3 OR NOT $3) AND (COALESCE(c.metadata::jsonb->>'audience_tag','')='' OR c.metadata::jsonb->>'audience_tag'=ANY($4)) ORDER BY c.pinned DESC,c.id DESC LIMIT 11 OFFSET $5`, now, category, s.approved, s.tags, page*10)
	if e != nil {
		return e
	}
	type item struct {
		id      int64
		title   string
		version int
	}
	items := []item{}
	for rows.Next() {
		var c item
		if e = rows.Scan(&c.id, &c.title, &c.version); e != nil {
			rows.Close()
			return e
		}
		items = append(items, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	more := len(items) > 10
	if more {
		items = items[:10]
	}
	body := fmt.Sprintf("<b>بانک اطلاعیه‌ها · صفحهٔ %d</b>", page+1)
	versions := map[string]int{}
	buttons := [][]Button{}
	for j, c := range items {
		label := html.EscapeString(c.title)
		id := strconv.FormatInt(c.id, 10)
		if s.engine.Username != "" {
			label = `<a href="https://t.me/` + s.engine.Username + `?start=notice_` + id + `">` + label + `</a>`
		} else {
			buttons = append(buttons, []Button{b(c.title, "notice:view:"+id)})
		}
		body += fmt.Sprintf("\n\n%d. <b>%s</b>", page*10+j+1, label)
		versions[id] = c.version
	}
	if len(items) == 0 {
		body += "\n\nهنوز اطلاعیه‌ای منتشر نشده است."
	}
	nav := []Button{}
	if page > 0 {
		nav = append(nav, b("→ قبلی", fmt.Sprintf("notice:bank:%d:%d", category, page-1)))
	}
	nav = append(nav, b(strconv.Itoa(page+1), s.route))
	if more {
		nav = append(nav, b("بعدی ←", fmt.Sprintf("notice:bank:%d:%d", category, page+1)))
	}
	buttons = append(buttons, nav, []Button{b("دسته‌ها", "notice:categories"), b("دنبال‌کردن", "notice:follows")}, []Button{b("بازگشت", "notice:home")})
	guard := Guard{Content: versions}
	if s.update.Callback != nil {
		return s.emit("editMessageText", map[string]any{"chat_id": s.chat, "message_id": s.message, "text": body, "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}, "reply_markup": Markup{Inline: buttons}}, guard, 10)
	}
	return s.reply(body, buttons, guard)
}

// ExpandNotice performs a single bounded chunk and atomically queues the next chunk.
func (e Engine) ExpandNotice(ctx context.Context, tx pgx.Tx, j workqueue.Job) error {
	var id int64
	if json.Unmarshal(j.Payload, &id) != nil {
		return catalog.ErrInvalid
	}
	var content, maxUser, cursor, category int64
	var version, enqueued int
	var mode, state string
	err := tx.QueryRow(ctx, `SELECT content_id,content_version,max_user_id,cursor_id,COALESCE(category_id,0),audience,state,enqueued_count FROM announcement_dispatches WHERE id=$1 FOR UPDATE`, id).Scan(&content, &version, &maxUser, &cursor, &category, &mode, &state, &enqueued)
	if err != nil {
		return err
	}
	if state != "pending" {
		return nil
	}
	c, err := catalog.Get(ctx, tx, content)
	if err != nil {
		return err
	}
	if c.Status != "published" || c.Version != version {
		_, err = tx.Exec(ctx, `UPDATE announcement_dispatches SET state='cancelled' WHERE id=$1`, id)
		return err
	}
	rows, err := tx.Query(ctx, `SELECT u.id,u.telegram_id,u.tags,EXISTS(SELECT 1 FROM student_verifications v WHERE v.user_id=u.id AND v.status='approved') FROM users u WHERE u.id>$1 AND u.id<=$2 AND u.status='active' AND NOT u.delivery_blocked AND ($3='all' OR EXISTS(SELECT 1 FROM announcement_subscriptions s JOIN announcement_categories ac ON ac.id=s.category_id AND ac.active WHERE s.user_id=u.id AND s.category_id=$4)) ORDER BY u.id LIMIT 50`, cursor, maxUser, mode, category)
	if err != nil {
		return err
	}
	type recipient struct {
		id, chat int64
		tags     string
		approved bool
	}
	users := []recipient{}
	for rows.Next() {
		var u recipient
		var chat string
		if err = rows.Scan(&u.id, &chat, &u.tags, &u.approved); err != nil {
			rows.Close()
			return err
		}
		u.chat, _ = strconv.ParseInt(chat, 10, 64)
		users = append(users, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if enqueued+len(users) > announcements.MaxAudience {
		_, err = tx.Exec(ctx, `UPDATE announcement_dispatches SET state='cancelled' WHERE id=$1`, id)
		return err
	}
	doc, err := loadDocument(ctx, tx, c.ID)
	if err != nil {
		return err
	}
	for _, u := range users {
		cursor = u.id
		tags := strings.Fields(strings.NewReplacer(",", " ", "،", " ").Replace(u.tags))
		if !catalog.Visible(c, tags, u.approved, time.Now().Unix()) {
			continue
		}
		s := session{engine: e, ctx: ctx, tx: tx, user: u.id, chat: u.chat, tags: tags, approved: u.approved, route: "view:" + strconv.FormatInt(c.ID, 10), activeModule: "content", keyPrefix: fmt.Sprintf("notice:%d:user:%d", id, u.id), dispatchID: id}
		enqueued++
		if err = s.sendDocument(c, doc); err != nil {
			return err
		}
	}
	state = "pending"
	if len(users) < 50 {
		state = "queued"
	}
	_, err = tx.Exec(ctx, `UPDATE announcement_dispatches SET cursor_id=$1,state=$2,enqueued_count=$4 WHERE id=$3`, cursor, state, id, enqueued)
	if err != nil || state != "pending" {
		return err
	}
	return workqueue.Enqueue(ctx, tx, workqueue.Input{Key: fmt.Sprintf("notice:expand:%d:%d", id, cursor), Kind: "notice.expand", Payload: j.Payload})
}
