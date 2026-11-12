package bot

import (
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"html"
	"strconv"
	"strings"
	"time"
)

var professorFields = []string{"name", "courses", "faculty", "major", "phone", "handle", "email"}
var professorLabels = []string{"نام و نام خانوادگی استاد", "دروسی که تدریس می‌کند", "دانشکده", "رشته", "شماره تماس", "آیدی تلگرام", "ایمیل"}

func (s *session) professors() error {
	form := object(s.state["professor_form"])
	now := time.Now().Unix()
	r := s.route
	if r == "u:professor:cancel" || r == "/cancel" {
		delete(s.state, "professor_form")
		return s.reply("به بخش اساتید برگشتید.", [][]Button{{b("استادشناسی", "u:people:professors:0")}}, Guard{})
	}
	if r == "u:professor:new" {
		s.state["professor_form"] = map[string]any{"mode": "new", "step": 0, "values": map[string]any{}, "nonce": token(), "expires": now + 3600}
		return s.reply("معرفی استاد — فرم ۷ مرحله‌ای\nاطلاعات پس از بررسی مدیر منتشر می‌شود.\n۱/۷: نام و نام خانوادگی استاد را بفرستید.", [][]Button{{b("لغو و بازگشت", "u:professor:cancel")}}, Guard{})
	}
	if r == "u:professor:search" {
		s.state["professor_form"] = map[string]any{"mode": "search", "expires": now + 3600}
		return s.reply("نام استاد، درس، دانشکده، رشته، شماره، آیدی یا ایمیل را بنویسید (۲ تا ۱۰۰ حرف).", [][]Button{{b("لغو و بازگشت", "u:professor:cancel")}}, Guard{})
	}
	if r == "u:people:contacts:0" {
		delete(s.state, "professor_form")
		return s.reply("شماره‌ها و اساتید", [][]Button{{b("استادشناسی", "button:professors")}, {b("اطلاعات تماس", "list:contacts:0")}, {b("منوی اصلی", "menu")}}, Guard{})
	}
	page := -1
	query := ""
	mode := "all"
	if r == "u:people:professors:0" {
		page = 0
		delete(s.state, "professor_form")
	}
	if strings.HasPrefix(r, "u:professor:all:") || strings.HasPrefix(r, "u:professor:results:") {
		parts := strings.Split(r, ":")
		n, e := strconv.Atoi(parts[len(parts)-1])
		if e != nil || n < 0 || n > 10000 {
			return s.reply("صفحه معتبر نیست.", nil, Guard{})
		}
		page = n
		mode = parts[2]
		if mode == "results" {
			snapshot := object(s.state["professor_search"])
			if number(snapshot["expires"]) < now {
				return s.reply("جست‌وجو منقضی شده؛ دوباره جست‌وجو کنید.", nil, Guard{})
			}
			query = text(snapshot["query"])
		}
		delete(s.state, "professor_form")
	}
	if page >= 0 {
		return s.professorList(query, page, mode)
	}
	if number(form["expires"]) < now {
		delete(s.state, "professor_form")
		return s.reply("فرم منقضی شده؛ دوباره معرفی استاد یا جست‌وجو را انتخاب کنید.", nil, Guard{})
	}
	if form["mode"] == "search" && s.typed {
		q := catalog.Normalize(r)
		if !catalog.Text(q, 100) || len([]rune(q)) < 2 {
			return s.reply("برای جست‌وجو بین ۲ تا ۱۰۰ حرف بنویسید.", nil, Guard{})
		}
		s.state["professor_search"] = map[string]any{"query": q, "expires": now + 3600}
		delete(s.state, "professor_form")
		return s.professorList(q, 0, "results")
	}
	step := int(number(form["step"]))
	values := object(form["values"])
	nonce := text(form["nonce"])
	if s.typed && r == "ثبت نهایی" && step == 7 {
		r = "u:professor:confirm:" + nonce
	}
	if s.typed && r == "رد کردن این فیلد" && step >= 4 {
		r = fmt.Sprintf("u:professor:skip:%s:%d", nonce, step)
	}
	if r == "u:professor:confirm:"+nonce && step == 7 {
		body := "دروس: " + text(values["courses"]) + "\nدانشکده: " + text(values["faculty"]) + "\nرشته: " + text(values["major"])
		meta := map[string]any{"person_role": "professor", "office_phone": values["phone"], "foreign_id": values["handle"], "email": values["email"], "access": "public", "consent": false, "audience_tag": "", "topic_ids": []int{}, "student_submission": map[string]any{"user_id": s.user, "nonce": nonce}}
		raw, _ := json.Marshal(meta)
		var id int64
		e := s.tx.QueryRow(s.ctx, `INSERT INTO contents(kind,title,body,metadata,status,created_at,updated_at,search_text) VALUES('professors',$1,$2,$3,'pending',$4,$4,$5) RETURNING id`, text(values["name"]), body, string(raw), now, catalog.Normalize(text(values["name"])+" "+body+" "+string(raw))).Scan(&id)
		if e != nil {
			return e
		}
		snapshot, _ := json.Marshal(map[string]any{"title": values["name"], "body": body, "metadata": meta})
		_, e = s.tx.Exec(s.ctx, `INSERT INTO content_revisions(content_id,version,snapshot,created_at) VALUES($1,1,$2,$3)`, id, string(snapshot), now)
		if e != nil {
			return e
		}
		delete(s.state, "professor_form")
		return s.reply("معرفی استاد ثبت شد و در انتظار بررسی مدیر است.", [][]Button{{b("جست‌وجوی همهٔ اساتید", "u:professor:search"), b("بازگشت", "u:people:professors:0")}}, Guard{})
	}
	if step < 0 || step > 7 {
		return s.reply("فرم معتبر نیست؛ دوباره شروع کنید.", nil, Guard{})
	}
	if step == 7 {
		return s.reply("پیش‌نمایش را بررسی کنید و ثبت نهایی را بزنید.", [][]Button{{b("ثبت نهایی", "u:professor:confirm:"+nonce)}, {b("لغو و بازگشت", "u:professor:cancel")}}, Guard{})
	}
	skip := r == fmt.Sprintf("u:professor:skip:%s:%d", nonce, step) && step >= 4
	if !s.typed && !skip {
		return s.reply("از گزینه‌های همین فرم استفاده کنید.", nil, Guard{})
	}
	value := ""
	if !skip {
		var e error
		value, e = catalog.ProfessorField(professorFields[step], r)
		if e != nil {
			return s.reply(professorLabels[step]+" معتبر وارد کنید یا در صورت اختیاری بودن آن را رد کنید.", nil, Guard{})
		}
	}
	values[professorFields[step]] = value
	step++
	form["step"] = step
	form["values"] = values
	s.state["professor_form"] = form
	if step == 7 {
		body := "<b>پیش‌نمایش معرفی استاد</b>"
		for i, field := range professorFields {
			body += "\n" + professorLabels[i] + ": " + html.EscapeString(text(values[field]))
		}
		return s.reply(body, [][]Button{{b("ثبت نهایی", "u:professor:confirm:"+nonce)}, {b("لغو و بازگشت", "u:professor:cancel")}}, Guard{})
	}
	rows := [][]Button{}
	if step >= 4 {
		rows = append(rows, []Button{b("رد کردن این فیلد", fmt.Sprintf("u:professor:skip:%s:%d", nonce, step))})
	}
	rows = append(rows, []Button{b("لغو و بازگشت", "u:professor:cancel")})
	return s.reply(fmt.Sprintf("%d/۷: %s را بفرستید.", step+1, professorLabels[step]), rows, Guard{})
}
func (s *session) professorList(query string, page int, mode string) error {
	result, e := catalog.Search(s.ctx, s.tx, "professors", query, page, s.tags, s.approved)
	if e != nil {
		return e
	}
	body := fmt.Sprintf("<b>همهٔ اساتید</b> — صفحه %d از %d\n%d نتیجه", result.Page+1, result.Pages, result.Total)
	if query != "" {
		body += "\nجست‌وجو: " + html.EscapeString(query)
	}
	rows := [][]Button{}
	versions := map[string]int{}
	for i, c := range result.Rows {
		body += fmt.Sprintf("\n\n%d. %s", result.Page*10+i+1, html.EscapeString(c.Title))
		contacts := []string{}
		for _, key := range []string{"office_phone", "personal_phone", "foreign_id", "domestic_id", "email"} {
			if value := catalog.MetaText(c.Metadata, key); value != "" {
				contacts = append(contacts, value)
			}
		}
		if len(contacts) > 0 {
			body += "\n" + html.EscapeString(strings.Join(contacts, " | "))
		}
		rows = append(rows, []Button{b(fmt.Sprintf("%d. جزئیات %s", result.Page*10+i+1, c.Title), fmt.Sprintf("view:%d", c.ID))})
		versions[strconv.FormatInt(c.ID, 10)] = c.Version
	}
	if result.Total == 0 {
		body += "\nموردی پیدا نشد. نام دیگری جست‌وجو کنید یا یک استاد معرفی کنید."
	}
	nav := []Button{}
	if result.Page > 0 {
		nav = append(nav, b("صفحه قبل", fmt.Sprintf("u:professor:%s:%d", mode, result.Page-1)))
	}
	nav = append(nav, b(fmt.Sprintf("%d / %d", result.Page+1, result.Pages), fmt.Sprintf("u:professor:%s:%d", mode, result.Page)))
	if result.Page+1 < result.Pages {
		nav = append(nav, b("صفحه بعد", fmt.Sprintf("u:professor:%s:%d", mode, result.Page+1)))
	}
	contextRows := [][]Button{nav, []Button{b("جست‌وجوی همهٔ اساتید", "u:professor:search"), b("معرفی استاد", "u:professor:new")}, []Button{{Text: "فهرست همهٔ اساتید", Data: "u:professor:all:0", Style: "danger"}}, []Button{b("بازگشت", "u:people:contacts:0"), b("منوی اصلی", "menu")}}
	if err := s.board(contextRows); err != nil {
		return err
	}
	return s.reply(body, rows, Guard{Content: versions})
}
