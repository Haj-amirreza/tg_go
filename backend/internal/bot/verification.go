package bot

import (
	"encoding/json"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/legacy"
	"github.com/jackc/pgx/v5"
	"html"
	"regexp"
	"strings"
	"time"
)

func (s *session) verification() error {
	crypt, e := legacy.New(s.engine.AppKey)
	if e != nil {
		return s.reply("سامانهٔ امن احراز دانشجویی هنوز تنظیم نشده؛ با پشتیبانی تماس بگیر.", [][]Button{{b("پشتیبانی", "support")}}, Guard{Module: "feedback"})
	}
	s.sensitive = true
	var status, phase, encrypted, note string
	e = s.tx.QueryRow(s.ctx, `SELECT status,phase,details,review_note FROM student_verifications WHERE user_id=$1`, s.user).Scan(&status, &phase, &encrypted, &note)
	if e != nil && e != pgx.ErrNoRows {
		return e
	}
	restart := s.route == "verify:restart"
	if restart && status == "approved" {
		return s.reply("حساب تأیید شده است. برای تغییر اطلاعات با پشتیبانی تماس بگیر.", nil, Guard{})
	}
	if e == pgx.ErrNoRows || restart {
		raw, _ := crypt.Encrypt([]byte(`{}`))
		_, e = s.tx.Exec(s.ctx, `INSERT INTO student_verifications(user_id,details,created_at,updated_at) VALUES($1,$2,$3,$3) ON CONFLICT(user_id) DO UPDATE SET status='collecting',phase='phone',details=EXCLUDED.details,student_hash=NULL,revision=student_verifications.revision+1,updated_at=EXCLUDED.updated_at`, s.user, raw, time.Now().Unix())
		if e != nil {
			return e
		}
		status, phase, encrypted, note = "collecting", "phone", raw, ""
	}
	if status != "collecting" {
		delete(s.state, "verification_active")
		body := map[string]string{"pending": "درخواست ثبت شده و منتظر بررسی مدیر است.", "approved": "احراز دانشجویی تأیید شده است.", "rejected": "درخواست نیاز به ثبت دوباره دارد.", "revoked": "تأیید دانشجویی لغو شده است."}[status]
		rows := [][]Button{{b("منوی اصلی", "menu")}}
		if status == "rejected" || status == "revoked" {
			rows = append(rows, []Button{b("ثبت دوباره", "verify:restart")})
		}
		return s.reply(body+"\n"+html.EscapeString(note), rows, Guard{Module: "feedback"})
	}
	raw, e := crypt.Decrypt(encrypted)
	if e != nil {
		return e
	}
	details := map[string]string{}
	if json.Unmarshal(raw, &details) != nil {
		return catalog.ErrInvalid
	}
	navigation := s.route == "verify" || s.route == "/verify" || restart || s.update.Callback != nil
	errorText := ""
	s.state["verification_active"] = true
	if !navigation && s.update.Message != nil {
		switch phase {
		case "phone":
			c := s.update.Message.Contact
			if c == nil || c.User != s.from.ID || !regexp.MustCompile(`^\+?[0-9]{7,15}$`).MatchString(c.Phone) {
				errorText = "فقط تماس همین حساب تلگرام پذیرفته می‌شود.\n"
			} else {
				details["phone"] = c.Phone
				phase = "name"
			}
		case "name":
			if !catalog.Text(s.route, 120) || len([]rune(strings.TrimSpace(s.route))) < 3 {
				errorText = "نام کامل معتبر نیست.\n"
			} else {
				details["full_name"] = strings.TrimSpace(s.route)
				phase = "number"
			}
		case "number":
			number := catalog.Normalize(s.route)
			if !regexp.MustCompile(`^[0-9]{4,30}$`).MatchString(number) {
				errorText = "شماره دانشجویی باید ۴ تا ۳۰ رقم باشد.\n"
			} else {
				details["student_number"] = number
				phase = "complete"
			}
		}
	}
	hash := ""
	if number := details["student_number"]; number != "" {
		hash = legacy.SignJSON([]byte(number), s.engine.AppKey)
	}
	status = "collecting"
	if phase == "complete" {
		status = "pending"
		delete(s.state, "verification_active")
	}
	raw, _ = json.Marshal(details)
	encrypted, e = crypt.Encrypt(raw)
	if e != nil {
		return e
	}
	_, e = s.tx.Exec(s.ctx, `UPDATE student_verifications SET status=$1,phase=$2,details=$3,student_hash=NULLIF($4,''),revision=revision+1,updated_at=$5 WHERE user_id=$6`, status, phase, encrypted, hash, time.Now().Unix(), s.user)
	if e != nil {
		return e
	}
	body := errorText + map[string]string{"phone": "فقط گروه‌ها و ابزارهای شخصی نیازمند تأییدند. شمارهٔ همین حساب را با دکمهٔ زیر بفرست.", "name": "نام و نام خانوادگی خود را وارد کن.", "number": "شماره دانشجویی خود را وارد کن.", "complete": "درخواست ثبت شد؛ پس از تأیید مدیر با /start وارد شو."}[phase]
	rows := [][]Button{{b("منوی اصلی", "menu")}}
	if phase == "phone" {
		return s.emit("sendMessage", map[string]any{"chat_id": s.chat, "text": body, "reply_markup": map[string]any{"keyboard": [][]any{{map[string]any{"text": "ارسال شماره همین حساب", "request_contact": true}}, {map[string]any{"text": "منوی اصلی"}}}, "resize_keyboard": true, "one_time_keyboard": true}}, Guard{Module: "feedback"}, 10)
	}
	delete(s.state, "go_keyboard_hash")
	return s.reply(body, rows, Guard{Module: "feedback"})
}
