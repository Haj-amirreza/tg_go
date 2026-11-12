package bot

import (
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/community"
	"html"
	"strconv"
	"strings"
	"time"
)

var formFields = map[string][]string{"channels": {"title", "url", "body"}, "market": {"title", "body", "price", "contact"}, "lost": {"title", "body", "place", "contact"}, "found": {"title", "body", "place", "contact"}, "personal": {"title", "body", "day", "clock"}, "notice": {"title", "body"}, "admin_reply": {"body"}}
var formLabels = map[string]string{"title": "عنوان یا نام را بنویس.", "body": "توضیح یا متن را بنویس.", "url": "آیدی @channel یا لینک معتبر https://t.me را بفرست.", "contact": "راه تماس قابل انتشار را بفرست؛ برای نداشتن تماس «-» بفرست.", "price": "قیمت به تومان را بنویس؛ برای قیمت توافقی «-» بفرست.", "place": "محل گم‌شدن یا پیدا شدن را بنویس؛ برای عبور «-» بفرست.", "day": "تاریخ میلادی را به شکل YYYY-MM-DD بنویس.", "clock": "ساعت را به شکل HH:MM بنویس؛ برای عبور «-» بفرست."}

func (s *session) startForm(kind string, extra map[string]any) error {
	if _, ok := formFields[kind]; !ok {
		return catalog.ErrInvalid
	}
	form := map[string]any{"kind": kind, "step": 0, "values": map[string]any{}, "expires": time.Now().Unix() + 1800, "nonce": token()}
	for k, v := range extra {
		form[k] = v
	}
	s.state["go_form"] = form
	delete(s.state, "support_chat")
	delete(s.state, "professor_form")
	delete(s.state, "book_search")
	prompt := formLabels[formFields[kind][0]]
	if number(form["id"]) > 0 {
		prompt += "\nبرای حفظ مقدار قبلی «=» بفرست. مقدار فعلی:\n" + html.EscapeString(text(object(form["values"])[formFields[kind][0]]))
	}
	return s.reply(prompt, [][]Button{{{Text: "لغو و بازگشت", Data: "form:cancel", Style: "danger"}}}, Guard{Approved: kind == "personal" || kind == "channels" || kind == "market" || kind == "lost" || kind == "found"})
}
func (s *session) form() error {
	f := object(s.state["go_form"])
	kind := text(f["kind"])
	fields, ok := formFields[kind]
	if !ok || number(f["expires"]) < time.Now().Unix() {
		delete(s.state, "go_form")
		return s.reply("زمان فرم پایان یافته؛ دوباره شروع کن.", [][]Button{{b("منوی اصلی", "menu")}}, Guard{})
	}
	if kind != "notice" && kind != "admin_reply" && kind != "personal" && !s.approved {
		return s.requireStudent()
	}
	if kind == "personal" && !s.approved {
		return s.requireStudent()
	}
	if s.route == "form:cancel" {
		delete(s.state, "go_form")
		return s.reply("ثبت لغو شد.", [][]Button{{b("بازگشت", formBack(kind))}}, Guard{})
	}
	step := int(number(f["step"]))
	values := object(f["values"])
	if strings.HasPrefix(s.route, "form:confirm:") {
		if s.route != "form:confirm:"+text(f["nonce"]) || step != len(fields) {
			return catalog.ErrInvalid
		}
		switch kind {
		case "channels", "market", "lost", "found":
			ad := community.Ad{ID: number(f["id"]), Revision: int(number(f["revision"])), Kind: kind, Title: text(values["title"]), Body: text(values["body"]), Contact: text(values["contact"]), URL: text(values["url"]), Place: text(values["place"]), Consent: true}
			if v := text(values["price"]); v != "" {
				n, _ := strconv.ParseInt(v, 10, 64)
				ad.Price = &n
			}
			_, e := community.Save(s.ctx, s.tx, ad, s.user, false)
			if e != nil {
				return e
			}
			delete(s.state, "go_form")
			return s.reply("ثبت شد و پس از بررسی مدیر منتشر می‌شود.", [][]Button{{b("بازگشت", formBack(kind))}}, Guard{Approved: true})
		case "personal":
			done, _ := f["done"].(bool)
			p := community.Personal{Done: done, ID: number(f["id"]), Revision: int(number(f["revision"])), Kind: text(f["personalKind"]), Title: text(values["title"]), Body: text(values["body"]), Day: text(values["day"]), Clock: text(values["clock"])}
			if _, e := community.PersonalSave(s.ctx, s.tx, s.user, p); e != nil {
				return e
			}
			delete(s.state, "go_form")
			return s.reply("مورد شخصی ذخیره شد.", [][]Button{{b("بازگشت", "u:personal")}}, Guard{Approved: true})
		case "notice":
			e := s.commitNotice(f, values)
			if e == nil {
				delete(s.state, "go_form")
			}
			return e
		case "admin_reply":
			e := s.commitAdminReply(f, values)
			if e == nil {
				delete(s.state, "go_form")
			}
			return e
		}
	}
	if !s.typed || s.update.Message == nil || s.route != s.update.Message.Text || step >= len(fields) {
		return s.formPreview(f)
	}
	field := fields[step]
	value := strings.TrimSpace(s.route)
	if value == "=" {
		value = text(values[field])
	}
	if kind == "personal" && field == "body" && value == "-" {
		value = ""
	}
	if value == "-" && field != "title" && field != "body" && field != "url" {
		value = ""
	}
	valid := true
	switch field {
	case "title":
		valid = catalog.Text(value, 160)
	case "body":
		valid = catalog.Text(value, 3000)
		if kind == "personal" {
			valid = len([]rune(value)) <= 2000
		}
		if kind == "channels" {
			valid = catalog.Text(value, 500)
		}
	case "url":
		v, e := community.ChannelURL(value)
		valid = e == nil
		value = v
	case "price":
		if value != "" {
			value = catalog.Normalize(value)
			n, e := strconv.ParseInt(value, 10, 64)
			valid = e == nil && n >= 0 && n <= 1000000000000
		}
	case "contact":
		valid = len([]rune(value)) <= 300
	case "place":
		valid = len([]rune(value)) <= 200
	case "day":
		d, e := time.Parse("2006-01-02", value)
		valid = e == nil && d.Format("2006-01-02") == value
	case "clock":
		_, e := time.Parse("15:04", value)
		valid = value == "" || e == nil
	}
	if !valid {
		return s.reply("مقدار معتبر نیست.\n"+formLabels[field], [][]Button{{b("لغو", "form:cancel")}}, Guard{})
	}
	values[field] = value
	f["values"] = values
	step++
	f["step"] = step
	s.state["go_form"] = f
	if step == len(fields) {
		return s.formPreview(f)
	}
	return s.reply(formLabels[fields[step]], [][]Button{{b("لغو", "form:cancel")}}, Guard{})
}
func (s *session) formPreview(f map[string]any) error {
	body := "<b>پیش‌نمایش؛ هنوز ثبت نشده</b>"
	values := object(f["values"])
	kind := text(f["kind"])
	for _, field := range formFields[kind] {
		if v := text(values[field]); v != "" {
			body += "\n\n" + html.EscapeString(v)
		}
	}
	label := "ثبت نهایی"
	if kind == "market" || kind == "lost" || kind == "found" {
		body += "\n\nبا ثبت، اجازهٔ انتشار راه تماس بالا را برای این آگهی می‌دهی."
	}
	if kind == "channels" {
		label = "ارسال برای تأیید مدیر"
	}
	if kind == "admin_reply" {
		label = "تأیید پاسخ"
	}
	return s.reply(body, [][]Button{{{Text: label, Data: "form:confirm:" + text(f["nonce"]), Style: "success"}}, {b("لغو و بازگشت", "form:cancel")}}, Guard{})
}
func formBack(kind string) string {
	switch kind {
	case "channels":
		return "fun:channels"
	case "market":
		return "market"
	case "lost", "found":
		return "lostfound"
	case "notice", "admin_reply":
		return "ac:home"
	}
	return "u:personal"
}
func (s *session) requireStudent() error {
	return s.reply("این بخش به تأیید دانشجویی نیاز دارد؛ درخواستت را ثبت کن و منتظر بررسی مدیر بمان. تفریح و امکانات عمومی در دسترس‌اند.", [][]Button{{{Text: "احراز دانشجویی", Data: "verify", Style: "success"}}, {b("بازگشت به بخش شخصی", "u:personal"), b("منوی اصلی", "menu")}}, Guard{Module: "feedback", Route: "system:feedback"})
}
