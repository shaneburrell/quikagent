package tui

import (
	"strings"
	"testing"
)

func rowTexts(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		var b strings.Builder
		for _, s := range r.Segs {
			b.WriteString(s.Text)
		}
		out[i] = b.String()
	}
	return out
}

func TestModelUserAndAssistant(t *testing.T) {
	m := NewModel()
	m.SetWidth(80)
	m.User("do the thing")
	m.Text("Here is **bold** and `code`.")

	rows := m.Rows()
	txt := rowTexts(rows)
	// Blank separator row between blocks.
	if len(txt) < 2 {
		t.Fatalf("rows = %q", txt)
	}
	user, asst := txt[0], txt[len(txt)-1]
	if !strings.HasPrefix(user, "❯ ") || !strings.Contains(user, "do the thing") {
		t.Fatalf("user row = %q", user)
	}
	if !strings.Contains(asst, "bold") || !strings.Contains(asst, "code") {
		t.Fatalf("assistant row = %q", asst)
	}
}

func TestModelTextMergesDeltas(t *testing.T) {
	m := NewModel()
	m.SetWidth(80)
	m.Text("hel")
	m.Text("lo")
	if n := len(m.blocks); n != 1 {
		t.Fatalf("blocks = %d, want 1", n)
	}
	m.Thinking("hmm")
	m.Text(" world")
	if n := len(m.blocks); n != 3 {
		t.Fatalf("blocks = %d, want 3 (text, thinking, text)", n)
	}
	if m.blocks[0].text != "hello" {
		t.Fatalf("first = %q", m.blocks[0].text)
	}
	if m.blocks[2].text != " world" {
		t.Fatalf("third = %q", m.blocks[2].text)
	}
}

func TestModelToolLifecycle(t *testing.T) {
	m := NewModel()
	m.SetWidth(80)
	m.ToolStart("bash", `{"command":"ls -la"}`)
	rows := m.Rows()
	if head := rowTexts(rows)[0]; !strings.Contains(head, "bash") || !strings.Contains(head, "ls -la") {
		t.Fatalf("running head = %q", head)
	}
	m.ToolDone("bash", "file.go\nmain.go")
	rows = m.Rows()
	txt := rowTexts(rows)
	if !strings.Contains(txt[0], "✓") || !strings.Contains(txt[0], "bash") {
		t.Fatalf("done head = %q", txt[0])
	}
	joined := strings.Join(txt, "\n")
	if strings.Contains(joined, "file.go") {
		t.Fatalf("successful tool should collapse: %q", joined)
	}
	m.ToolStart("write", `{"path":"x"}`)
	m.ToolDone("write", "Error: boom")
	rows = m.Rows()
	joined = strings.Join(rowTexts(rows), "\n")
	if !strings.Contains(joined, "Error: boom") {
		t.Fatalf("failed tool should stay expanded: %q", joined)
	}
}

func TestThinkingCollapsesAfterNextBlock(t *testing.T) {
	m := NewModel()
	m.SetWidth(80)
	m.Thinking("secret plan")
	if joined := strings.Join(rowTexts(m.Rows()), "\n"); !strings.Contains(joined, "secret plan") {
		t.Fatalf("in-progress thinking should expand: %q", joined)
	}
	m.Text("done")
	joined := strings.Join(rowTexts(m.Rows()), "\n")
	if strings.Contains(joined, "secret plan") {
		t.Fatalf("finished thinking should collapse: %q", joined)
	}
	if !strings.Contains(joined, "thinking ·") {
		t.Fatalf("want collapsed header: %q", joined)
	}
}

func TestStatusHasNewHint(t *testing.T) {
	row := StatusRowOpts(StatusOpts{Mode: "build", Model: "m", Width: 80, HasNew: true})
	var b strings.Builder
	for _, s := range row.Segs {
		b.WriteString(s.Text)
	}
	if !strings.Contains(b.String(), "↓ new") || !strings.Contains(b.String(), "tab plan/build") {
		t.Fatalf("%q", b.String())
	}
}

func TestArgSummaryPatchAndQuery(t *testing.T) {
	if got := argSummary(`{"query":"find foo"}`); got != "find foo" {
		t.Fatalf("query = %q", got)
	}
	if got := argSummary(`{"patch":"*** Begin Patch\n*** Update File: main.go\n*** End Patch"}`); got != "main.go" {
		t.Fatalf("patch = %q", got)
	}
}

