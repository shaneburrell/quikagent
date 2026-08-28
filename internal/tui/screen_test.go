package tui

import (
	"strings"
	"testing"
)

func renderTo(w, h int, rows []Row) string {
	var buf strings.Builder
	r := NewRenderer(&buf)
	r.Resize(w, h)
	r.Render(rows)
	return buf.String()
}

func TestRenderWritesContent(t *testing.T) {
	out := renderTo(10, 3, []Row{{Segs: []Segment{{Text: "hello", Attr: styleDefault}}}})
	if !strings.Contains(out, "hello") {
		t.Fatalf("out = %q", out)
	}
	// Cursor moves are 1-based.
	if !strings.Contains(out, "\x1b[1;1H") {
		t.Fatalf("missing home move: %q", out)
	}
}

func TestRenderSkipsUnchangedCells(t *testing.T) {
	var buf strings.Builder
	r := NewRenderer(&buf)
	r.Resize(10, 2)
	frame := []Row{{Segs: []Segment{{Text: "ab", Attr: styleDefault}}}}
	r.Render(frame)
	first := buf.Len()

	buf.Reset()
	r.Render(frame) // identical frame
	if buf.Len() != 0 {
		t.Fatalf("unchanged frame produced output: %q", buf.String())
	}
	if first == 0 {
		t.Fatal("first frame produced no output")
	}
}

func TestRenderPartialUpdate(t *testing.T) {
	var buf strings.Builder
	r := NewRenderer(&buf)
	r.Resize(10, 1)
	r.Render([]Row{{Segs: []Segment{{Text: "aaaa", Attr: styleDefault}}}})
	buf.Reset()

	r.Render([]Row{{Segs: []Segment{{Text: "aXaa", Attr: styleDefault}}}})
	out := buf.String()
	if !strings.Contains(out, "X") {
		t.Fatalf("missing changed char: %q", out)
	}
	// Only one cell changed; cursor move to column 2 (1-based 3... col idx 1 -> 2).
	if !strings.Contains(out, "\x1b[1;2H") {
		t.Fatalf("expected move to changed cell: %q", out)
	}
}

func TestRenderAttrChangeEmitsSGR(t *testing.T) {
	out := renderTo(6, 1, []Row{{Segs: []Segment{
		{Text: "ab", Attr: styleDefault},
		{Text: "cd", Attr: styleAccent},
	}}})
	if !strings.Contains(out, "38;2;88;166;255") { // accent #58a6ff
		t.Fatalf("missing accent SGR: %q", out)
	}
}

func TestRenderTruncatesToWidth(t *testing.T) {
	out := renderTo(3, 1, []Row{{Segs: []Segment{{Text: "abcdef", Attr: styleDefault}}}})
	if strings.Contains(out, "def") || strings.Contains(out, "de\x00") {
		t.Fatalf("content beyond width leaked: %q", out)
	}
	if !strings.Contains(out, "abc") {
		t.Fatalf("missing content: %q", out)
	}
}

func TestWrapRow(t *testing.T) {
	row := Row{Segs: []Segment{{Text: "one two three", Attr: styleDefault}}}
	wrapped := WrapRow(row, 7)
	if len(wrapped) != 2 {
		t.Fatalf("wrapped = %+v", wrapped)
	}
	if textOf(wrapped[0]) != "one two" {
		t.Fatalf("line0 = %q", textOf(wrapped[0]))
	}
	if textOf(wrapped[1]) != "three" {
		t.Fatalf("line1 = %q", textOf(wrapped[1]))
	}
}

func TestWrapRowHardBreak(t *testing.T) {
	row := Row{Segs: []Segment{{Text: "abcdefghij", Attr: styleDefault}}}
	wrapped := WrapRow(row, 4)
	if len(wrapped) != 3 {
		t.Fatalf("wrapped = %+v", wrapped)
	}
	if textOf(wrapped[0]) != "abcd" || textOf(wrapped[1]) != "efgh" || textOf(wrapped[2]) != "ij" {
		t.Fatalf("lines = %q %q %q", textOf(wrapped[0]), textOf(wrapped[1]), textOf(wrapped[2]))
	}
}

func TestWrapRowPreservesAttrs(t *testing.T) {
	row := Row{Segs: []Segment{
		{Text: "plain ", Attr: styleDefault},
		{Text: "styled", Attr: styleAccent},
	}}
	wrapped := WrapRow(row, 6)
	if len(wrapped) != 2 {
		t.Fatalf("wrapped = %+v", wrapped)
	}
	if textOf(wrapped[0]) != "plain" {
		t.Fatalf("line0 = %q", textOf(wrapped[0]))
	}
	if len(wrapped[1].Segs) != 1 || wrapped[1].Segs[0].Attr != styleAccent || textOf(wrapped[1]) != "styled" {
		t.Fatalf("line1 = %+v", wrapped[1])
	}
}

func TestWrapRowOverlongWord(t *testing.T) {
	row := Row{Segs: []Segment{{Text: "你好世界", Attr: styleDefault}}}
	wrapped := WrapRow(row, 4)
	for i, wr := range wrapped {
		if displayWidth(textOf(wr)) > 4 {
			t.Fatalf("row %d width %d > 4: %q", i, displayWidth(textOf(wr)), textOf(wr))
		}
	}
	if len(wrapped) < 2 {
		t.Fatalf("expected a hard wrap, got %+v", wrapped)
	}
}

func TestWrapRowEmpty(t *testing.T) {
	wrapped := WrapRow(Row{}, 10)
	if len(wrapped) != 1 {
		t.Fatalf("wrapped = %+v", wrapped)
	}
	if wrapped[0].Segs[0].Attr != styleDefault {
		t.Fatalf("empty wrap attr = %+v want styleDefault", wrapped[0].Segs[0].Attr)
	}
}

func TestWrapRowKeepTrailing(t *testing.T) {
	row := Row{Segs: []Segment{{Text: "hello ", Attr: styleDefault}}}
	trimmed := WrapRow(row, 80)
	if len(trimmed) != 1 || textOf(trimmed[0]) != "hello" {
		t.Fatalf("default wrap should trim: %q", textOf(trimmed[0]))
	}
	kept := WrapRowKeepTrailing(row, 80)
	if len(kept) != 1 || textOf(kept[0]) != "hello " {
		t.Fatalf("keep trailing = %q", textOf(kept[0]))
	}
}

func textOf(row Row) string {
	var b strings.Builder
	for _, s := range row.Segs {
		b.WriteString(s.Text)
	}
	return b.String()
}
