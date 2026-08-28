package tui

import (
	"strings"
)

// RenderMarkdown converts a text block to styled, wrapped rows. It
// supports the subset of markdown models actually produce: headings,
// bullets, numbered lists, fenced code, blockquotes, links, inline
// code, bold, italic, and strikethrough.
func RenderMarkdown(text string, width int) []Row {
	var out []Row
	inFence := false
	for _, raw := range strings.Split(text, "\n") {
		line := raw
		trimmed := strings.TrimLeft(line, " ")

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			out = append(out, wrapSegments(
				[]Segment{{Text: trimmed, Attr: styleFence}}, width)...)
			continue
		}
		if inFence {
			out = append(out, wrapSegments(
				[]Segment{{Text: line, Attr: styleFence}}, width)...)
			continue
		}

		switch {
		case trimmed == "":
			out = append(out, Row{})
		case isHeading(trimmed):
			level, rest := splitHeading(trimmed)
			a := styleBold
			if level == 1 {
				a = styleAccent.withBold()
			}
			out = append(out, wrapSegments(
				[]Segment{{Text: rest, Attr: a}}, width)...)
		case isBullet(trimmed):
			rest := strings.TrimSpace(strings.TrimLeft(trimmed, "-*"))
			segs := []Segment{{Text: "•  ", Attr: styleAccent}}
			segs = append(segs, parseInline(rest)...)
			out = append(out, wrapSegments(segs, width)...)
		case isNumbered(trimmed):
			idx := strings.IndexAny(trimmed, ".)")
			marker, rest := trimmed[:idx+1], strings.TrimSpace(trimmed[idx+1:])
			segs := []Segment{{Text: " " + marker + " ", Attr: styleDim}}
			segs = append(segs, parseInline(rest)...)
			out = append(out, wrapSegments(segs, width)...)
		case strings.HasPrefix(trimmed, "> "):
			segs := []Segment{{Text: "│ ", Attr: styleDim}}
			segs = append(segs, parseInline(strings.TrimSpace(trimmed[2:]))...)
			for i := range segs {
				if i > 0 {
					segs[i].Attr = segs[i].Attr.withItalic()
				}
			}
			out = append(out, wrapSegments(segs, width)...)
		default:
			out = append(out, wrapSegments(parseInline(line), width)...)
		}
	}
	return out
}

func isHeading(s string) bool {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && n < len(s) && s[n] == ' '
}

func splitHeading(s string) (int, string) {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	return n, strings.TrimSpace(s[n:])
}

func isBullet(s string) bool {
	return (strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ")) &&
		!strings.HasPrefix(s, "** ")
}

func isNumbered(s string) bool {
	if len(s) < 3 || s[0] < '1' || s[0] > '9' {
		return false
	}
	i := 1
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i < len(s) && (s[i] == '.' || s[i] == ')') && i+1 < len(s) && s[i+1] == ' '
}

// parseInline splits a line into segments, handling `code`, **bold**,
// *italic*, ~~strike~~, and [text](url). Iteration is rune-aware.
func parseInline(s string) []Segment {
	var segs []Segment
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			segs = append(segs, Segment{Text: plain.String(), Attr: styleDefault})
			plain.Reset()
		}
	}

	r := []rune(s)
	for i := 0; i < len(r); {
		switch r[i] {
		case '`':
			if end := indexRune(r[i+1:], '`'); end >= 0 {
				flush()
				segs = append(segs, Segment{Text: string(r[i+1 : i+1+end]), Attr: styleCode})
				i += end + 2
				continue
			}
		case '*':
			if i+1 < len(r) && r[i+1] == '*' {
				if end := indexRunes(r[i+2:], []rune{'*', '*'}); end >= 0 {
					flush()
					segs = append(segs, Segment{Text: string(r[i+2 : i+2+end]), Attr: styleBold})
					i += end + 4
					continue
				}
			} else if i+1 < len(r) && r[i+1] != ' ' {
				if end := indexRune(r[i+1:], '*'); end >= 0 {
					flush()
					segs = append(segs, Segment{Text: string(r[i+1 : i+1+end]), Attr: styleItalic})
					i += end + 2
					continue
				}
			}
		case '~':
			if i+1 < len(r) && r[i+1] == '~' {
				if end := indexRunes(r[i+2:], []rune{'~', '~'}); end >= 0 {
					flush()
					segs = append(segs, Segment{Text: string(r[i+2 : i+2+end]), Attr: styleDimItalic})
					i += end + 4
					continue
				}
			}
		case '[':
			if textEnd, url, ok := parseLink(r[i:]); ok {
				flush()
				segs = append(segs, Segment{Text: string(r[i+1 : i+textEnd]), Attr: styleAccent})
				if url != "" {
					segs = append(segs, Segment{Text: " (" + url + ")", Attr: styleDim})
				}
				i += textEnd + 3 + len([]rune(url))
				continue
			}
		}
		plain.WriteRune(r[i])
		i++
	}
	flush()
	if len(segs) == 0 {
		return []Segment{}
	}
	return segs
}

func parseLink(r []rune) (textEnd int, url string, ok bool) {
	if len(r) < 5 || r[0] != '[' {
		return 0, "", false
	}
	closeBr := indexRune(r[1:], ']')
	if closeBr < 0 {
		return 0, "", false
	}
	textEnd = closeBr + 1 // index of ]
	if textEnd+1 >= len(r) || r[textEnd+1] != '(' {
		return 0, "", false
	}
	start := textEnd + 2
	depth := 0
	for i := start; i < len(r); i++ {
		switch r[i] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return textEnd, string(r[start:i]), true
			}
			depth--
		}
	}
	return 0, "", false
}

func indexRune(r []rune, want rune) int {
	for i, x := range r {
		if x == want {
			return i
		}
	}
	return -1
}

func indexRunes(r []rune, pat []rune) int {
	if len(pat) == 0 || len(r) < len(pat) {
		return -1
	}
	for i := 0; i+len(pat) <= len(r); i++ {
		match := true
		for j := range pat {
			if r[i+j] != pat[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func wrapSegments(segs []Segment, width int) []Row {
	if width <= 0 {
		return []Row{{Segs: segs}}
	}
	return WrapRow(Row{Segs: segs}, width)
}
