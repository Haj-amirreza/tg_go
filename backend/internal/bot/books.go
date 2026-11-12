package bot

import (
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *session) books() error {
	r := s.route
	if r == "u:library" {
		delete(s.state, "professor_form")
		delete(s.state, "book_search")
		return s.reply("کتابخانه", [][]Button{{b("کتابخانه دانشگاه", "ubooks:home")}, {b("فرمول‌نامه", "list:formulas:0")}, {b("کتابخانه و منابع آموزشی", "list:resources:0")}, {b("جزوه", "list:handouts:0")}, {b("منوی اصلی", "menu")}}, Guard{})
	}
	base := [][]Button{{b("دسته‌بندی کتاب‌ها", "ubooks:categories"), b("جست‌وجوی کتاب", "ubooks:search")}, {b("نمایش همه کتاب‌ها", "ubooks:home")}, {b("بازگشت", "u:library"), b("منوی اصلی", "menu")}}
	if r == "ubooks:categories" {
		rows := [][]Button{}
		for _, c := range catalog.Categories {
			rows = append(rows, []Button{b(c.Name, "ubooks:category:"+c.ID)})
		}
		rows = append(rows, []Button{b("همهٔ کتاب‌ها", "ubooks:home"), b("بازگشت", "u:library")})
		return s.reply("<b>دسته‌بندی کتابخانه دانشگاه</b>", rows, Guard{})
	}
	if r == "ubooks:search" {
		s.state["book_search"] = true
		return s.reply("نام کتاب، نویسنده، ناشر، موضوع یا شمارهٔ شناسایی را بفرستید.", base, Guard{})
	}
	if strings.HasPrefix(r, "ubooks:view:") {
		id, _ := strconv.ParseInt(strings.TrimPrefix(r, "ubooks:view:"), 10, 64)
		book, e := catalog.GetBook(s.ctx, s.tx, id)
		if e != nil {
			return s.reply("کتاب پیدا نشد.", base, Guard{})
		}
		body := "<b>" + html.EscapeString(book.Title) + "</b>"
		keys := []string{}
		for key := range book.Details {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key == "عنوان" || book.Details[key] == "" {
				continue
			}
			body += "\n\n<b>" + html.EscapeString(key) + ":</b> " + html.EscapeString(fmt.Sprint(book.Details[key]))
		}
		return s.reply(body, base, Guard{Books: []int64{id}})
	}
	if err := s.board(base); err != nil {
		return err
	}
	now := time.Now().Unix()
	queries := object(s.state["book_queries"])
	qtoken := ""
	query := ""
	category := text(s.state["book_category"])
	page := 0
	editing := false
	if strings.HasPrefix(r, "ubooks:page:") {
		parts := strings.Split(r, ":")
		if len(parts) != 4 {
			return s.reply("صفحه معتبر نیست.", base, Guard{})
		}
		qtoken = parts[2]
		snapshot := object(queries[qtoken])
		if number(snapshot["expires"]) < now {
			return s.reply("این جست‌وجو منقضی شده؛ دوباره جست‌وجو کنید.", base, Guard{})
		}
		query = text(snapshot["query"])
		category = text(snapshot["category"])
		var e error
		page, e = strconv.Atoi(parts[3])
		if e != nil || page < 0 || page > 10000 {
			return s.reply("صفحه معتبر نیست.", base, Guard{})
		}
		editing = s.update.Callback != nil
	} else {
		if r == "ubooks:home" {
			category = ""
		} else if strings.HasPrefix(r, "ubooks:category:") {
			category = strings.TrimPrefix(r, "ubooks:category:")
			if !catalog.CategoryOK(category) {
				return s.reply("دسته معتبر نیست.", base, Guard{})
			}
		} else if s.typed {
			query = strings.TrimSpace(r)
			if len([]rune(query)) > 160 {
				return s.reply("نام کوتاه‌تری برای جست‌وجو بفرستید.", base, Guard{})
			}
		} else {
			return s.reply("گزینه معتبر نیست.", base, Guard{})
		}
		qtoken = token()
		for k, v := range queries {
			if number(object(v)["expires"]) < now {
				delete(queries, k)
			}
		}
		if len(queries) >= 10 {
			oldest := ""
			var expiry int64 = 1 << 62
			for k, v := range queries {
				if n := number(object(v)["expires"]); n < expiry {
					expiry = n
					oldest = k
				}
			}
			delete(queries, oldest)
		}
		queries[qtoken] = map[string]any{"query": query, "category": category, "expires": now + 86400}
	}
	s.state["book_search"] = true
	s.state["book_queries"] = queries
	s.state["book_category"] = category
	result, e := catalog.CachedBooks(s.ctx, s.tx, s.engine.PublicCache, query, category, page)
	if e != nil {
		return e
	}
	body := fmt.Sprintf("<b>کتابخانه دانشگاه</b>\n%d نتیجه — صفحه %d از %d", result.Total, result.Page+1, result.Pages)
	if query != "" {
		body += "\nجست‌وجو: " + html.EscapeString(query)
	}
	if category != "" {
		for _, c := range catalog.Categories {
			if c.ID == category {
				body += "\n" + c.Name
			}
		}
	}
	rows := [][]Button{}
	ids := []int64{}
	for i, book := range result.Rows {
		title := []rune(book.Title)
		if len(title) > 230 {
			title = title[:230]
		}
		label := html.EscapeString(string(title))
		body += fmt.Sprintf("\n\n%d. ", result.Page*10+i+1)
		if regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`).MatchString(s.engine.Username) {
			body += fmt.Sprintf("<b><a href=\"https://t.me/%s?start=book_%d\">%s</a></b>", s.engine.Username, book.ID, label)
		} else {
			body += "<b>" + label + "</b>"
			rows = append(rows, []Button{b(string(title), fmt.Sprintf("ubooks:view:%d", book.ID))})
		}
		ids = append(ids, book.ID)
	}
	if result.Total == 0 {
		body += "\n\nکتابی پیدا نشد. نام کوتاه‌تر یا نام نویسنده را امتحان کنید."
	}
	nav := []Button{}
	if result.Page > 0 {
		nav = append(nav, b("→ قبلی", fmt.Sprintf("ubooks:page:%s:%d", qtoken, result.Page-1)))
	}
	nav = append(nav, b(fmt.Sprintf("%d / %d", result.Page+1, result.Pages), fmt.Sprintf("ubooks:page:%s:%d", qtoken, result.Page)))
	if result.Page+1 < result.Pages {
		nav = append(nav, b("بعدی ←", fmt.Sprintf("ubooks:page:%s:%d", qtoken, result.Page+1)))
	}
	rows = append(rows, nav)

	payload := map[string]any{"chat_id": s.chat, "text": body, "parse_mode": "HTML", "reply_markup": Markup{Inline: rows}, "link_preview_options": map[string]bool{"is_disabled": true}}
	method := "sendMessage"
	if editing {
		method = "editMessageText"
		payload["message_id"] = s.message
	}
	return s.emit(method, payload, Guard{Books: ids}, 10)
}
