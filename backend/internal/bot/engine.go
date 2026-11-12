// Package bot ports Telegram update handling without network calls inside transactions.
package bot

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/cache"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/rich"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"github.com/jackc/pgx/v5"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed testdata/menu-default.json
var stockMenu []byte

type From struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	First    string `json:"first_name"`
	Last     string `json:"last_name"`
	Bot      bool   `json:"is_bot"`
}
type Contact struct {
	Phone string `json:"phone_number"`
	User  int64  `json:"user_id"`
}
type Message struct {
	Contact *Contact `json:"contact,omitempty"`
	ID      int64    `json:"message_id"`
	Date    int64    `json:"date"`
	From    From     `json:"from"`
	Chat    struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	Text string `json:"text"`
}
type Update struct {
	ID       int64    `json:"update_id"`
	Message  *Message `json:"message"`
	Callback *struct {
		ID      string  `json:"id"`
		From    From    `json:"from"`
		Message Message `json:"message"`
		Data    string  `json:"data"`
	} `json:"callback_query"`
}
type Button struct {
	Text  string    `json:"text"`
	Data  string    `json:"callback_data,omitempty"`
	Style string    `json:"style,omitempty"`
	URL   string    `json:"url,omitempty"`
	Copy  *CopyText `json:"copy_text,omitempty"`
}
type CopyText struct {
	Text string `json:"text"`
}
type Markup struct {
	Inline   [][]Button `json:"inline_keyboard,omitempty"`
	Keyboard [][]Button `json:"keyboard,omitempty"`
	Resize   bool       `json:"resize_keyboard,omitempty"`
}
type Guard struct {
	Leisure       int64                     `json:"leisure,omitempty"`
	AdminView     bool                      `json:"adminView,omitempty"`
	Approved      bool                      `json:"approved,omitempty"`
	Community     map[string]map[string]int `json:"community,omitempty"`
	Personal      map[string]int            `json:"personal,omitempty"`
	Ticket        int64                     `json:"ticket,omitempty"`
	TicketMessage int64                     `json:"ticketMessage,omitempty"`
	Actor         int64                     `json:"actor,omitempty"`
	Permission    string                    `json:"permission,omitempty"`
	Dispatch      int64                     `json:"dispatch,omitempty"`
	Module        string                    `json:"module,omitempty"`
	User          int64                     `json:"user"`
	Content       map[string]int            `json:"content,omitempty"`
	Books         []int64                   `json:"books,omitempty"`
	Route         string                    `json:"route"`
}
type Delivery struct {
	Steps    []Delivery     `json:"steps,omitempty"`
	Cursor   int            `json:"cursor,omitempty"`
	Fallback []Delivery     `json:"fallback,omitempty"`
	Optional bool           `json:"optional,omitempty"`
	Delay    int            `json:"delay,omitempty"`
	Method   string         `json:"method"`
	Payload  map[string]any `json:"payload"`
	Guard    Guard          `json:"guard"`
}
type Node struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Action  string   `json:"action"`
	Value   string   `json:"value"`
	Parent  *string  `json:"parent_id"`
	Enabled bool     `json:"enabled"`
	Style   string   `json:"style"`
	Sort    int      `json:"sort"`
	Width   string   `json:"row_width"`
	Aliases []string `json:"aliases"`
}
type Menu struct {
	Buttons []Node `json:"buttons"`
	Columns int    `json:"columns"`
}
type Engine struct {
	RichEnabled bool
	AdminURL    string
	AppKey      string
	Username    string
	PublicCache *cache.Cache
}
type session struct {
	planned                *[]Delivery
	sensitive              bool
	actorID                int64
	super                  bool
	permissions            []string
	keyPrefix              string
	dispatchID             int64
	activeModule           string
	engine                 Engine
	tx                     pgx.Tx
	ctx                    context.Context
	update                 Update
	from                   From
	chat, message, user    int64
	route                  string
	typed, approved, admin bool
	state                  map[string]any
	settings               map[string]json.RawMessage
	tags                   []string
	menu                   Menu
	sequence               int
}

