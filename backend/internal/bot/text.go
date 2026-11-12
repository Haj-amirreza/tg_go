package bot

import (
	"html"
	"regexp"
	"unicode/utf16"
)

var linkTags = regexp.MustCompile(`<a href="([^"]+)">([^<]*)</a>`)
var htmlTags = regexp.MustCompile(`<[^>]*>`)

func visibleLength(value string) int {
	return len(utf16.Encode([]rune(html.UnescapeString(htmlTags.ReplaceAllString(value, "")))))
}
func splitPlain(value string, max int) []string {
	out := []string{}
	runes := []rune(value)
	start, n := 0, 0
	for i, r := range runes {
		size := 1
		if r > 0xffff {
			size = 2
		}
		if n+size > max {
			out = append(out, string(runes[start:i]))
			start = i
			n = 0
		}
		n += size
	}
	if start < len(runes) {
		out = append(out, string(runes[start:]))
	}
	return out
}
