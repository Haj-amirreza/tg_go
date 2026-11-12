package rich

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSafeHTMLAndStructuralLimits(t *testing.T) {
	for _, s := range []string{`<b>سلام</b> <a href="https://example.org">پیوند</a>`, `<tg-spoiler>کلمه</tg-spoiler>`, `<code>&lt;script&gt;</code>`} {
		if _, e := HTML(s); e != nil {
			t.Fatal(s, e)
		}
	}
	for _, s := range []string{`<script>alert(1)</script>`, `<a href="javascript:alert(1)">x</a>`, `<!DOCTYPE x>`, `<b onclick="x">a</b>`, `<b>unclosed`, `<a href="https://u:p@example.org">x</a>`} {
		if _, e := HTML(s); e == nil {
			t.Fatal("unsafe html", s)
		}
	}
	doc := Document{Schema: 1, Rich: true, Blocks: []Block{{Type: "table", Enabled: true, Rows: [][]string{{"نام", "شماره"}, {"دانشگاه", "1"}}}}}
	if Validate(doc) != nil {
		t.Fatal("valid table")
	}
	doc.Blocks[0].Rows[1] = []string{"only one"}
	if Validate(doc) == nil {
		t.Fatal("ragged table accepted")
	}
	doc.Blocks = []Block{{Type: "text", Text: strings.Repeat("الف", 12001)}}
	if Validate(doc) == nil {
		t.Fatal("oversized text")
	}
}
func TestNativeRichPreservesTableAndFormatting(t *testing.T) {
	d := Document{Schema: 1, Rich: true, Blocks: []Block{{Type: "text", Enabled: true, Text: `<b>تیتر</b><br><a href="https://example.org/?a=1&amp;b=2">پیوند</a> $x^2$`}, {Type: "table", Enabled: true, Header: true, Bordered: true, Rows: [][]string{{"نام", "شماره"}, {"نمونه", "123"}}}, {Type: "slideshow", Enabled: true, Slides: []Slide{{File: 1, Caption: "یک"}, {File: 2, Caption: "دو"}}}}}
	if e := Validate(d); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(Native(d))
	if e != nil {
		t.Fatal(e)
	}
	for _, part := range []string{`"type":"bold"`, `"type":"url"`, `"type":"mathematical_expression"`, `"type":"table"`, `"type":"slideshow"`, `unibot-file:1:photo`} {
		if !strings.Contains(string(raw), part) {
			t.Fatal("lost rich structure", part, string(raw))
		}
	}
}