func TestModelToolErrorFlag(t *testing.T) {
	m := NewModel()
	m.SetWidth(80)
	m.ToolStart("bash", `{"command":"nope"}`)
	m.ToolDone("bash", "Error: exit status 127")
	head := rowTexts(m.Rows())[0]
	if !strings.Contains(head, "✗") {
		t.Fatalf("head = %q", head)
	}
}

func TestModelNoteAndError(t *testing.T) {
	m := NewModel()
	m.SetWidth(80)
	m.Note("mode: plan")
	m.Error("boom")
	txt := rowTexts(m.Rows())
	joined := strings.Join(txt, "\n")
	if !strings.Contains(joined, "mode: plan") || !strings.Contains(joined, "boom") {
		t.Fatalf("rows = %q", txt)
	}
}

func TestModelLongOutputClipped(t *testing.T) {
	m := NewModel()
	m.SetWidth(80)
	m.ToolStart("read", `{"file_path":"big.txt"}`)
	m.ToolDone("read", strings.Repeat("x\n", 500))
	m.blocks[len(m.blocks)-1].collapsed = false
	rows := m.Rows()
	if len(rows) > maxToolOutputLines+3 {
		t.Fatalf("too many rows: %d", len(rows))
	}
	if last := rowTexts(rows)[len(rows)-1]; !strings.Contains(last, "…") {
		t.Fatalf("last row = %q", last)
	}
}

