package bot

import (
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/rich"
	"html"
	"net/url"
	"strings"
)

// Imported directories contain structured contacts, including source year and trust notes.
func directoryPresentation(meta map[string]any) (string, [][]Button) {
	body := ""
	buttons := []Button{}
	fields, _ := meta["directory_contacts"].([]any)
	for i, entry := range fields {
		if i >= 30 {
			break
		}
		f := object(entry)
		value, label := text(f["value"]), text(f["label"])
		if value == "" {
			continue
		}
		if label == "" {
			label = text(f["type"])
		}
		body += "\n" + html.EscapeString(label+": "+value)
		if year := number(f["year"]); year > 0 {
			body += fmt.Sprintf(" · منبع %d", year)
		}
		if len(buttons) >= 8 {
			continue
		}
		target := ""
		if text(f["type"]) == "url" && rich.URL(value) {
			target = value
		}
		if text(f["type"]) == "handle" && strings.HasPrefix(value, "@") {
			host := map[string]string{"telegram": "t.me", "eitaa": "eitaa.com", "bale": "ble.ir"}[text(f["platform"])]
			if host != "" {
				target = "https://" + host + "/" + url.PathEscape(strings.TrimPrefix(value, "@"))
			}
		}
		if target != "" {
			buttons = append(buttons, Button{Text: label, URL: target, Style: "success"})
		} else if len([]rune(value)) <= 256 {
			buttons = append(buttons, Button{Text: "کپی " + label, Copy: &CopyText{Text: value}})
		}
	}
	source := object(meta["directory_source"])
	if len(source) > 0 {
		trust := map[string]string{"official": "منبع رسمی", "announced": "اعلام‌شده در منبع", "unverified": "نیازمند بررسی", "historical": "قدیمی / تاریخی"}[catalog.MetaText(source, "trust")]
		if trust == "" {
			trust = "اطلاعات منبع"
		}
		body += "\n" + html.EscapeString(trust)
		if year := number(source["year"]); year > 0 {
			body += fmt.Sprintf(" · %d", year)
		}
		if note := text(source["note"]); note != "" {
			body += "\n" + html.EscapeString(note)
		}
	}
	rows := [][]Button{}
	for len(buttons) > 0 {
		n := min(2, len(buttons))
		rows = append(rows, buttons[:n])
		buttons = buttons[n:]
	}
	return body, rows
}
