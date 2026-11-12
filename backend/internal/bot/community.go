package bot

import (
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/community"
	"html"
	"strconv"
	"strings"
	"time"
)

func (s *session) community() error {
	r := s.route
	if r == "u:personal" || r == "personal:home" {
		if e := s.board([][]Button{{{Text: "معرفی کانال دانشجویان", Data: "fun:channels", Style: "success"}}, {b("برنامه روز", "u:personal:schedule"), b("غذای روز", "u:personal:meal")}, {b("کارهای روز", "u:personal:task"), {Text: "تفریح", Data: "fun:home", Style: "primary"}}, {b("بازارچه دانشجویی", "market"), b("گمشده‌ها", "lostfound")}, {b("منوی اصلی", "menu")}}); e != nil {
			return e
		}
		return s.reply("<b>شخصی؛ ابزارهای روز دانشجویی</b>\nتفریح برای همه باز است. برای ثبت کانال، آگهی و ابزارهای شخصی تأیید دانشجویی لازم است.", [][]Button{{{Text: "تفریح و مطالعه", Data: "fun:home", Style: "primary"}}, {b("احراز دانشجویی", "verify")}}, Guard{})
	}
	if !s.approved {
		return s.requireStudent()
	}
	parts := strings.Split(r, ":")
	if strings.HasPrefix(r, "u:personal:") {
		return s.personalItems()
	}
	kind := "channels"
	prefix := "sch"
	heading := "کانال‌های دانشجویان"
	if r == "market" || strings.HasPrefix(r, "market:") {
		kind, prefix, heading = "market", "market", "بازارچه دانشجویی"
	}
	if r == "lostfound" || strings.HasPrefix(r, "lost:") || strings.HasPrefix(r, "lostfound:") {
		kind, prefix, heading = "lost", "lost", "گمشده‌ها و پیداشده‌ها"
	}
	if len(parts) > 1 && parts[1] == "new" {
		if kind == "lost" {
			if len(parts) > 2 && parts[2] == "found" {
				kind = "found"
			}
		}
		return s.startForm(kind, nil)
	}
	if len(parts) == 4 && parts[1] == "edit" {
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		version, _ := strconv.Atoi(parts[3])
		a, e := community.Get(s.ctx, s.tx, kind, id, s.user, false)
		if e != nil {
			return e
		}
		if a.User == nil || *a.User != s.user || a.State == "deleted" {
			return catalog.ErrMissing
		}
		if a.Revision != version {
			return catalog.ErrConflict
		}
		values := map[string]any{"title": a.Title, "body": a.Body, "url": a.URL, "contact": a.Contact, "place": a.Place, "price": ""}
		if a.Price != nil {
			values["price"] = strconv.FormatInt(*a.Price, 10)
		}
		return s.startForm(a.Kind, map[string]any{"id": a.ID, "revision": a.Revision, "values": values})
	}
	if len(parts) >= 4 && parts[1] == "remove" {
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		revision, _ := strconv.Atoi(parts[3])
		if e := community.Remove(s.ctx, s.tx, kind, id, s.user, revision, false); e != nil {
			return e
		}
		return s.reply("موردت حذف شد.", [][]Button{{b("بازگشت", prefix+":mine:0")}}, Guard{Approved: true})
	}
	if len(parts) >= 3 && parts[1] == "view" {
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		a, e := community.Get(s.ctx, s.tx, kind, id, s.user, false)
		if e != nil {
			return e
		}
		body := "<b>" + html.EscapeString(a.Title) + "</b>\n\n" + html.EscapeString(a.Body)
		if a.Contact != "" {
			body += "\n\nراه تماس: " + html.EscapeString(a.Contact)
		}
		if a.Place != "" {
			body += "\nمحل: " + html.EscapeString(a.Place)
		}
		if a.Price != nil {
			body += fmt.Sprintf("\nقیمت: %d تومان", *a.Price)
		}
		rows := [][]Button{}
		if a.User != nil && *a.User == s.user {
			rows = append(rows, []Button{b("ویرایش آگهی من", fmt.Sprintf("%s:edit:%d:%d", prefix, a.ID, a.Revision))})
			rows = append(rows, []Button{{Text: "حذف آگهی من", Data: fmt.Sprintf("%s:remove:%d:%d", prefix, a.ID, a.Revision), Style: "danger"}})
		}
		if kind == "market" {
			rows = append(rows, []Button{b("گزارش این آگهی", fmt.Sprintf("market:report:%d", a.ID))})
		}
		rows = append(rows, []Button{b("بازگشت", prefix+":page:0")})
		return s.reply(body, rows, Guard{Approved: true, Community: map[string]map[string]int{kind: {strconv.FormatInt(a.ID, 10): a.Revision}}})
	}
	if len(parts) == 3 && parts[1] == "report" && kind == "market" {
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		if e := community.Report(s.ctx, s.tx, s.user, id, "گزارش دانشجو از داخل ربات"); e != nil {
			return e
		}
		return s.reply("گزارش برای بررسی مدیر ثبت شد.", [][]Button{{b("بازگشت", "market")}}, Guard{Approved: true})
	}
	own := len(parts) > 1 && parts[1] == "mine"
	page := 0
	if len(parts) > 2 {
		page, _ = strconv.Atoi(parts[2])
	}
	items, e := community.List(s.ctx, s.tx, kind, s.user, own, false, "", page)
	if e != nil {
		return e
	}
	more := len(items) > 10
	if more {
		items = items[:10]
	}
	body := fmt.Sprintf("<b>%s · صفحهٔ %d</b>", heading, page+1)
	rows := [][]Button{}
	versions := map[string]int{}
	for i, a := range items {
		id := strconv.FormatInt(a.ID, 10)
		versions[id] = a.Revision
		label := html.EscapeString(a.Title)
		if kind == "channels" {
			label = `<a href="` + html.EscapeString(a.URL) + `">` + label + `</a>`
			rows = append(rows, []Button{{Text: a.Title, URL: a.URL, Style: "success"}})
		} else {
			rows = append(rows, []Button{b(a.Title, prefix+":view:"+id)})
		}
		body += fmt.Sprintf("\n\n%d. <b>%s</b>", page*10+i+1, label)
		if own {
			body += " · " + html.EscapeString(a.State)
			if kind == "channels" {
				rows = append(rows, []Button{b("ویرایش کانال من: "+a.Title, fmt.Sprintf("sch:edit:%d:%d", a.ID, a.Revision))})
				rows = append(rows, []Button{{Text: "حذف کانال من: " + a.Title, Data: fmt.Sprintf("sch:remove:%d:%d", a.ID, a.Revision), Style: "danger"}})
			}
		}
	}
	if len(items) == 0 {
		body += "\n\nهنوز موردی در این صفحه ثبت نشده است."
	}
	nav := []Button{}
	if page > 0 {
		nav = append(nav, b("→ قبلی", fmt.Sprintf("%s:%s:%d", prefix, map[bool]string{true: "mine", false: "page"}[own], page-1)))
	}
	nav = append(nav, b(strconv.Itoa(page+1), r))
	if more {
		nav = append(nav, b("بعدی ←", fmt.Sprintf("%s:%s:%d", prefix, map[bool]string{true: "mine", false: "page"}[own], page+1)))
	}
	rows = append(rows, nav, []Button{{Text: map[string]string{"channels": "معرفی کانال من", "market": "ثبت آگهی", "lost": "ثبت گمشده"}[kind], Data: prefix + ":new", Style: "success"}, b("موارد من", prefix+":mine:0")})
	if kind == "lost" {
		rows = append(rows, []Button{b("ثبت پیداشده", "lost:new:found")})
	}
	rows = append(rows, []Button{b("بازگشت به شخصی", "u:personal"), b("منوی اصلی", "menu")})
	guard := Guard{Approved: true}
	if len(versions) > 0 {
		guard.Community = map[string]map[string]int{kind: versions}
	}
	if s.update.Callback != nil {
		return s.emit("editMessageText", map[string]any{"chat_id": s.chat, "message_id": s.message, "text": body, "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}, "reply_markup": Markup{Inline: rows}}, guard, 10)
	}
	return s.reply(body, rows, guard)
}
func (s *session) personalItems() error {
	p := strings.Split(s.route, ":")
	kind := p[2]
	if kind != "schedule" && kind != "meal" && kind != "task" {
		return s.reply("نوع ابزار شخصی معتبر نیست.", nil, Guard{Approved: true})
	}
	if len(p) > 3 && p[3] == "new" {
		return s.startForm("personal", map[string]any{"personalKind": kind})
	}
	if len(p) > 5 && p[3] == "edit" {
		id, _ := strconv.ParseInt(p[4], 10, 64)
		version, _ := strconv.Atoi(p[5])
		var title, body, day, clock string
		var done bool
		var revision int
		e := s.tx.QueryRow(s.ctx, `SELECT title,body,day::text,clock,done,revision FROM personal_items WHERE id=$1 AND user_id=$2`, id, s.user).Scan(&title, &body, &day, &clock, &done, &revision)
		if e != nil {
			return catalog.ErrMissing
		}
		if revision != version {
			return catalog.ErrConflict
		}
		return s.startForm("personal", map[string]any{"personalKind": kind, "id": id, "revision": revision, "done": done, "values": map[string]any{"title": title, "body": body, "day": day, "clock": clock}})
	}
	if len(p) > 5 && (p[3] == "done" || p[3] == "remove") {
		id, _ := strconv.ParseInt(p[4], 10, 64)
		version, _ := strconv.Atoi(p[5])
		query := `DELETE FROM personal_items WHERE id=$1 AND user_id=$2 AND revision=$3`
		if p[3] == "done" {
			query = `UPDATE personal_items SET done=NOT done,revision=revision+1,updated_at=extract(epoch FROM now())::bigint WHERE id=$1 AND user_id=$2 AND revision=$3`
		}
		tag, e := s.tx.Exec(s.ctx, query, id, s.user, version)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return s.reply("این مورد تغییر کرده؛ دوباره فهرست را باز کن.", nil, Guard{Approved: true})
		}
	}
	zone, _ := time.LoadLocation("Asia/Tehran")
	day := time.Now().In(zone).Format("2006-01-02")
	page := 0
	if len(p) > 4 && p[3] == "page" {
		page, _ = strconv.Atoi(p[4])
	}
	items, e := community.PersonalList(s.ctx, s.tx, s.user, day, kind, page)
	if e != nil {
		return e
	}
	more := len(items) > 10
	if more {
		items = items[:10]
	}
	body := "<b>" + map[string]string{"schedule": "برنامه روز", "meal": "غذای روز", "task": "کارهای روز"}[kind] + "</b>\n" + day
	rows := [][]Button{}
	versions := map[string]int{}
	for _, item := range items {
		mark := "☐"
		if item.Done {
			mark = "☑"
		}
		body += "\n\n" + mark + " " + html.EscapeString(item.Clock+" "+item.Title) + "\n" + html.EscapeString(item.Body)
		versions[strconv.FormatInt(item.ID, 10)] = item.Revision
		rows = append(rows, []Button{b("ویرایش: "+item.Title, fmt.Sprintf("u:personal:%s:edit:%d:%d", kind, item.ID, item.Revision))})
		rows = append(rows, []Button{b(mark+" "+item.Title, fmt.Sprintf("u:personal:%s:done:%d:%d", kind, item.ID, item.Revision)), {Text: "حذف", Data: fmt.Sprintf("u:personal:%s:remove:%d:%d", kind, item.ID, item.Revision), Style: "danger"}})
	}
	if len(items) == 0 {
		body += "\n\nبرای امروز هنوز موردی ثبت نشده."
	}
	nav := []Button{}
	if page > 0 {
		nav = append(nav, b("→ قبلی", fmt.Sprintf("u:personal:%s:page:%d", kind, page-1)))
	}
	if more {
		nav = append(nav, b("بعدی ←", fmt.Sprintf("u:personal:%s:page:%d", kind, page+1)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []Button{{Text: "افزودن مورد", Data: "u:personal:" + kind + ":new", Style: "success"}}, []Button{b("بازگشت", "u:personal")})
	return s.reply(body, rows, Guard{Approved: true, Personal: versions})
}
