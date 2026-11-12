package rich

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"
)

var entityPattern = regexp.MustCompile(`&(?:#x[0-9a-fA-F]+|#[0-9]+|[a-zA-Z]+);|&`)
var inlineMath = regexp.MustCompile(`\$[^$\n]+\$`)

func xmlText(source string) string {
	source = strings.ReplaceAll(source, "<br>", "<br/>")
	return entityPattern.ReplaceAllStringFunc(source, func(s string) string { return html.EscapeString(html.UnescapeString(s)) })
}
func collapse(nodes []any) any {
	if len(nodes) == 1 {
		return nodes[0]
	}
	return nodes
}
func NativeText(source string) any {
	type frame struct {
		tag        string
		attributes []xml.Attr
		children   []any
	}
	stack := []frame{{tag: "root", children: []any{}}}
	d := xml.NewDecoder(strings.NewReader(xmlText(source)))
	math := inlineMath
	for {
		token, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return source
		}
		switch v := token.(type) {
		case xml.StartElement:
			stack = append(stack, frame{tag: v.Name.Local, attributes: v.Attr, children: []any{}})
		case xml.CharData:
			current := &stack[len(stack)-1]
			s := string(v)
			offset := 0
			for _, pair := range math.FindAllStringIndex(s, -1) {
				if pair[0] > offset {
					current.children = append(current.children, s[offset:pair[0]])
				}
				if len([]rune(s[pair[0]+1:pair[1]-1])) <= 2000 {
					current.children = append(current.children, map[string]any{"type": "mathematical_expression", "expression": s[pair[0]+1 : pair[1]-1]})
				} else {
					current.children = append(current.children, s[pair[0]:pair[1]])
				}
				offset = pair[1]
			}
			if offset < len(s) {
				current.children = append(current.children, s[offset:])
			}
		case xml.EndElement:
			if len(stack) < 2 {
				return source
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			var node any
			tag := map[string]string{"b": "bold", "strong": "bold", "i": "italic", "em": "italic", "u": "underline", "ins": "underline", "s": "strikethrough", "del": "strikethrough", "code": "code", "pre": "code", "sup": "superscript", "sub": "subscript", "tg-spoiler": "spoiler"}[f.tag]
			if f.tag == "br" {
				node = "\n"
			} else if f.tag == "a" {
				href := ""
				for _, a := range f.attributes {
					if a.Name.Local == "href" {
						href = a.Value
					}
				}
				node = map[string]any{"type": "url", "url": href, "text": collapse(f.children)}
			} else {
				node = map[string]any{"type": tag, "text": collapse(f.children)}
			}
			parent := &stack[len(stack)-1]
			parent.children = append(parent.children, node)
		}
	}
	return collapse(stack[0].children)
}

// Native matches the PHP rich-message wire shape. Delivery retains a standard fallback.
func Native(doc Document) map[string]any {
	blocks := []map[string]any{}
	photo := func(id int64, caption string) map[string]any {
		return map[string]any{"type": "photo", "photo": map[string]any{"type": "photo", "media": fileMarker(id, "photo")}, "caption": map[string]any{"text": caption}}
	}
	for _, b := range doc.Blocks {
		if !b.Enabled || b.Type == "audio" {
			continue
		}
		if b.Title != "" {
			blocks = append(blocks, map[string]any{"type": "heading", "size": 3, "text": b.Title})
		}
		if b.Description != "" {
			blocks = append(blocks, map[string]any{"type": "paragraph", "text": b.Description})
		}
		switch b.Type {
		case "text", "typing":
			blocks = append(blocks, map[string]any{"type": "paragraph", "text": NativeText(b.Text)})
		case "heading":
			size := b.Size
			if size < 1 || size > 6 {
				size = 2
			}
			blocks = append(blocks, map[string]any{"type": "heading", "size": size, "text": NativeText(b.Text)})
		case "details":
			summary := b.Title
			if summary == "" {
				summary = "بیشتر بخوانید"
			}
			blocks = append(blocks, map[string]any{"type": "details", "summary": summary, "blocks": []map[string]any{{"type": "paragraph", "text": NativeText(b.Text)}}, "is_open": b.Open})
		case "formula":
			math := map[string]any{"type": "mathematical_expression", "expression": b.Latex}
			if b.Inline {
				blocks = append(blocks, map[string]any{"type": "paragraph", "text": math})
			} else {
				blocks = append(blocks, math)
			}
			if b.Example != "" {
				blocks = append(blocks, map[string]any{"type": "paragraph", "text": b.Example})
			}
		case "table":
			rows := [][]map[string]any{}
			for i, row := range b.Rows {
				cells := []map[string]any{}
				for _, v := range row {
					cells = append(cells, map[string]any{"text": v, "is_header": b.Header && i == 0, "align": "right", "valign": "top"})
				}
				rows = append(rows, cells)
			}
			blocks = append(blocks, map[string]any{"type": "table", "cells": rows, "is_bordered": b.Bordered, "is_striped": b.Striped, "is_compact": b.Compact})
		case "image":
			blocks = append(blocks, photo(b.File, b.Description))
		case "slideshow":
			slides := []map[string]any{}
			for _, s := range b.Slides {
				slides = append(slides, photo(s.File, s.Caption))
			}
			blocks = append(blocks, map[string]any{"type": "slideshow", "blocks": slides})
		case "chart":
			blocks = append(blocks, map[string]any{"type": "paragraph", "text": string(b.Chart)})
		case "quiz":
			blocks = append(blocks, map[string]any{"type": "paragraph", "text": "برای شروع آزمون، از بخش آموزش استفاده کنید."})
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "paragraph", "text": "محتوای آموزشی"})
	}
	return map[string]any{"blocks": blocks, "is_rtl": true}
}

func fileMarker(id int64, kind string) string { return fmt.Sprintf("unibot-file:%d:%s", id, kind) }
