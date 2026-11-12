// Package rich validates portable block documents and compiles Telegram-safe fallbacks.
package rich

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/jackc/pgx/v5"
	"html"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Document struct {
	Schema int     `json:"schema"`
	Rich   bool    `json:"rich"`
	Blocks []Block `json:"blocks"`
}
type Slide struct {
	File    int64  `json:"file_id"`
	Caption string `json:"caption"`
}
type Item struct {
	Text     string `json:"text"`
	Action   string `json:"action"`
	Value    string `json:"value"`
	Style    string `json:"style"`
	Icon     string `json:"icon"`
	Emoji    string `json:"custom_emoji_id"`
	Disabled bool   `json:"disabled"`
	Break    bool   `json:"row_break"`
	Step     int64  `json:"step"`
	Min      int64  `json:"min"`
	Max      int64  `json:"max"`
	Audience string `json:"audience_tag"`
}
type Block struct {
	Type        string           `json:"type"`
	Enabled     bool             `json:"enabled"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Text        string           `json:"text"`
	Size        int              `json:"size"`
	Open        bool             `json:"open"`
	Rows        [][]string       `json:"rows,omitempty"`
	Header      bool             `json:"header"`
	Bordered    bool             `json:"bordered"`
	Striped     bool             `json:"striped"`
	Compact     bool             `json:"compact"`
	File        int64            `json:"file_id,omitempty"`
	Voice       bool             `json:"voice"`
	Slides      []Slide          `json:"slides,omitempty"`
	Items       []Item           `json:"items,omitempty"`
	Columns     int              `json:"columns"`
	Latex       string           `json:"latex,omitempty"`
	Inline      bool             `json:"inline"`
	Variables   []map[string]any `json:"variables,omitempty"`
	Example     string           `json:"example,omitempty"`
	Calculator  string           `json:"calculator,omitempty"`
	Unit        string           `json:"result_unit,omitempty"`
	Chart       json.RawMessage  `json:"chart,omitempty"`
	QuizID      int64            `json:"quiz_id,omitempty"`
	Speed       string           `json:"speed,omitempty"`
	Animate     bool             `json:"animate"`
	MinLength   int              `json:"min_length"`
	MaxLength   int              `json:"max_length"`
}

func bounded(s string, n int) bool {
	return utf8.RuneCountInString(s) <= n && !strings.ContainsRune(s, 0)
}
func URL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(s, "\r\n")
}

// HTML accepts a small explicit allow-list, rejects attributes and external XML entities.
func HTML(source string) (string, error) {
	source = xmlText(source)
	d := xml.NewDecoder(strings.NewReader("<root>" + source + "</root>"))
	d.Strict = true
	var out strings.Builder
	depth := 0
	stack := []string{}
	for {
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", catalog.ErrInvalid
		}
		switch v := t.(type) {
		case xml.StartElement:
			tag := v.Name.Local
			if depth == 0 {
				depth++
				continue
			}
			depth++
			if v.Name.Space != "" {
				return "", catalog.ErrInvalid
			}
			switch tag {
			case "b", "strong", "i", "em", "u", "ins", "s", "del", "code", "pre", "tg-spoiler", "sup", "sub", "br", "a":
			default:
				return "", catalog.ErrInvalid
			}
			if tag == "a" {
				if len(v.Attr) != 1 || v.Attr[0].Name.Local != "href" || !URL(v.Attr[0].Value) {
					return "", catalog.ErrInvalid
				}
				out.WriteString(`<a href="` + html.EscapeString(v.Attr[0].Value) + `">`)
			} else {
				if len(v.Attr) > 0 {
					return "", catalog.ErrInvalid
				}
				if tag == "sup" || tag == "sub" {
					out.WriteString(" ")
				} else if tag == "br" {
					out.WriteString("\n")
				} else {
					out.WriteString("<" + tag + ">")
				}
			}
			stack = append(stack, tag)
		case xml.EndElement:
			depth--
			if depth == 0 {
				continue
			}
			tag := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if tag != "br" && tag != "sup" && tag != "sub" {
				out.WriteString("</" + tag + ">")
			}
		case xml.CharData:
			out.WriteString(html.EscapeString(string(v)))
		default:
			return "", catalog.ErrInvalid
		}
	}
	return out.String(), nil
}

var menuRoute = regexp.MustCompile(`^(menu|u:library|u:rules|u:personal|u:links|u:systems|u:professor:(new|search|all:0)|button:[a-z0-9_]{1,40})$`)

func Validate(doc Document) error {
	raw, e := json.Marshal(doc)
	if e != nil || len(raw) > 262144 || doc.Schema != 1 || len(doc.Blocks) > 40 || len(doc.Blocks) == 0 {
		return catalog.ErrInvalid
	}
	buttons := 0
	for _, b := range doc.Blocks {
		if !bounded(b.Title, 200) || !bounded(b.Description, 2000) {
			return catalog.ErrInvalid
		}
		switch b.Type {
		case "text", "heading", "typing", "details":
			if !bounded(b.Text, 12000) {
				return catalog.ErrInvalid
			}
			if _, e := HTML(b.Text); e != nil {
				return e
			}
		case "table":
			if len(b.Rows) < 1 || len(b.Rows) > 30 || len(b.Rows[0]) < 1 || len(b.Rows[0]) > 10 {
				return catalog.ErrInvalid
			}
			for _, r := range b.Rows {
				if len(r) != len(b.Rows[0]) {
					return catalog.ErrInvalid
				}
				for _, v := range r {
					if !bounded(v, 500) {
						return catalog.ErrInvalid
					}
				}
			}
		case "image", "audio":
			if b.File < 1 {
				return catalog.ErrInvalid
			}
		case "slideshow":
			if len(b.Slides) < 2 || len(b.Slides) > 10 {
				return catalog.ErrInvalid
			}
			for _, s := range b.Slides {
				if s.File < 1 || !bounded(s.Caption, 1000) {
					return catalog.ErrInvalid
				}
			}
		case "formula":
			if !catalog.Text(b.Latex, 10000) || regexp.MustCompile(`(?i)\\(input|include|write|openout|href|url|html|def|newcommand)\b`).MatchString(b.Latex) {
				return catalog.ErrInvalid
			}
		case "chart":
			if len(b.Chart) == 0 || !json.Valid(b.Chart) {
				return catalog.ErrInvalid
			}
		case "quiz":
			if b.QuizID < 1 {
				return catalog.ErrInvalid
			}
		case "buttons":
			if len(b.Items) > 20 || b.Columns < 1 || b.Columns > 4 {
				return catalog.ErrInvalid
			}
			if b.Enabled {
				buttons += len(b.Items)
			}
			for _, i := range b.Items {
				if !catalog.Text(i.Text, 60) || !bounded(i.Value, 500) || !bounded(i.Icon, 8) || !bounded(i.Audience, 60) {
					return catalog.ErrInvalid
				}
				if i.Style != "" && i.Style != "default" && i.Style != "primary" && i.Style != "success" && i.Style != "danger" {
					return catalog.ErrInvalid
				}
				switch i.Action {
				case "url":
					if !URL(i.Value) {
						return catalog.ErrInvalid
					}
				case "copy":
					if !catalog.Text(i.Value, 256) {
						return catalog.ErrInvalid
					}
				case "menu":
					if !menuRoute.MatchString(i.Value) {
						return catalog.ErrInvalid
					}
				case "content", "quiz", "calculator":
					n, e := strconv.ParseInt(i.Value, 10, 64)
					if e != nil || n < 1 {
						return catalog.ErrInvalid
					}
				case "toggle", "confirm", "cancel", "increment", "decrement":
					if i.Step < 1 || i.Step > 100 || i.Min < -1000000 || i.Max > 1000000 || i.Min > i.Max {
						return catalog.ErrInvalid
					}
				default:
					return catalog.ErrInvalid
				}
			}
		default:
			return catalog.ErrInvalid
		}
	}
	if buttons > 90 {
		return catalog.ErrInvalid
	}
	return nil
}
func Files(doc Document) map[int64]string {
	out := map[int64]string{}
	for _, b := range doc.Blocks {
		if b.Type == "image" {
			out[b.File] = "image/"
		}
		if b.Type == "audio" {
			out[b.File] = "audio/"
		}
		for _, s := range b.Slides {
			out[s.File] = "image/"
		}
	}
	return out
}
func ValidateFiles(ctx context.Context, tx pgx.Tx, doc Document) error {
	wanted := Files(doc)
	if len(wanted) == 0 {
		return nil
	}
	ids := []int64{}
	for id := range wanted {
		ids = append(ids, id)
	}
	rows, e := tx.Query(ctx, `SELECT id,mime FROM files WHERE id=ANY($1)`, ids)
	if e != nil {
		return e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var mime string
		if e = rows.Scan(&id, &mime); e != nil {
			return e
		}
		if !strings.HasPrefix(mime, wanted[id]) {
			return catalog.ErrInvalid
		}
		count++
	}
	if rows.Err() != nil {
		return rows.Err()
	}
	if count != len(wanted) {
		return catalog.ErrInvalid
	}
	return nil
}
func Text(b Block) string {
	head := ""
	if b.Title != "" {
		head = "<b>" + html.EscapeString(b.Title) + "</b>\n"
	}
	if b.Description != "" {
		head += html.EscapeString(b.Description) + "\n"
	}
	switch b.Type {
	case "text", "typing":
		v, _ := HTML(b.Text)
		return head + v
	case "heading":
		v, _ := HTML(b.Text)
		return head + "<b>" + v + "</b>"
	case "details":
		v, _ := HTML(b.Text)
		return head + "<blockquote>" + v + "</blockquote>"
	case "table":
		lines := []string{}
		for _, row := range b.Rows {
			lines = append(lines, strings.Join(row, " │ "))
		}
		return head + "<pre>" + html.EscapeString(strings.Join(lines, "\n")) + "</pre>"
	case "formula":
		return head + "<code>" + html.EscapeString(b.Latex) + "</code>\n" + html.EscapeString(b.Example)
	case "chart":
		return head + "<pre>" + html.EscapeString(string(b.Chart)) + "</pre>"
	default:
		return head
	}
}
