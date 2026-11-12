package bot

import (
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"html"
	"strconv"
	"strings"
)

func (s *session) topicNavigation() error {
	p := strings.Split(s.route, ":")
	if len(p) < 3 {
		return catalog.ErrInvalid
	}
	if p[1] == "roles" {
		topic, e := strconv.ParseInt(p[2], 10, 64)
		if e != nil || topic < 0 {
			return catalog.ErrInvalid
		}
		return s.reply("نوع اطلاعات تماس را انتخاب کنید:", [][]Button{{b("اساتید", fmt.Sprintf("u:persons:contacts:%d:professor:0", topic))}, {b("کارکنان", fmt.Sprintf("u:persons:contacts:%d:staff:0", topic))}, {b("اساتید حق‌التدریس", fmt.Sprintf("u:persons:contacts:%d:adjunct:0", topic))}, {b("همهٔ راه‌های تماس", fmt.Sprintf("u:persons:contacts:%d:all:0", topic))}, {b("بازگشت", fmt.Sprintf("u:people:contacts:%d", topic))}}, Guard{})
	}
	kind := p[2]
	if kind != "contacts" && kind != "professors" {
		return catalog.ErrInvalid
	}
	if p[1] == "people" {
		if len(p) != 4 {
			return catalog.ErrInvalid
		}
		id, e := strconv.ParseInt(p[3], 10, 64)
		if e != nil || id < 0 {
			return catalog.ErrInvalid
		}
		var parent int64
		name := "دانشکده یا بخش را انتخاب کنید:"
		leaf := false
		if id > 0 {
			var topicKind string
			e = s.tx.QueryRow(s.ctx, `SELECT name,COALESCE(parent_id,0),kind FROM taxonomy WHERE id=$1`, id).Scan(&name, &parent, &topicKind)
			if e != nil {
				return catalog.ErrMissing
			}
			leaf = topicKind == "major"
		}
		children, e := catalog.Topics(s.ctx, s.tx, id, kind == "contacts")
		if e != nil {
			return e
		}
		target := fmt.Sprintf("u:roles:%d", id)
		if kind == "professors" {
			target = fmt.Sprintf("u:persons:professors:%d:all:0", id)
		}
		if len(children) == 0 || leaf {
			if id == 0 && kind == "professors" {
				return s.professorList("", 0, "all")
			}
			if id == 0 && kind == "contacts" {
				return s.reply("شماره‌ها و اساتید", [][]Button{{b("استادشناسی", "button:professors")}, {b("اطلاعات تماس", "list:contacts:0")}, {b("منوی اصلی", "menu")}}, Guard{})
			}
			s.route = target
			return s.topicNavigation()
		}
		rows := [][]Button{{{Text: "جست‌وجوی همهٔ اساتید", Data: "u:professor:search", Style: "primary"}, {Text: "معرفی استاد", Data: "u:professor:new", Style: "success"}}, {b("فهرست همین بخش", target)}}
		rows = append(rows, []Button{{Text: "فهرست همهٔ اساتید", Data: "u:professor:all:0", Style: "danger"}})
		for _, t := range children {
			rows = append(rows, []Button{b(t.Name, fmt.Sprintf("u:people:%s:%d", kind, t.ID))})
		}
		rows = append(rows, []Button{b("بازگشت", fmt.Sprintf("u:people:%s:%d", kind, parent)), b("منوی اصلی", "menu")})
		return s.reply(html.EscapeString(name), rows, Guard{})
	}
	if p[1] != "persons" || len(p) != 6 {
		return catalog.ErrInvalid
	}
	id, e := strconv.ParseInt(p[3], 10, 64)
	if e != nil || id < 0 {
		return catalog.ErrInvalid
	}
	page, e := strconv.Atoi(p[5])
	if e != nil {
		return catalog.ErrInvalid
	}
	filters := map[string]string{"topic": p[3]}
	if p[4] != "all" {
		if p[4] != "professor" && p[4] != "staff" && p[4] != "adjunct" {
			return catalog.ErrInvalid
		}
		filters["person_role"] = p[4]
	}
	searchKind := kind
	if kind == "contacts" {
		searchKind = "people"
	}
	result, e := catalog.SearchFiltered(s.ctx, s.tx, searchKind, "", page, s.tags, s.approved, filters)
	if e != nil {
		return e
	}
	base := strings.Join(p[:5], ":")
	return s.contentPage("اطلاعات تماس و استادشناسی", result, base, fmt.Sprintf("u:people:%s:%d", kind, id))
}
func (s *session) contentPage(title string, result catalog.Page[catalog.Content], base, back string) error {
	body := "<b>" + html.EscapeString(title) + "</b>"
	rows := [][]Button{}
	versions := map[string]int{}
	for i, c := range result.Rows {
		body += fmt.Sprintf("\n\n%d. <b>%s</b>", result.Page*10+i+1, html.EscapeString(c.Title))
		rows = append(rows, []Button{b(c.Title, fmt.Sprintf("view:%d", c.ID))})
		versions[strconv.FormatInt(c.ID, 10)] = c.Version
	}
	if len(result.Rows) == 0 {
		body += "\n\nهنوز محتوایی در این بخش منتشر نشده است."
	}
	nav := []Button{}
	if result.Page > 0 {
		nav = append(nav, b("→ قبلی", fmt.Sprintf("%s:%d", base, result.Page-1)))
	}
	nav = append(nav, b(fmt.Sprintf("%d / %d", result.Page+1, result.Pages), fmt.Sprintf("%s:%d", base, result.Page)))
	if result.Page+1 < result.Pages {
		nav = append(nav, b("بعدی ←", fmt.Sprintf("%s:%d", base, result.Page+1)))
	}
	rows = append(rows, nav, []Button{b("بازگشت", back), b("منوی اصلی", "menu")})
	return s.reply(body, rows, Guard{Content: versions})
}
