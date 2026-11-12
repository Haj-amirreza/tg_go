package bot

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/leisure"
	"html"
	"math/big"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

func randomN(n int) int {
	if n < 1 {
		return 0
	}
	v, e := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if e != nil {
		panic(e)
	}
	return int(v.Int64())
}
func (s *session) leisure() error {
	if strings.HasPrefix(s.route, "fun:xo:") {
		return s.xo()
	}
	r := s.route
	if r == "fun:home" {
		rows := [][]Button{{{Text: "فال حافظ با تفسیر", Data: "fun:fortune", Style: "success"}, b("شعر روز", "fun:poetry")}, {b("تکه‌کتاب و جمله", "fun:reading"), b("معرفی کتاب", "fun:books")}, {{Text: "دوز ❌⭕", Data: "fun:xo:new", Style: "primary"}, b("پانتومیم / ادابازی", "fun:charades")}, {b("چیستان", "fun:riddle"), b("شانس و تاس", "fun:chance")}, {b("بازگشت به شخصی", "u:personal"), b("منوی اصلی", "menu")}}
		if e := s.board(rows); e != nil {
			return e
		}
		return s.reply("<b>تفریح؛ یک نفس بین کلاس‌ها</b>\nشعر بخوان، فال بگیر یا چند دقیقه بازی کن.", rows, Guard{})
	}
	if r == "fun:poetry" {
		return s.reply("<b>شعر امروز را با کدام شاعر بخوانیم؟</b>", [][]Button{{b("حافظ", "fun:daily:hafez"), b("سعدی", "fun:daily:saadi"), b("مولانا", "fun:daily:rumi")}, {b("بازگشت", "fun:home")}}, Guard{})
	}
	if r == "fun:reading" {
		return s.reply("<b>چند خط برای مکث</b>", [][]Button{{b("تکه‌ای از کتاب", "fun:excerpt"), b("جملهٔ امروز", "fun:quote")}, {b("بازگشت", "fun:home")}}, Guard{})
	}
	if r == "fun:chance" {
		return s.reply("یک انتخاب تصادفی کوچک", [][]Button{{b("🎲 تاس بینداز", "fun:dice"), b("🪙 شیر یا خط", "fun:coin")}, {b("بازگشت", "fun:home")}}, Guard{})
	}
	if r == "fun:dice" || r == "fun:coin" {
		value := fmt.Sprintf("🎲 تاس تو: %d", randomN(6)+1)
		if r == "fun:coin" {
			value = []string{"🪙 شیر", "🪙 خط"}[randomN(2)]
		}
		return s.reply(value, [][]Button{{b("دوباره", r), b("بازگشت", "fun:home")}}, Guard{})
	}
	records, generation, e := leisure.Load(s.ctx, s.tx, s.engine.PublicCache)
	if e != nil {
		return e
	}
	if strings.HasPrefix(r, "fun:charades") {
		return s.charades(records, generation)
	}
	kind := "poem"
	if r == "fun:quote" {
		kind = "quote"
	}
	if r == "fun:excerpt" {
		kind = "excerpt"
	}
	if r == "fun:riddle" || strings.HasPrefix(r, "fun:answer:") {
		kind = "riddle"
	}
	if r == "fun:books" || strings.HasPrefix(r, "fun:book:") || r == "fun:books:random" {
		return s.recommendations(records, generation)
	}
	ids := leisure.Ordered(records, kind)
	if kind == "poem" {
		poet := "hafez"
		if strings.HasPrefix(r, "fun:daily:") {
			poet = strings.TrimPrefix(r, "fun:daily:")
		}
		filtered := []string{}
		for _, id := range ids {
			if text(records[kind][id]["poet"]) == poet {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	if len(ids) == 0 {
		return s.reply("هنوز محتوایی در این بخش ثبت نشده است.", [][]Button{{b("بازگشت", "fun:home")}}, Guard{Leisure: generation})
	}
	index := randomN(len(ids))
	zone, _ := time.LoadLocation("Asia/Tehran")
	day := time.Now().In(zone).Format("2006-01-02")
	if strings.HasPrefix(r, "fun:daily:") {
		sum := sha256.Sum256([]byte(day + strings.TrimPrefix(r, "fun:daily:")))
		n, _ := strconv.ParseInt(fmt.Sprintf("%x", sum)[:7], 16, 64)
		index = int(n % int64(len(ids)))
	}
	if kind == "quote" {
		sum := sha256.Sum256([]byte(day))
		n, _ := strconv.ParseInt(fmt.Sprintf("%x", sum)[:6], 16, 64)
		index = int(n % int64(len(ids)))
	}
	if strings.HasPrefix(r, "fun:answer:") {
		n, e := strconv.Atoi(strings.TrimPrefix(r, "fun:answer:"))
		if e == nil && n >= 0 && n < len(ids) {
			index = n
		}
	}
	item := records[kind][ids[index]]
	body := ""
	rows := [][]Button{}
	switch kind {
	case "poem":
		body = "<b>شعر روز | " + html.EscapeString(text(item["name"])) + "</b>\n" + html.EscapeString(text(item["title"]))
		if r == "fun:fortune" {
			body = "<b>فال حافظ</b>\n" + html.EscapeString(text(item["title"]))
		}
		verses, _ := item["verses"].([]any)
		for _, v := range verses {
			body += "\n" + html.EscapeString(text(v))
		}
		body += `\n\n<a href="` + html.EscapeString(text(item["source"])) + `">متن و منبع: گنجور</a>`
		body = strings.ReplaceAll(body, `\n`, "\n")
		if r == "fun:fortune" {
			body += "\n\n<b>برداشت ادبی</b>\n" + html.EscapeString(text(item["interpretation"])) + "\n\n<i>برای تأمل و سرگرمی؛ پیش‌بینی آینده نیست.</i>"
			rows = append(rows, []Button{b("فال تازه", "fun:fortune")})
		} else {
			rows = append(rows, []Button{b("انتخاب شاعر", "fun:poetry")})
		}
	case "quote":
		body = "<b>جملهٔ امروز</b>\n\n" + html.EscapeString(text(item["text"])) + "\n\n<i>نثر کوتاه اختصاصی ربات</i>"
	case "excerpt":
		body = "<b>تکه‌کتاب</b>\n\n" + html.EscapeString(text(item["text"])) + "\n\n" + html.EscapeString(text(item["source"]))
		rows = append(rows, []Button{b("تکهٔ بعدی", "fun:excerpt")})
	case "riddle":
		if strings.HasPrefix(r, "fun:answer:") {
			body = "<b>پاسخ چیستان</b>\n\n" + html.EscapeString(text(item["answer"]))
		} else {
			body = "<b>چیستان</b>\n\n" + html.EscapeString(text(item["question"]))
			rows = append(rows, []Button{b("دیدن پاسخ", fmt.Sprintf("fun:answer:%d", index))})
		}
		rows = append(rows, []Button{b("چیستان بعدی", "fun:riddle")})
	}
	rows = append(rows, []Button{b("بازگشت", "fun:home")})
	return s.reply(body, rows, Guard{Leisure: generation})
}
func (s *session) recommendations(records leisure.Records, generation int64) error {
	if s.route == "fun:books" {
		return s.reply("<b>معرفی کتاب | حال‌وهوای مطالعه‌ات چیست؟</b>", [][]Button{{b("کوتاه و شروع‌کردنی", "fun:book:short:0"), b("داستان ایرانی", "fun:book:iran:0")}, {b("معنا و اندیشه", "fun:book:thought:0"), b("ادبیات کلاسیک", "fun:book:classic:0")}, {b("🎲 یک کتاب غافلگیرم کن", "fun:books:random")}, {b("بازگشت", "fun:home")}}, Guard{Leisure: generation})
	}
	parts := strings.Split(s.route, ":")
	category := "all"
	page := 0
	if len(parts) == 4 {
		category = parts[2]
		page, _ = strconv.Atoi(parts[3])
	}
	ids := []string{}
	for _, id := range leisure.Ordered(records, "recommendation") {
		if category == "all" || text(records["recommendation"][id]["category"]) == category {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return s.reply("هنوز پیشنهادی در این دسته ثبت نشده.", [][]Button{{b("بازگشت", "fun:books")}}, Guard{Leisure: generation})
	}
	page = min(max(0, page), len(ids)-1)
	if s.route == "fun:books:random" {
		page = randomN(len(ids))
	}
	book := records["recommendation"][ids[page]]
	body := "<b>📚 " + html.EscapeString(text(book["title"])) + "</b>\nنویسنده: " + html.EscapeString(text(book["author"])) + "\n" + html.EscapeString(text(book["kind"])) + "\n\n<b>دربارهٔ کتاب</b>\n" + html.EscapeString(text(book["summary"])) + "\n\n<b>چرا بخوانم؟</b>\n" + html.EscapeString(text(book["why"]))
	nav := []Button{}
	if page > 0 {
		nav = append(nav, b("→ قبلی", fmt.Sprintf("fun:book:%s:%d", category, page-1)))
	}
	nav = append(nav, b(fmt.Sprintf("%d / %d", page+1, len(ids)), s.route))
	if page+1 < len(ids) {
		nav = append(nav, b("بعدی ←", fmt.Sprintf("fun:book:%s:%d", category, page+1)))
	}
	return s.reply(body, [][]Button{nav, {b("انتخاب حال‌وهوای دیگر", "fun:books"), b("جست‌وجوی کتاب دانشگاه", "ubooks:search")}}, Guard{Leisure: generation})
}

type xoGame struct {
	ID      string `json:"id"`
	Board   string `json:"board"`
	Turn    int    `json:"turn"`
	Expires int64  `json:"expires"`
}

func (s *session) gameReply(body string, rows [][]Button) error {
	if s.update.Callback != nil {
		return s.emit("editMessageText", map[string]any{"chat_id": s.chat, "message_id": s.message, "text": body, "parse_mode": "HTML", "reply_markup": Markup{Inline: rows}}, Guard{}, 10)
	}
	return s.reply(body, rows, Guard{})
}
func (s *session) xo() error {
	var game xoGame
	raw, _ := json.Marshal(s.state["leisure_xo"])
	json.Unmarshal(raw, &game)
	parts := strings.Split(s.route, ":")
	if s.route == "fun:xo:new" {
		game = xoGame{ID: token(), Board: ".........", Expires: time.Now().Unix() + 86400}
	} else {
		valid := len(parts) == 5 && parts[2] == game.ID && len(game.Board) == 9 && game.Expires >= time.Now().Unix() && leisure.Result(game.Board) == ""
		turn, cell := -1, -1
		if len(parts) == 5 {
			turn, _ = strconv.Atoi(parts[3])
			cell, _ = strconv.Atoi(parts[4])
		}
		valid = valid && turn == game.Turn && cell >= 0 && cell < 9
		if !valid || game.Board[cell] != '.' {
			return s.reply("این صفحه قدیمی شده یا خانه پر است؛ از آخرین صفحه بازی کن.", [][]Button{{b("بازی تازه", "fun:xo:new")}}, Guard{})
		}
		board := []byte(game.Board)
		board[cell] = 'X'
		game.Board = leisure.BotMove(string(board))
		game.Turn++
	}
	s.state["leisure_xo"] = game
	body := "<b>دوز | تو ❌، ربات ⭕</b>\n\n" + map[string]string{"": "نوبت توست؛ یک خانهٔ خالی را لمس کن.", "X": "آفرین، تو بردی! 🎉", "O": "این دور ربات برد. دوباره امتحان کنیم؟", "draw": "مساوی شد؛ بازی خوبی بود! 🤝"}[leisure.Result(game.Board)]
	rows := [][]Button{}
	for r := 0; r < 3; r++ {
		row := []Button{}
		for c := 0; c < 3; c++ {
			i := r*3 + c
			mark := map[byte]string{'.': "▫️", 'X': "❌", 'O': "⭕"}[game.Board[i]]
			style := map[byte]string{'.': "", 'X': "danger", 'O': "success"}[game.Board[i]]
			row = append(row, Button{Text: mark, Data: fmt.Sprintf("fun:xo:%s:%d:%d", game.ID, game.Turn, i), Style: style})
		}
		rows = append(rows, row)
	}
	rows = append(rows, []Button{b("بازی تازه", "fun:xo:new"), b("بازگشت", "fun:home")})
	return s.gameReply(body, rows)
}

type charadesGame struct {
	ID       string   `json:"id"`
	Level    string   `json:"level"`
	Team     int      `json:"team"`
	Scores   [2]int   `json:"scores"`
	Round    int      `json:"round"`
	Word     string   `json:"word"`
	Seen     []string `json:"seen"`
	Phase    string   `json:"phase"`
	Deadline int64    `json:"deadline"`
	Expires  int64    `json:"expires"`
	Outcome  string   `json:"outcome"`
}

var levels = map[string]int64{"easy": 60, "medium": 90, "hard": 120}

func charadesWord(game charadesGame, records leisure.Records) charadesGame {
	all := []string{}
	available := []string{}
	for _, id := range leisure.Ordered(records, "word") {
		if text(records["word"][id]["level"]) == game.Level {
			all = append(all, id)
			seen := false
			for _, s := range game.Seen {
				seen = seen || s == id
			}
			if !seen {
				available = append(available, id)
			}
		}
	}
	if len(available) == 0 {
		available = all
		game.Seen = nil
	}
	if len(available) == 0 {
		return game
	}
	id := available[randomN(len(available))]
	game.Seen = append(game.Seen, id)
	game.Word = text(records["word"][id]["text"])
	game.ID = token()
	game.Phase = "ready"
	game.Deadline = 0
	game.Expires = time.Now().Unix() + 86400
	return game
}
func (s *session) charades(records leisure.Records, generation int64) error {
	if s.route == "fun:charades" {
		return s.reply("<b>🎭 پانتومیم / ادابازی</b>\nدو تیم بسازید و گوشی را به اجراکننده بدهید. کلمه را بدون حرف زدن یا نوشتن اجرا کنید. حدس درست در زمان مجاز یک امتیاز دارد؛ نتیجه را گرداننده ثبت می‌کند.", [][]Button{{b("آسان | ۶۰ ثانیه", "fun:charades:new:easy")}, {b("متوسط | ۹۰ ثانیه", "fun:charades:new:medium")}, {b("سخت؛ ضرب‌المثل | ۱۲۰ ثانیه", "fun:charades:new:hard")}, {b("بازگشت", "fun:home")}}, Guard{Leisure: generation})
	}
	var game charadesGame
	raw, _ := json.Marshal(s.state["leisure_charades"])
	json.Unmarshal(raw, &game)
	p := strings.Split(s.route, ":")
	valid := false
	if len(p) == 4 && p[2] == "new" && levels[p[3]] > 0 {
		game = charadesWord(charadesGame{Level: p[3], Round: 1}, records)
		valid = game.Word != ""
	} else if len(p) == 4 && p[2] == game.ID && game.Expires >= time.Now().Unix() && game.Team >= 0 && game.Team < 2 && levels[game.Level] > 0 {
		action := p[3]
		now := time.Now().Unix()
		if game.Phase == "ready" && action == "start" {
			game.Phase = "playing"
			game.Deadline = now + levels[game.Level]
			valid = true
		} else if game.Phase == "ready" && action == "reroll" {
			game = charadesWord(game, records)
			valid = true
		} else if game.Phase == "playing" && (action == "correct" || action == "skip" || action == "check") {
			valid = true
			if now >= game.Deadline {
				game.Phase = "finished"
				game.Outcome = "زمان تمام شد؛ امتیازی ثبت نشد."
			} else if action == "correct" {
				game.Scores[game.Team]++
				game.Phase = "finished"
				game.Outcome = "درست حدس زدید! یک امتیاز برای این تیم."
			} else if action == "skip" {
				game.Phase = "finished"
				game.Outcome = "این دور رد شد؛ امتیازی ثبت نشد."
			}
		} else if game.Phase == "finished" && action == "next" {
			game.Team = 1 - game.Team
			game.Round++
			game = charadesWord(game, records)
			valid = true
		}
	}
	if !valid {
		return s.reply("این صفحه قدیمی شده یا این اقدام برای نوبت فعلی نیست.", [][]Button{{b("سطح و بازی تازه", "fun:charades")}}, Guard{Leisure: generation})
	}
	s.state["leisure_charades"] = game
	body := fmt.Sprintf("<b>🎭 ادابازی | دور %d</b>\nتیم ۱: %d امتیاز | تیم ۲: %d امتیاز\nنوبت تیم %d", game.Round, game.Scores[0], game.Scores[1], game.Team+1)
	prefix := "fun:charades:" + game.ID + ":"
	rows := [][]Button{}
	switch game.Phase {
	case "ready":
		body += "\n\nفقط اجراکننده کلمه را باز کند:\n<tg-spoiler>" + html.EscapeString(game.Word) + fmt.Sprintf("</tg-spoiler>\nزمان: %d ثانیه", levels[game.Level])
		rows = append(rows, []Button{{Text: "▶️ شروع اجرا", Data: prefix + "start", Style: "success"}}, []Button{b("کلمهٔ دیگری بده", prefix+"reroll")})
	case "playing":
		body += fmt.Sprintf("\n\nاجرا شروع شد؛ زمان باقی‌مانده در این لحظه: %d ثانیه", max(int64(0), game.Deadline-time.Now().Unix()))
		rows = append(rows, []Button{{Text: "✅ درست حدس زدیم", Data: prefix + "correct", Style: "success"}, {Text: "⏭ رد کردن", Data: prefix + "skip", Style: "danger"}}, []Button{b("⏱ بررسی زمان", prefix+"check")})
	case "finished":
		body += "\n\n" + html.EscapeString(game.Outcome) + "\nکلمهٔ این دور: <b>" + html.EscapeString(game.Word) + "</b>"
		rows = append(rows, []Button{{Text: fmt.Sprintf("نوبت تیم %d", 2-game.Team), Data: prefix + "next", Style: "success"}})
	}
	rows = append(rows, []Button{b("سطح و بازی تازه", "fun:charades"), b("بازگشت", "fun:home")})
	return s.gameReply(body, rows)
}
