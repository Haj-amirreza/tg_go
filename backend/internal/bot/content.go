package bot

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"html"
	"strconv"
	"strings"
)

//go:embed testdata/university-defaults.json
var universityDefaults []byte

func (s *session) genericContent() error {
	r := s.route
	var defaults struct {
		SystemOrder   []string          `json:"system_order"`
		CategoryOrder []string          `json:"category_order"`
		Systems       map[string]string `json:"systems"`
		Categories    map[string]string `json:"categories"`
	}
	json.Unmarshal(universityDefaults, &defaults)
	rows := [][]Button{}
	if r == "u:systems" {
		for _, key := range defaults.SystemOrder {
			label := defaults.Systems[key]
			rows = append(rows, []Button{b(label, "u:systems:"+key)})
		}
		return s.reply("سامانه‌ها", rows, Guard{})
	}
	if r == "u:links" {
		return s.reply("گروه‌ها و کانال‌ها", [][]Button{{b("گروه‌ها", "u:links:group"), b("کانال‌ها", "u:links:channel")}, {b("پیوندهای قدیمی", "u:legacylinks:0")}}, Guard{})
	}
	if r == "u:links:group" || r == "u:links:channel" {
		if r == "u:links:group" && !s.approved {
			return s.reply("برای دیدن گروه‌ها، تأیید دانشجویی لازم است.", [][]Button{{b("منوی اصلی", "menu")}}, Guard{})
		}
		for _, key := range defaults.CategoryOrder {
			label := defaults.Categories[key]
			rows = append(rows, []Button{b(label, r+":"+key)})
		}
		return s.reply("دستهٔ موردنظر را انتخاب کنید.", rows, Guard{})
	}
	filters := map[string]string{}
	page := 0
	kind := ""
	parts := strings.Split(r, ":")
	base := r
	title := "فهرست مطالب"
	if strings.HasPrefix(r, "list:") && len(parts) == 3 {
		kind = parts[1]
		if kind == "contacts" {
			kind = "people"
		}
		page, _ = strconv.Atoi(parts[2])
		base = "list:" + kind
		title = map[string]string{"people": "اطلاعات تماس", "contacts": "اطلاعات تماس", "formulas": "فرمول‌نامه", "handouts": "جزوه", "faq": "پرسش‌های متداول", "resources": "منابع کتابخانه"}[kind]
	}
	if strings.HasPrefix(r, "u:items:") && len(parts) >= 3 {
		kind = parts[2]
		if len(parts) > 3 {
			page, _ = strconv.Atoi(parts[3])
		}
		base = "u:items:" + kind
	}
	if r == "u:faq" {
		kind = "faq"
		base = "list:faq"
		title = "پرسش‌های متداول"
	}
	if strings.HasPrefix(r, "u:systems:") && len(parts) >= 3 {
		if _, ok := defaults.Systems[parts[2]]; !ok {
			return s.reply("سامانه معتبر نیست.", nil, Guard{})
		}
		kind = "systems"
		filters["system"] = parts[2]
		title = defaults.Systems[parts[2]]
		base = "u:systems:" + parts[2]
		if len(parts) > 3 {
			page, _ = strconv.Atoi(parts[3])
		}
	}
	if strings.HasPrefix(r, "u:links:") && len(parts) >= 4 {
		kind = "links"
		filters["link_type"] = parts[2]
		filters["link_category"] = parts[3]
		if parts[3] == "courses" {
			if len(parts) == 4 {
				return s.reply("درسی", [][]Button{{b("عمومی", r+":general:0"), b("دروس تخصصی", r+":specialized:0")}, {b("بازگشت", "u:links:"+parts[2])}}, Guard{})
			}
			if parts[4] != "general" && parts[4] != "specialized" {
				return catalog.ErrInvalid
			}
			filters["course_level"] = parts[4]
			if parts[4] == "specialized" {
				id := int64(0)
				if len(parts) > 5 {
					var e error
					id, e = strconv.ParseInt(parts[5], 10, 64)
					if e != nil || id < 0 {
						return catalog.ErrInvalid
					}
				}
				children, e := catalog.Topics(s.ctx, s.tx, id, false)
				if e != nil {
					return e
				}
				prefix := strings.Join(parts[:5], ":")
				if len(parts) < 7 && (id == 0 || len(children) > 0) {
					rows := [][]Button{}
					for _, t := range children {
						rows = append(rows, []Button{b(t.Name, fmt.Sprintf("%s:%d", prefix, t.ID))})
					}
					rows = append(rows, []Button{b("پیوندهای همین بخش", fmt.Sprintf("%s:%d:0", prefix, id))}, []Button{b("بازگشت", strings.Join(parts[:4], ":"))})
					return s.reply("دانشکده و رشته را انتخاب کنید:", rows, Guard{})
				}
				filters["topic"] = strconv.FormatInt(id, 10)
			}
		}
		base = strings.Join(parts[:4], ":")
		if parts[3] == "courses" {
			n := 5
			if parts[4] == "specialized" {
				n = 6
			}
			base = strings.Join(parts[:n], ":")
		}
		title = defaults.Categories[parts[3]]
		if parts[3] != "courses" {
			if len(parts) > 4 {
				page, _ = strconv.Atoi(parts[4])
			}
		} else if parts[4] == "general" {
			if len(parts) > 5 {
				page, _ = strconv.Atoi(parts[5])
			}
		} else if len(parts) > 6 {
			page, _ = strconv.Atoi(parts[6])
		}
		if parts[2] != "channel" && !s.approved {
			return s.reply("برای این بخش تأیید دانشجویی لازم است.", nil, Guard{})
		}
	}
	if strings.HasPrefix(r, "u:legacylinks") && !s.approved {
		return s.requireStudent()
	}
	if strings.HasPrefix(r, "u:legacylinks") {
		kind = "links"
		filters["link_type"] = "unclassified"
		title = "پیوندهای قدیمی"
		base = "u:legacylinks"
		if len(parts) > 2 {
			page, _ = strconv.Atoi(parts[2])
		}
	}
	if kind == "" {
		return s.reply("گزینه معتبر نیست.", nil, Guard{})
	}
	result, e := catalog.SearchFiltered(s.ctx, s.tx, kind, "", page, s.tags, s.approved, filters)
	if e != nil {
		return e
	}
	body := "<b>" + html.EscapeString(title) + "</b>"
	versions := map[string]int{}
	for i, c := range result.Rows {
		body += fmt.Sprintf("\n\n%d. <b>%s</b>", result.Page*10+i+1, html.EscapeString(c.Title))
		rows = append(rows, []Button{b(c.Title, fmt.Sprintf("view:%d", c.ID))})
		versions[strconv.FormatInt(c.ID, 10)] = c.Version
	}
	if len(result.Rows) == 0 {
		body += "\nهنوز محتوایی در این بخش منتشر نشده است."
	}
	nav := []Button{}
	if result.Page > 0 {
		nav = append(nav, b("صفحه قبل", fmt.Sprintf("%s:%d", base, result.Page-1)))
	}
	if result.Page+1 < result.Pages {
		nav = append(nav, b("صفحه بعد", fmt.Sprintf("%s:%d", base, result.Page+1)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []Button{b("منوی اصلی", "menu")})
	return s.reply(body, rows, Guard{Content: versions})
}