func TestArgSummary(t *testing.T) {
	if got := argSummary(`{"command":"git status"}`); got != "git status" {
		t.Fatalf("got %q", got)
	}
	if got := argSummary(`{"file_path":"/a/b.go","other":1}`); got != "/a/b.go" {
		t.Fatalf("got %q", got)
	}
	if got := argSummary("not json"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestStatusRow(t *testing.T) {
	row := StatusRow("plan", "qwen", "abc…1234", 10, 5, false, 0)
	var b strings.Builder
	for _, s := range row.Segs {
		b.WriteString(s.Text)
	}
	out := b.String()
	for _, want := range []string{"[plan]", "qwen", "abc…1234", "↑10", "↓5", "ctrl+q"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status %q missing %q", out, want)
		}
	}
	row = StatusRow("build", "qwen", "", 0, 0, true, 3)
	var b2 strings.Builder
	for _, s := range row.Segs {
		b2.WriteString(s.Text)
	}
	if !strings.Contains(b2.String(), "working…") {
		t.Fatalf("busy status = %q", b2.String())
	}
}

func TestStatusRowAutoPin(t *testing.T) {
	row := StatusRowOpts(StatusOpts{
		Mode: "build", Model: "nemotron-nano-q4", Route: "nano",
		Auto: true, Width: 80,
	})
	var b strings.Builder
	for _, s := range row.Segs {
		b.WriteString(s.Text)
	}
	out := b.String()
	if !strings.Contains(out, "auto·nano→") || !strings.Contains(out, "nemotron-nano-q4") {
		t.Fatalf("%q", out)
	}
	row = StatusRowOpts(StatusOpts{Mode: "build", Model: "qwen", Pin: true, Width: 60})
	b.Reset()
	for _, s := range row.Segs {
		b.WriteString(s.Text)
	}
	if !strings.Contains(b.String(), "pin·") {
		t.Fatalf("%q", b.String())
	}
}

func TestCursorInLine(t *testing.T) {
	line := "short line"
	r, c := cursorInLine(line, 5, 80)
	if r != 0 || c != 5 {
		t.Fatalf("got (%d,%d)", r, c)
	}
	// Wrapping: "aaaaa bbbbb" at width 6 -> "aaaaa" / "bbbbb"
	line = "aaaaa bbbbb"
	r, c = cursorInLine(line, 2, 6)
	if r != 0 || c != 2 {
		t.Fatalf("got (%d,%d) want (0,2)", r, c)
	}
	r, c = cursorInLine(line, 8, 6)
	if r != 1 || c != 2 {
		t.Fatalf("got (%d,%d) want (1,2)", r, c)
	}
	// Cursor on the wrap space is kept (input wrap does not trim).
	r, c = cursorInLine(line, 6, 6)
	if r != 0 || c != 6 {
		t.Fatalf("got (%d,%d) want (0,6)", r, c)
	}
	r, c = cursorInLine("hello ", 6, 80)
	if r != 0 || c != 6 {
		t.Fatalf("trailing space cursor (%d,%d) want (0,6)", r, c)
	}
	r, c = cursorInLine(" ", 1, 80)
	if r != 0 || c != 1 {
		t.Fatalf("lone space cursor (%d,%d) want (0,1)", r, c)
	}
	// Repeated wrap fragments must not reverse-match the first chunk.
	line = "aaaa aaaa aaaa"
	r, c = cursorInLine(line, 12, 4)
	if r != 2 {
		t.Fatalf("repeated wrap: row %d want 2 (col %d)", r, c)
	}
	// Display columns, not rune index (each CJK ideograph is 2 cells).
	r, c = cursorInLine("日本語", 1, 80)
	if r != 0 || c != 2 {
		t.Fatalf("CJK cursor (%d,%d) want (0,2)", r, c)
	}
}

func TestRenderMarkdownBasics(t *testing.T) {
	rows := RenderMarkdown("# Title\n\n- item one\n- item two\n\ntext with **bold**", 80)
	txt := rowTexts(rows)
	joined := strings.Join(txt, "\n")
	if !strings.Contains(joined, "Title") {
		t.Fatalf("missing title: %q", joined)
	}
	bullets := 0
	for _, l := range txt {
		if strings.HasPrefix(l, "•") {
			bullets++
		}
	}
	if bullets != 2 {
		t.Fatalf("bullets = %d, rows = %q", bullets, txt)
	}
}

func TestRenderMarkdownFence(t *testing.T) {
	rows := RenderMarkdown("before\n```\ncode here\n```\nafter", 80)
	if len(rows) != 5 {
		t.Fatalf("rows = %+v", rowTexts(rows))
	}
	if rows[2].Segs[0].Attr != styleFence || rows[2].Segs[0].Text != "code here" {
		t.Fatalf("fence row = %+v", rows[2])
	}
}

func TestRenderMarkdownLinkQuoteStrike(t *testing.T) {
	rows := RenderMarkdown("> quoted\nsee [docs](https://ex.test) and ~~old~~ café", 80)
	joined := strings.Join(rowTexts(rows), "\n")
	if !strings.Contains(joined, "quoted") || !strings.Contains(joined, "│") {
		t.Fatalf("blockquote missing: %q", joined)
	}
	if !strings.Contains(joined, "docs") || !strings.Contains(joined, "https://ex.test") {
		t.Fatalf("link missing: %q", joined)
	}
	if !strings.Contains(joined, "old") || !strings.Contains(joined, "café") {
		t.Fatalf("strike/unicode missing: %q", joined)
	}
}

func TestToolDoneNameMismatchClosesLatest(t *testing.T) {
	m := NewModel()
	m.SetWidth(80)
	m.ToolStart("bash", `{"command":"ls"}`)
	m.ToolDone("read", "ok")
	if len(m.blocks) != 1 || !m.blocks[0].done {
		t.Fatal("name mismatch should still close the open ToolStart")
	}
	if m.blocks[0].out != "ok" {
		t.Fatalf("out=%q", m.blocks[0].out)
	}
}

func TestArgSummaryTruncatesDisplayWidth(t *testing.T) {
	m := NewModel()
	m.SetWidth(24)
	m.ToolStart("read", `{"path":"`+strings.Repeat("世界", 30)+`"}`)
	w := 0
	for _, s := range m.Rows()[0].Segs {
		w += displayWidth(s.Text)
	}
	if w > 24 {
		t.Fatalf("header width %d > 24", w)
	}
}

func TestIsNumberedMultiDigit(t *testing.T) {
	if !isNumbered("10. item") {
		t.Fatal("10. should be a numbered list")
	}
	if !isNumbered("1. item") {
		t.Fatal("1. should be a numbered list")
	}
	if isNumbered("0. item") {
		t.Fatal("0. should not be a numbered list")
	}
	rows := RenderMarkdown("10. tenth", 80)
	joined := strings.Join(rowTexts(rows), "\n")
	if !strings.Contains(joined, "10.") && !strings.Contains(joined, "tenth") {
		t.Fatalf("numbered 10 missing: %q", joined)
	}
}

func TestParseLinkBalancedParens(t *testing.T) {
	_, url, ok := parseLink([]rune("[docs](http://ex.test/foo_(bar))"))
	if !ok || url != "http://ex.test/foo_(bar)" {
		t.Fatalf("ok=%v url=%q", ok, url)
	}
	rows := RenderMarkdown("see [docs](http://ex.test/foo_(bar))", 80)
	joined := strings.Join(rowTexts(rows), "\n")
	if !strings.Contains(joined, "foo_(bar)") {
		t.Fatalf("balanced paren url missing: %q", joined)
	}
}