var ErrUnavailable = errors.New("bot operation unavailable")

func token() string {
	b := make([]byte, 6)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func number(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		r, _ := n.Int64()
		return r
	}
	return 0
}
func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func text(v any) string            { s, _ := v.(string); return s }
func b(label, route string) Button { return Button{Text: label, Data: route} }
func (e Engine) Handle(ctx context.Context, tx pgx.Tx, j workqueue.Job) error {
	var u Update
	if json.Unmarshal(j.Payload, &u) != nil {
		return errors.New("invalid stored update")
	}
	s := session{engine: e, tx: tx, ctx: ctx, update: u, state: map[string]any{}, settings: map[string]json.RawMessage{}}
	var msg *Message
	if u.Callback != nil {
		s.from = u.Callback.From
		msg = &u.Callback.Message
		s.route = u.Callback.Data
		s.message = msg.ID
	} else if u.Message != nil {
		msg = u.Message
		s.from = msg.From
		s.route = msg.Text
		s.typed = true
	}
	if msg == nil || s.from.Bot || s.from.ID <= 0 || msg.Chat.Type != "private" || msg.Chat.ID != s.from.ID {
		return nil
	}
	s.chat = msg.Chat.ID
	if msg.Contact != nil && j.ID > 0 {
		if _, err := tx.Exec(ctx, `UPDATE work_jobs SET payload=jsonb_build_object('update_id',$1::bigint,'redacted',true) WHERE id=$2 AND kind='telegram.update'`, u.ID, j.ID); err != nil {
			return err
		}
	}
	var state, tags, status string
	now := time.Now().Unix()
	err := tx.QueryRow(ctx, `INSERT INTO users(telegram_id,first_name,last_name,username,created_at,last_seen,interactions) VALUES($1,$2,$3,$4,$5,$5,1) ON CONFLICT(telegram_id) DO UPDATE SET first_name=EXCLUDED.first_name,last_name=EXCLUDED.last_name,username=EXCLUDED.username,last_seen=EXCLUDED.last_seen,interactions=users.interactions+1 RETURNING id,COALESCE(conversation,'{}'),tags,status`, strconv.FormatInt(s.from.ID, 10), s.from.First, s.from.Last, s.from.Username, now).Scan(&s.user, &state, &tags, &status)
	if err != nil {
		return err
	}
	if status != "active" {
		return nil
	}
	if json.Unmarshal([]byte(state), &s.state) != nil {
		return errors.New("invalid stored conversation")
	}
	if s.state == nil {
		s.state = map[string]any{}
	}
	s.tags = strings.Fields(strings.NewReplacer(",", " ", "،", " ", ";", " ").Replace(tags))
	rows, err := tx.Query(ctx, `SELECT name,value FROM settings WHERE NOT is_secret AND name IN ('bot_menu','bot_state','emergency','modules','maintenance_modules','maintenance_text','welcome','rules','help','bot_info')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		s.settings[key] = json.RawMessage(value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM student_verifications WHERE user_id=$1 AND status='approved'),EXISTS(SELECT 1 FROM admin_telegram_links l JOIN admins a ON a.id=l.admin_id WHERE l.telegram_id=$2 AND a.active)`, s.user, strconv.FormatInt(s.from.ID, 10)).Scan(&s.approved, &s.admin); err != nil {
		return err
	}
	if s.admin {
		var permissions string
		err = tx.QueryRow(ctx, `SELECT a.id,a.is_super,r.permissions FROM admin_telegram_links l JOIN admins a ON a.id=l.admin_id JOIN roles r ON r.id=a.role_id WHERE l.telegram_id=$1 AND a.active`, strconv.FormatInt(s.from.ID, 10)).Scan(&s.actorID, &s.super, &permissions)
		if err != nil {
			return err
		}
		if json.Unmarshal([]byte(permissions), &s.permissions) != nil {
			return ErrUnavailable
		}
	}
	if u.Callback != nil {
		if err = s.emit("answerCallbackQuery", map[string]any{"callback_query_id": u.Callback.ID}, Guard{User: s.user, Route: "callback"}, 100); err != nil {
			return err
		}
	}
	var emergency struct {
		Active bool `json:"active"`
	}
	json.Unmarshal(s.settings["emergency"], &emergency)
	var botState string
	json.Unmarshal(s.settings["bot_state"], &botState)
	if emergency.Active || botState == "off" || botState == "" {
		if !s.admin {
			return nil
		}
	}
	if json.Unmarshal(s.settings["bot_menu"], &s.menu) != nil || len(s.menu.Buttons) == 0 {
		json.Unmarshal(stockMenu, &s.menu)
	}
	if s.engine.Username == "" {
		var info struct {
			Username string `json:"username"`
		}
		json.Unmarshal(s.settings["bot_info"], &info)
		s.engine.Username = info.Username
	}
	if strings.HasPrefix(s.route, "/start ") {
		key := strings.TrimPrefix(s.route, "/start ")
		if strings.HasPrefix(key, "book_") {
			s.route = "ubooks:view:" + strings.TrimPrefix(key, "book_")
		} else if strings.HasPrefix(key, "notice_") {
			s.route = "view:" + strings.TrimPrefix(key, "notice_")
		} else {
			s.route = "view:" + strings.TrimPrefix(key, "content_")
		}
	}
	if s.route == "/start" || s.route == "/menu" || s.route == "منوی اصلی" || s.route == "menu" {
		s.route = "menu"
		delete(s.state, "professor_form")
		delete(s.state, "go_form")
		delete(s.state, "support_chat")
		delete(s.state, "verification_active")
		delete(s.state, "book_search")
		delete(s.state, "go_reply_routes")
		delete(s.state, "go_keyboard_hash")
	}
	if s.typed && s.route != "menu" {
		if route := text(object(s.state["go_reply_routes"])[s.route]); route != "" {
			s.route = route
		}
		parent := text(s.state["menu_parent"])
		for _, node := range s.menu.Buttons {
			matches := catalog.Normalize(node.Label) == catalog.Normalize(s.route)
			for _, alias := range node.Aliases {
				matches = matches || catalog.Normalize(alias) == catalog.Normalize(s.route)
			}
			if matches && node.Enabled && (node.Parent == nil || *node.Parent == parent) {
				s.route = "button:" + node.ID
				break
			}
		}
		labels := map[string]string{"معرفی استاد": "u:professor:new", "جست‌وجوی همهٔ اساتید": "u:professor:search", "فهرست همهٔ اساتید": "u:professor:all:0", "لغو و بازگشت": "u:professor:cancel", "استادشناسی": "u:people:professors:0", "جست‌وجوی کتاب": "ubooks:search", "نمایش همه کتاب‌ها": "ubooks:home", "دسته‌بندی کتاب‌ها": "ubooks:categories"}
		if route, ok := labels[s.route]; ok {
			s.route = route
		}
	}
	if strings.HasPrefix(s.route, "button:") {
		delete(s.state, "professor_form")
		delete(s.state, "go_form")
		delete(s.state, "support_chat")
		delete(s.state, "verification_active")
		delete(s.state, "book_search")
		id := strings.TrimPrefix(s.route, "button:")
		found := false
		for _, node := range s.menu.Buttons {
			if node.ID == id && s.nodeEnabled(id) {
				found = true
				if (node.ID == "adminconsole" || strings.HasPrefix(node.Action, "ac:")) && !s.admin {
					return nil
				}
				s.state["menu_parent"] = id
				if node.Action == "submenu" {
					s.route = "submenu:" + id
				} else {
					switch node.Action {
					case "text", "url":
						s.route = "custom:" + node.Action
					case "content":
						s.route = "view:" + node.Value
					default:
						s.route = node.Action
					}
				}
				break
			}
		}
		if !found {
			return s.reply("این گزینه در دسترس نیست.", nil, Guard{})
		}
	}
	if (!s.typed || u.Message != nil && s.route != u.Message.Text) && !strings.HasPrefix(s.route, "u:professor:") && s.route != "u:people:professors:0" {
		delete(s.state, "professor_form")
	}
	if (!s.typed || u.Message != nil && s.route != u.Message.Text) && !strings.HasPrefix(s.route, "ubooks:") && s.route != "u:library" {
		delete(s.state, "book_search")
	}
	navigation := !s.typed || s.update.Message != nil && s.route != s.update.Message.Text
	if navigation && !strings.HasPrefix(s.route, "form:") {
		delete(s.state, "go_form")
	}
	if navigation && !strings.HasPrefix(s.route, "support") {
		delete(s.state, "support_chat")
	}
	if navigation && !strings.HasPrefix(s.route, "verify") {
		delete(s.state, "verification_active")
	}
	s.activeModule = s.module()
	if s.moduleBlocked() {
		return s.reply("این بخش فعلاً در دسترس نیست.", nil, Guard{Route: "system:feedback", Module: "feedback"})
	}
	err = s.dispatch()
	if errors.Is(err, catalog.ErrInvalid) || errors.Is(err, catalog.ErrMissing) || errors.Is(err, catalog.ErrConflict) {
		err = s.reply("اطلاعات معتبر نیست یا این مورد تغییر کرده؛ دوباره بخش مربوط را باز کن.", [][]Button{{b("منوی اصلی", "menu")}}, Guard{Module: "feedback", Route: "system:feedback"})
	}
	if err == nil && s.sensitive && j.ID > 0 {
		_, err = tx.Exec(ctx, `UPDATE work_jobs SET payload=jsonb_build_object('update_id',$1::bigint,'redacted',true) WHERE id=$2 AND kind='telegram.update'`, u.ID, j.ID)
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	if string(raw) == state {
		return nil
	}
	_, err = tx.Exec(ctx, "UPDATE users SET conversation=$1 WHERE id=$2", string(raw), s.user)
	return err
}
func (s *session) emit(method string, payload map[string]any, guard Guard, priority int) error {
	s.sequence++
	if s.dispatchID > 0 {
		priority = -10
	}
	if strings.HasPrefix(s.route, "ac:") {
		guard.AdminView = true
	}
	if strings.HasPrefix(s.route, "ac:") && guard.Actor == 0 {
		guard.Actor = s.actorID
	}
	if guard.Module == "" {
		if guard.Route == "callback" || guard.Route == "system:feedback" {
			guard.Module = "feedback"
		} else {
			guard.Module = s.module()
		}
	}
	if guard.Module == "market" || guard.Module == "personal" || guard.Module == "directory" && (s.route == "fun:channels" || strings.HasPrefix(s.route, "sch:") || strings.HasPrefix(s.route, "u:links:group") || strings.HasPrefix(s.route, "u:legacylinks") || text(object(s.state["go_form"])["kind"]) == "channels") {
		guard.Approved = true
	}
	if guard.User == 0 {
		guard.User = s.user
	}
	if guard.Route == "" {
		guard.Route = s.route
	}
	if guard.Dispatch == 0 {
		guard.Dispatch = s.dispatchID
	}
	delivery := Delivery{Method: method, Payload: payload, Guard: guard}
	if s.planned != nil {
		*s.planned = append(*s.planned, delivery)
		return nil
	}
	raw, err := json.Marshal(delivery)
	if err != nil {
		return err
	}
	stream := fmt.Sprintf("user:%d", s.user)
	if method == "answerCallbackQuery" {
		stream = ""
	}
	key := fmt.Sprintf("update:%d:%d", s.update.ID, s.sequence)
	if s.keyPrefix != "" {
		key = fmt.Sprintf("%s:%d", s.keyPrefix, s.sequence)
	}
	return workqueue.Enqueue(s.ctx, s.tx, workqueue.Input{Key: key, Kind: "telegram.send", Stream: stream, Payload: raw, External: true, Priority: priority})
}
func (s *session) reply(body string, rows [][]Button, guard Guard) error {
	if visibleLength(body) > 3900 {
		plain := html.UnescapeString(htmlTags.ReplaceAllString(linkTags.ReplaceAllString(body, "$2 ($1)"), ""))
		parts := splitPlain(plain, 3900)
		for i, part := range parts {
			payload := map[string]any{"chat_id": s.chat, "text": part}
			if i == len(parts)-1 && len(rows) > 0 {
				payload["reply_markup"] = Markup{Inline: rows}
			}
			if err := s.emit("sendMessage", payload, guard, 10); err != nil {
				return err
			}
		}
		return nil
	}

	payload := map[string]any{"chat_id": s.chat, "text": body, "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}}
	if len(rows) > 0 {
		payload["reply_markup"] = Markup{Inline: rows}
	}
	return s.emit("sendMessage", payload, guard, 10)
}
func (s *session) menuReply(body string, buttons [][]Button) error {
	return s.emit("sendMessage", map[string]any{"chat_id": s.chat, "text": body, "reply_markup": Markup{Keyboard: buttons, Resize: true}}, Guard{}, 10)
}
func (s *session) dispatch() error {
	if strings.HasPrefix(s.route, "u:people:") || strings.HasPrefix(s.route, "u:persons:") || strings.HasPrefix(s.route, "u:roles:") {
		return s.topicNavigation()
	}
	if s.route == "verify" || s.route == "/verify" || strings.HasPrefix(s.route, "verify:") || s.state["verification_active"] == true && s.typed && s.update.Message != nil && s.route == s.update.Message.Text {
		return s.verification()
	}
	if strings.HasPrefix(s.route, "form:") || s.typed && s.update.Message != nil && s.route == s.update.Message.Text && len(object(s.state["go_form"])) > 0 {
		return s.form()
	}
	if strings.HasPrefix(s.route, "ac:") {
		return s.adminConsole()
	}
	if s.route == "support" || strings.HasPrefix(s.route, "support:") || s.typed && s.update.Message != nil && s.route == s.update.Message.Text && len(object(s.state["support_chat"])) > 0 {
		return s.supportChat()
	}
	if s.route == "u:personal" || s.route == "personal" || strings.HasPrefix(s.route, "u:personal:") || s.route == "fun:channels" || strings.HasPrefix(s.route, "sch:") || s.route == "market" || strings.HasPrefix(s.route, "market:") || s.route == "lostfound" || strings.HasPrefix(s.route, "lost:") {
		if s.route == "personal" {
			s.route = "u:personal"
		}
		return s.community()
	}
	if strings.HasPrefix(s.route, "fun:") {
		return s.leisure()
	}

	if s.route == "u:news" || strings.HasPrefix(s.route, "notice:") {
		return s.notices()
	}
	if strings.HasPrefix(s.route, "ra:") || strings.HasPrefix(s.route, "advanced:") {
		return s.richAction()
	}
	if strings.HasPrefix(s.route, "u:professor:") || s.route == "u:people:professors:0" || s.route == "u:people:contacts:0" || len(object(s.state["professor_form"])) > 0 {
		return s.professors()
	}
	if strings.HasPrefix(s.route, "ubooks:") || s.route == "u:library" || s.typed && s.state["book_search"] == true {
		return s.books()
	}
	if strings.HasPrefix(s.route, "list:") || strings.HasPrefix(s.route, "u:items:") || strings.HasPrefix(s.route, "u:systems") || strings.HasPrefix(s.route, "u:links") || strings.HasPrefix(s.route, "u:legacylinks") || s.route == "u:faq" {
		return s.genericContent()
	}
	if strings.HasPrefix(s.route, "u:channelview:") {
		s.route = "view:" + strings.TrimPrefix(s.route, "u:channelview:")
	}
	if strings.HasPrefix(s.route, "view:") {
		id, _ := strconv.ParseInt(strings.TrimPrefix(s.route, "view:"), 10, 64)
		c, err := catalog.ReadPublished(s.ctx, s.tx, id, s.tags, s.approved)
		if err != nil {
			return s.reply("مطلب پیدا نشد یا دسترسی آن تغییر کرده است.", nil, Guard{})
		}
		switch c.Kind {
		case "professors", "contacts", "links":
			s.activeModule = "directory"
		case "resources", "handouts":
			s.activeModule = "library"
		case "formulas":
			s.activeModule = "formulas"
		}
		if s.moduleBlocked() {
			return s.reply("این بخش فعلاً در دسترس نیست.", nil, Guard{Route: "system:feedback", Module: "feedback"})
		}
		doc, loadErr := loadDocument(s.ctx, s.tx, c.ID)
		if loadErr != nil {
			return loadErr
		}
		if doc != nil {
			return s.sendDocument(c, doc)
		}
		body := "<b>" + html.EscapeString(c.Title) + "</b>\n\n" + html.EscapeString(c.Body)
		for _, key := range []string{"office_phone", "foreign_id", "email"} {
			if value := catalog.MetaText(c.Metadata, key); value != "" {
				body += "\n" + html.EscapeString(value)
			}
		}
		directory, rows := directoryPresentation(c.Metadata)
		body += directory
		rows = append(rows, []Button{b("بازگشت", "menu")})
		return s.reply(body, rows, Guard{Content: map[string]int{strconv.FormatInt(c.ID, 10): c.Version}})
	}
	if strings.HasPrefix(s.route, "custom:") {
		for _, node := range s.menu.Buttons {
			if node.ID == text(s.state["menu_parent"]) {
				if s.route == "custom:text" {
					return s.reply(html.EscapeString(node.Value), nil, Guard{})
				}
				if rich.URL(node.Value) {
					return s.reply(html.EscapeString(node.Label), [][]Button{{{Text: node.Label, URL: node.Value}}, {b("منوی اصلی", "menu")}}, Guard{})
				}
			}
		}
		return catalog.ErrInvalid
	}
	if s.route == "menu" {
		sort.SliceStable(s.menu.Buttons, func(i, j int) bool { return s.menu.Buttons[i].Sort < s.menu.Buttons[j].Sort })
		buttons := [][]Button{}
		row := []Button{}
		columns := s.menu.Columns
		if columns < 1 || columns > 3 {
			columns = 2
		}
		for _, node := range s.menu.Buttons {
			if node.Parent != nil || !s.nodeEnabled(node.ID) || (node.ID == "adminconsole" || strings.HasPrefix(node.Action, "ac:")) && !s.admin {
				continue
			}
			if node.Width == "full" {
				if len(row) > 0 {
					buttons = append(buttons, row)
					row = nil
				}
				buttons = append(buttons, []Button{{Text: node.Label, Style: node.Style}})
			} else {
				row = append(row, Button{Text: node.Label, Style: node.Style})
				if len(row) == columns {
					buttons = append(buttons, row)
					row = nil
				}
			}
		}
		if len(row) > 0 {
			buttons = append(buttons, row)
		}
		return s.menuReply("به همراه دانشگاه خوش آمدید.", buttons)
	}
	if strings.HasPrefix(s.route, "submenu:") {
		parent := strings.TrimPrefix(s.route, "submenu:")
		rows := [][]Button{}
		for _, node := range s.menu.Buttons {
			if node.Parent != nil && *node.Parent == parent && s.nodeEnabled(node.ID) && (!strings.HasPrefix(node.Action, "ac:") || s.admin) {
				rows = append(rows, []Button{b(node.Label, "button:"+node.ID)})
			}
		}
		return s.reply("یک گزینه را انتخاب کنید.", rows, Guard{})
	}
	if s.route == "rules" || s.route == "help" {
		var value string
		json.Unmarshal(s.settings[s.route], &value)
		if value == "" {
			value = "برای استفاده از امکانات، یک بخش را از منو انتخاب کنید."
		}
		return s.reply(html.EscapeString(value), nil, Guard{})
	}
	return s.reply("این گزینه هنوز به نسخهٔ جدید منتقل نشده است.", [][]Button{{b("منوی اصلی", "menu")}}, Guard{})
}
