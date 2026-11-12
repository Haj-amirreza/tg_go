package bot

import (
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/announcements"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/community"
	"github.com/Haj-amirreza/tg_go/backend/internal/support"
	"html"
	"strconv"
	"strings"
)

func (s *session) can(permission string) bool {
	if !s.admin {
		return false
	}
	if s.super {
		return true
	}
	for _, p := range s.permissions {
		if p == permission {
			return true
		}
	}
	return false
}
func (s *session) adminConsole() error {
	if !s.admin {
		return s.reply("این بخش ویژهٔ مدیران ثبت‌شده است.", [][]Button{{b("منوی اصلی", "menu")}}, Guard{Module: "feedback"})
	}
	p := strings.Split(s.route, ":")
	guard := Guard{Actor: s.actorID}
	if s.route == "ac:home" {
		rows := [][]Button{}
		if s.can("content") {
			rows = append(rows, []Button{{Text: "افزودن اطلاعیه", Data: "ac:notice:new", Style: "success"}}, []Button{b("بررسی اساتید و شماره‌ها", "ac:contents:professors:0"), b("بررسی لینک گروه‌ها", "ac:contents:links:0")}, []Button{b("محتوا و راهنماها", "ac:contents:guides:0"), b("کانال دانشجویان", "ac:ads:channels:0")})
		}
		if s.can("tickets") {
			rows = append(rows, []Button{b("پرسش‌ها و گفتگوها", "ac:tickets:0")})
		}
		if s.can("market") {
			rows = append(rows, []Button{b("بازارچه", "ac:ads:market:0"), b("گمشده‌ها", "ac:ads:lost:0")})
		}
		if s.can("users") {
			rows = append(rows, []Button{b("تأیید دانشجو", "ac:verifications:0")})
		}
		rows = append(rows, []Button{b("بازگشت به ربات", "menu")})
		if e := s.board(rows); e != nil {
			return e
		}
		webRows := [][]Button{}
		if s.engine.AdminURL != "" {
			webRows = append(webRows, []Button{{Text: "پنل وب و استودیوی کامل پیام", URL: s.engine.AdminURL, Style: "primary"}})
		}
		return s.reply("<b>پنل مدیریت ربات</b>\nفقط بخش‌های مجاز برای حساب شما نمایش داده می‌شوند. یک بخش را انتخاب کن.", webRows, guard)
	}
	if len(p) == 3 && p[1] == "notice" && p[2] == "new" {
		if !s.can("content") || !s.can("publish") {
			return s.adminDenied()
		}
		return s.startForm("notice", nil)
	}
	if len(p) >= 3 && p[1] == "tickets" {
		if !s.can("tickets") {
			return s.adminDenied()
		}
		page, _ := strconv.Atoi(p[2])
		items, e := support.List(s.ctx, s.tx, 0, true, "", page)
		if e != nil {
			return e
		}
		rows := [][]Button{}
		for i, t := range items {
			if i == 10 {
				break
			}
			rows = append(rows, []Button{b(fmt.Sprintf("#%d %s · %s", t.ID, t.Subject, t.State), fmt.Sprintf("ac:ticket:%d", t.ID))})
		}
		if page > 0 {
			rows = append(rows, []Button{b("→ قبلی", fmt.Sprintf("ac:tickets:%d", page-1))})
		}
		if len(items) > 10 {
			rows = append(rows, []Button{b("بعدی ←", fmt.Sprintf("ac:tickets:%d", page+1))})
		}
		rows = append(rows, []Button{b("پنل مدیر", "ac:home")})
		guard.Permission = "tickets"
		return s.reply("<b>پرونده‌های پشتیبانی</b>", rows, guard)
	}
	if len(p) >= 3 && p[1] == "ticket" {
		if !s.can("tickets") {
			return s.adminDenied()
		}
		id, _ := strconv.ParseInt(p[2], 10, 64)
		page := -1
		if len(p) > 3 {
			page, _ = strconv.Atoi(p[3])
		}
		thread, e := support.Get(s.ctx, s.tx, id, 0, true, page)
		if e != nil {
			return e
		}
		body := fmt.Sprintf("<b>#%d · %s</b>\nوضعیت: %s · نسخه: %d", id, html.EscapeString(thread.Ticket.Subject), thread.Ticket.State, thread.Ticket.Revision)
		for _, m := range thread.Messages {
			label := "دانشجو"
			if m.Admin != nil {
				label = "مدیر"
			}
			if m.Internal {
				label += " · یادداشت داخلی"
			}
			body += "\n\n<b>" + label + "</b>\n" + html.EscapeString(m.Body)
		}
		rows := [][]Button{{{Text: "پاسخ به دانشجو", Data: fmt.Sprintf("ac:reply:%d:%d:public", id, thread.Ticket.Revision), Style: "success"}, {Text: "یادداشت داخلی", Data: fmt.Sprintf("ac:reply:%d:%d:internal", id, thread.Ticket.Revision), Style: "primary"}}}
		nav := []Button{}
		if thread.Page > 0 {
			nav = append(nav, b("→ قبل", fmt.Sprintf("ac:ticket:%d:%d", id, thread.Page-1)))
		}
		nav = append(nav, b(fmt.Sprintf("%d / %d", thread.Page+1, thread.Pages), s.route))
		if thread.Page+1 < thread.Pages {
			nav = append(nav, b("بعد ←", fmt.Sprintf("ac:ticket:%d:%d", id, thread.Page+1)))
		}
		rows = append(rows, nav, []Button{b("فهرست گفتگوها", "ac:tickets:0"), b("پنل مدیر", "ac:home")})
		guard.Permission = "tickets"
		return s.reply(body, rows, guard)
	}
	if len(p) == 5 && p[1] == "reply" {
		if !s.can("tickets") {
			return s.adminDenied()
		}
		id, _ := strconv.ParseInt(p[2], 10, 64)
		rev, _ := strconv.Atoi(p[3])
		return s.startForm("admin_reply", map[string]any{"ticket": id, "revision": rev, "internal": p[4] == "internal"})
	}
	if len(p) >= 4 && p[1] == "contents" {
		if !s.can("content") {
			return s.adminDenied()
		}
		kind := p[2]
		if kind != "professors" && kind != "contacts" && kind != "links" && kind != "guides" {
			return catalog.ErrInvalid
		}
		page, _ := strconv.Atoi(p[3])
		if page < 0 || page > 10000 {
			return catalog.ErrInvalid
		}
		rows, e := s.tx.Query(s.ctx, `SELECT id,title,status,version FROM contents WHERE kind=$1 ORDER BY CASE WHEN status='pending' THEN 0 ELSE 1 END,id DESC LIMIT 11 OFFSET $2`, kind, page*10)
		if e != nil {
			return e
		}
		buttons := [][]Button{}
		n := 0
		for rows.Next() {
			var id int64
			var title, state string
			var version int
			if e = rows.Scan(&id, &title, &state, &version); e != nil {
				rows.Close()
				return e
			}
			n++
			if n <= 10 {
				buttons = append(buttons, []Button{b(title+" · "+state, fmt.Sprintf("ac:content:%d", id))})
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if page > 0 {
			buttons = append(buttons, []Button{b("→ قبلی", fmt.Sprintf("ac:contents:%s:%d", kind, page-1))})
		}
		if n > 10 {
			buttons = append(buttons, []Button{b("بعدی ←", fmt.Sprintf("ac:contents:%s:%d", kind, page+1))})
		}
		buttons = append(buttons, []Button{b("پنل مدیر", "ac:home")})
		guard.Permission = "content"
		return s.reply("<b>مدیریت محتوا و بررسی اطلاعات</b>", buttons, guard)
	}
	if len(p) == 3 && p[1] == "content" {
		if !s.can("content") {
			return s.adminDenied()
		}
		id, _ := strconv.ParseInt(p[2], 10, 64)
		c, e := catalog.Get(s.ctx, s.tx, id)
		if e != nil {
			return e
		}
		body := "<b>" + html.EscapeString(c.Title) + "</b>\n\n" + html.EscapeString(c.Body)
		for _, key := range []string{"office_phone", "personal_phone", "foreign_id", "email"} {
			if v := catalog.MetaText(c.Metadata, key); v != "" {
				body += "\n" + html.EscapeString(v)
			}
		}
		rows := [][]Button{}
		if s.can("publish") {
			rows = append(rows, []Button{{Text: "تأیید و انتشار", Data: fmt.Sprintf("ac:review:%d:%d:published", id, c.Version), Style: "success"}, {Text: "رد / بایگانی", Data: fmt.Sprintf("ac:review:%d:%d:archived", id, c.Version), Style: "danger"}})
		}
		rows = append(rows, []Button{b("پنل مدیر", "ac:home")})
		guard.Permission = "content"
		return s.reply(body, rows, guard)
	}
	if len(p) == 5 && p[1] == "review" {
		if !s.can("content") || !s.can("publish") {
			return s.adminDenied()
		}
		id, _ := strconv.ParseInt(p[2], 10, 64)
		version, _ := strconv.Atoi(p[3])
		c, e := catalog.Get(s.ctx, s.tx, id)
		if e != nil {
			return e
		}
		if c.Version != version {
			return catalog.ErrConflict
		}
		if p[4] != "published" && p[4] != "archived" {
			return catalog.ErrInvalid
		}
		c.Status = p[4]
		if _, e = catalog.Save(s.ctx, s.tx, c, s.actorID, true); e != nil {
			return e
		}
		return s.reply("وضعیت بررسی ذخیره شد.", [][]Button{{b("پنل مدیر", "ac:home")}}, guard)
	}
	if len(p) >= 4 && p[1] == "ads" {
		kind := p[2]
		permission := "market"
		if kind == "channels" {
			permission = "content"
		}
		if !s.can(permission) {
			return s.adminDenied()
		}
		page, _ := strconv.Atoi(p[3])
		items, e := community.List(s.ctx, s.tx, kind, 0, false, true, "pending", page)
		if e != nil {
			return e
		}
		rows := [][]Button{}
		for i, a := range items {
			if i == 10 {
				break
			}
			rows = append(rows, []Button{b(a.Title, fmt.Sprintf("ac:ad:%s:%d", kind, a.ID))})
		}
		if page > 0 {
			rows = append(rows, []Button{b("→ قبلی", fmt.Sprintf("ac:ads:%s:%d", kind, page-1))})
		}
		if len(items) > 10 {
			rows = append(rows, []Button{b("بعدی ←", fmt.Sprintf("ac:ads:%s:%d", kind, page+1))})
		}
		rows = append(rows, []Button{b("پنل مدیر", "ac:home")})
		guard.Permission = permission
		return s.reply("<b>موارد در انتظار بررسی</b>", rows, guard)
	}
	if len(p) == 4 && p[1] == "ad" {
		kind := p[2]
		permission := "market"
		if kind == "channels" {
			permission = "content"
		}
		if !s.can(permission) {
			return s.adminDenied()
		}
		id, _ := strconv.ParseInt(p[3], 10, 64)
		a, e := community.Get(s.ctx, s.tx, kind, id, 0, true)
		if e != nil {
			return e
		}
		body := "<b>" + html.EscapeString(a.Title) + "</b>\n\n" + html.EscapeString(a.Body+"\n"+a.URL+"\n"+a.Contact+"\n"+a.Place)
		state := "published"
		if kind == "channels" {
			state = "approved"
		}
		rows := [][]Button{{{Text: "تأیید", Data: fmt.Sprintf("ac:adreview:%s:%d:%d:%s", kind, id, a.Revision, state), Style: "success"}, {Text: "رد", Data: fmt.Sprintf("ac:adreview:%s:%d:%d:rejected", kind, id, a.Revision), Style: "danger"}}, {b("پنل مدیر", "ac:home")}}
		guard.Permission = permission
		return s.reply(body, rows, guard)
	}
	if len(p) == 6 && p[1] == "adreview" {
		kind := p[2]
		permission := "market"
		if kind == "channels" {
			permission = "content"
		}
		if !s.can(permission) {
			return s.adminDenied()
		}
		id, _ := strconv.ParseInt(p[3], 10, 64)
		version, _ := strconv.Atoi(p[4])
		a, e := community.Get(s.ctx, s.tx, kind, id, 0, true)
		if e != nil {
			return e
		}
		if a.Revision != version {
			return catalog.ErrConflict
		}
		a.State = p[5]
		if _, e = community.Save(s.ctx, s.tx, a, s.user, true); e != nil {
			return e
		}
		return s.reply("نتیجهٔ بررسی ذخیره شد.", [][]Button{{b("پنل مدیر", "ac:home")}}, guard)
	}
	if len(p) >= 2 && p[1] == "verifications" {
		return s.adminVerifications(p)
	}
	return s.reply("این ابزار از پنل وب قابل مدیریت است؛ از منوی مدیر یک بخش را انتخاب کن.", [][]Button{{b("پنل مدیر", "ac:home")}}, guard)
}
func (s *session) adminDenied() error {
	return s.reply("دسترسی این عملیات برای حساب شما فعال نیست.", [][]Button{{b("پنل مدیر", "ac:home")}}, Guard{Module: "feedback"})
}
func (s *session) commitNotice(f, values map[string]any) error {
	if !s.can("content") || !s.can("publish") {
		return s.adminDenied()
	}
	c, e := catalog.Save(s.ctx, s.tx, catalog.Content{Kind: "announcements", Title: text(values["title"]), Body: text(values["body"]), Metadata: map[string]any{"access": "public", "audience_tag": ""}, Status: "published"}, s.actorID, true)
	if e != nil {
		return e
	}
	if e = announcements.Assign(s.ctx, s.tx, c.ID, 0, "bank"); e != nil {
		return e
	}
	return s.reply("اطلاعیه در بانک منتشر شد. دسته و ارسال سراسری را از استودیوی پیام تنظیم کن.", [][]Button{{b("نمایش اطلاعیه", fmt.Sprintf("notice:view:%d", c.ID)), b("پنل مدیر", "ac:home")}}, Guard{Actor: s.actorID, Permission: "content"})
}
func (s *session) commitAdminReply(f, values map[string]any) error {
	if !s.can("tickets") {
		return s.adminDenied()
	}
	internal, _ := f["internal"].(bool)
	thread, e := support.Get(s.ctx, s.tx, number(f["ticket"]), 0, true, -1)
	if e != nil {
		return e
	}
	state := "answered"
	if internal {
		state = thread.Ticket.State
	}
	event, e := support.AdminReply(s.ctx, s.tx, s.actorID, support.Reply{ID: number(f["ticket"]), Revision: int(number(f["revision"])), Body: text(values["body"]), Internal: internal, State: state, Priority: thread.Ticket.Priority, Assigned: thread.Ticket.Assigned})
	if e != nil {
		return e
	}
	if e = QueueTicketReply(s.ctx, s.tx, event); e != nil {
		return e
	}
	body := "پاسخ در صف ارسال به دانشجو قرار گرفت."
	if internal {
		body = "یادداشت داخلی ذخیره شد؛ برای دانشجو ارسال نمی‌شود."
	}
	return s.reply(body, [][]Button{{b("بازگشت به گفتگو", fmt.Sprintf("ac:ticket:%d", event.Ticket)), b("پنل مدیر", "ac:home")}}, Guard{Actor: s.actorID, Permission: "tickets"})
}
func (s *session) adminVerifications(p []string) error {
	if !s.can("users") {
		return s.adminDenied()
	}
	if len(p) == 6 && p[2] == "review" {
		user, _ := strconv.ParseInt(p[3], 10, 64)
		revision, _ := strconv.Atoi(p[4])
		if e := community.ReviewVerification(s.ctx, s.tx, s.actorID, community.Review{User: user, Revision: revision, Status: p[5], Checked: true}); e != nil {
			return e
		}
		return s.reply("نتیجهٔ بررسی دانشجویی ذخیره شد.", [][]Button{{b("فهرست درخواست‌ها", "ac:verifications:0")}}, Guard{Actor: s.actorID, Permission: "users"})
	}
	page := 0
	if len(p) > 2 {
		page, _ = strconv.Atoi(p[2])
	}
	items, e := community.Verifications(s.ctx, s.tx, s.engine.AppKey, page)
	if e != nil {
		return e
	}
	body := "<b>بررسی دانشجویی</b>\nپیش از تأیید، هویت دانشجویی را از مرجع دانشگاه بررسی کنید."
	rows := [][]Button{}
	for i, v := range items {
		if i == 10 {
			break
		}
		body += fmt.Sprintf("\n\n#%d · %s · %s\n%s\n%s", v.User, html.EscapeString(v.Status), html.EscapeString(v.Details["full_name"]), html.EscapeString(v.Details["student_number"]), html.EscapeString(v.Details["phone"]))
		if v.Status == "pending" {
			rows = append(rows, []Button{{Text: fmt.Sprintf("تأیید پس از بررسی #%d", v.User), Data: fmt.Sprintf("ac:verifications:review:%d:%d:approved", v.User, v.Revision), Style: "success"}, {Text: fmt.Sprintf("رد #%d", v.User), Data: fmt.Sprintf("ac:verifications:review:%d:%d:rejected", v.User, v.Revision), Style: "danger"}})
		} else if v.Status == "approved" {
			rows = append(rows, []Button{{Text: fmt.Sprintf("لغو تأیید #%d", v.User), Data: fmt.Sprintf("ac:verifications:review:%d:%d:revoked", v.User, v.Revision), Style: "danger"}})
		}
	}
	if page > 0 {
		rows = append(rows, []Button{b("→ قبلی", fmt.Sprintf("ac:verifications:%d", page-1))})
	}
	if len(items) > 10 {
		rows = append(rows, []Button{b("بعدی ←", fmt.Sprintf("ac:verifications:%d", page+1))})
	}
	rows = append(rows, []Button{b("پنل مدیر", "ac:home")})
	return s.reply(body, rows, Guard{Actor: s.actorID, Permission: "users"})
}
