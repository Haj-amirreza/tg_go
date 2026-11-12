package bot

import (
	"context"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/support"
	"github.com/jackc/pgx/v5"
	"html"
	"strconv"
	"strings"
	"time"
)

func (s *session) supportChat() error {
	p := strings.Split(s.route, ":")
	form := object(s.state["support_chat"])
	rows := [][]Button{}
	body := ""
	guard := Guard{}
	if s.route == "support:question" || s.route == "support:chat" {
		category := "سؤال دانشجویی"
		if s.route == "support:chat" {
			category = "گفتگو با مدیر"
		}
		s.state["support_chat"] = map[string]any{"category": category, "expires": time.Now().Unix() + 1800}
		return s.reply("پیامت را همین‌جا بنویس. پاسخ مدیر در همین ربات می‌آید.", [][]Button{{{Text: "پایان حالت نوشتن", Data: "support", Style: "danger"}}}, guard)
	}
	if s.typed && s.update.Message != nil && s.route == s.update.Message.Text && len(form) > 0 {
		if number(form["expires"]) < time.Now().Unix() {
			delete(s.state, "support_chat")
			return s.reply("زمان گفتگو پایان یافته؛ دوباره آن را باز کن.", [][]Button{{b("پشتیبانی", "support")}}, guard)
		}
		id := number(form["ticket"])
		var e error
		if id == 0 {
			id, e = support.Create(s.ctx, s.tx, s.user, s.route, text(form["category"]))
		} else {
			e = support.StudentReply(s.ctx, s.tx, s.user, id, "reply", s.route)
		}
		if e != nil {
			return e
		}
		s.state["support_chat"] = map[string]any{"ticket": id, "expires": time.Now().Unix() + 1800}
		return s.reply(fmt.Sprintf("پیامت در گفتگوی #%d ثبت شد. پیام بعدی را هم می‌توانی همین‌جا بنویسی.", id), [][]Button{{b("مشاهده گفتگو", fmt.Sprintf("support:ticket:%d", id))}, {b("پایان حالت نوشتن", "support")}}, Guard{Ticket: id})
	}
	delete(s.state, "support_chat")
	if len(p) >= 3 && (p[1] == "ticket" || p[1] == "reply" || p[1] == "close") {
		id, _ := strconv.ParseInt(p[2], 10, 64)
		page := -1
		if len(p) > 3 {
			page, _ = strconv.Atoi(p[3])
		}
		thread, e := support.Get(s.ctx, s.tx, id, s.user, false, page)
		if e != nil {
			return e
		}
		guard.Ticket = id
		if p[1] == "close" {
			if e = support.StudentReply(s.ctx, s.tx, s.user, id, "close", ""); e != nil {
				return e
			}
			body = "گفتگو بسته شد."
		} else if p[1] == "reply" {
			if thread.Ticket.State == "closed" {
				if e = support.StudentReply(s.ctx, s.tx, s.user, id, "reopen", ""); e != nil {
					return e
				}
			}
			s.state["support_chat"] = map[string]any{"ticket": id, "expires": time.Now().Unix() + 1800}
			body = fmt.Sprintf("پیام بعدی را برای گفتگوی #%d بنویس.", id)
		} else {
			body = fmt.Sprintf("<b>گفتگو #%d · %s</b>\nوضعیت: %s", id, html.EscapeString(thread.Ticket.Subject), html.EscapeString(thread.Ticket.State))
			for _, m := range thread.Messages {
				sender := "شما"
				if m.Admin != nil {
					sender = "مدیر"
				}
				body += "\n\n<b>" + sender + "</b>\n" + html.EscapeString(m.Body)
			}
			nav := []Button{}
			if thread.Page > 0 {
				nav = append(nav, b("→ پیام‌های قبل", fmt.Sprintf("support:ticket:%d:%d", id, thread.Page-1)))
			}
			nav = append(nav, b(fmt.Sprintf("%d / %d", thread.Page+1, thread.Pages), fmt.Sprintf("support:ticket:%d:%d", id, thread.Page)))
			if thread.Page+1 < thread.Pages {
				nav = append(nav, b("پیام‌های بعد ←", fmt.Sprintf("support:ticket:%d:%d", id, thread.Page+1)))
			}
			rows = append(rows, nav)
		}
		rows = append(rows, []Button{{Text: "ارسال پیام / بازکردن گفتگو", Data: fmt.Sprintf("support:reply:%d", id), Style: "success"}}, []Button{{Text: "بستن گفتگو", Data: fmt.Sprintf("support:close:%d", id), Style: "danger"}})
	} else {
		page := 0
		if len(p) == 3 && p[1] == "list" {
			page, _ = strconv.Atoi(p[2])
		}
		list, e := support.List(s.ctx, s.tx, s.user, false, "", page)
		if e != nil {
			return e
		}
		body = "<b>پرسش و گفتگو با مدیر</b>\nسؤالت را بپرس یا گفتگوی قبلی را ادامه بده."
		rows = append(rows, []Button{{Text: "پرسیدن سؤال جدید", Data: "support:question", Style: "success"}, b("چت با مدیر", "support:chat")})
		for i, t := range list {
			if i == 10 {
				break
			}
			rows = append(rows, []Button{b(fmt.Sprintf("#%d %s · %s", t.ID, t.Subject, t.State), fmt.Sprintf("support:ticket:%d", t.ID))})
		}
		nav := []Button{}
		if page > 0 {
			nav = append(nav, b("→ قبلی", fmt.Sprintf("support:list:%d", page-1)))
		}
		if len(list) > 10 {
			nav = append(nav, b("بعدی ←", fmt.Sprintf("support:list:%d", page+1)))
		}
		if len(nav) > 0 {
			rows = append(rows, nav)
		}
	}
	rows = append(rows, []Button{b("گفتگوهای من", "support"), b("منوی اصلی", "menu")})
	return s.reply(body, rows, guard)
}
func QueueTicketReply(ctx context.Context, tx pgx.Tx, event support.Event) error {
	if event.Message == 0 {
		return nil
	}
	chat, e := strconv.ParseInt(event.Telegram, 10, 64)
	if e != nil {
		return e
	}
	s := session{ctx: ctx, tx: tx, user: event.User, chat: chat, route: "support", activeModule: "support", keyPrefix: fmt.Sprintf("ticket:%d:reply:%d", event.Ticket, event.Message)}
	return s.reply(fmt.Sprintf("<b>پاسخ گفتگوی #%d</b>\n\n%s", event.Ticket, html.EscapeString(event.Body)), [][]Button{{b("ادامه گفتگو با مدیر", fmt.Sprintf("support:ticket:%d", event.Ticket))}}, Guard{Ticket: event.Ticket, TicketMessage: event.Message, Actor: event.Admin, Permission: "tickets"})
}
