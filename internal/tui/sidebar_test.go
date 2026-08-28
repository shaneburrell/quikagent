package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"quikagent/internal/session"
)

func TestCursorScreenRow(t *testing.T) {
	if got := CursorScreenRow(20, 0); got != 21 {
		t.Fatalf("got %d want 21", got)
	}
	if got := CursorScreenRow(20, 2); got != 23 {
		t.Fatalf("got %d want 23", got)
	}
	if got := CursorScreenRow(0, 0); got != 1 {
		t.Fatalf("got %d want 1", got)
	}
}

func TestSeparatorRow(t *testing.T) {
	row := SeparatorRow(10)
	if len(row.Segs) != 1 || displayWidth(row.Segs[0].Text) != 10 {
		t.Fatalf("%+v", row)
	}
}

func TestStatusRowOptsTruncates(t *testing.T) {
	row := StatusRowOpts(StatusOpts{
		Mode: "build", Model: "very-long-model-name-here",
		Route: "nano", SessionID: "aaaaaaaa…bbbb",
		Prompt: 100, Completion: 200, Width: 40,
	})
	var b strings.Builder
	for _, s := range row.Segs {
		b.WriteString(s.Text)
	}
	if displayWidth(b.String()) > 40 {
		t.Fatalf("width %d > 40: %q", displayWidth(b.String()), b.String())
	}
	if !strings.Contains(b.String(), "[build]") {
		t.Fatalf("missing mode: %q", b.String())
	}
}

func TestSideWidth(t *testing.T) {
	if SideWidth(80) != 0 {
		t.Fatal("expected 0 for narrow")
	}
	if w := SideWidth(120); w < minSideW || w > maxSideW {
		t.Fatalf("side=%d", w)
	}
}

func TestRenderSidebar(t *testing.T) {
	rows := RenderSidebar(SidebarData{
		SessionID: "abc", Preview: "hello", Model: "qwen", Route: "nano",
		Workdir: "/tmp/proj", MsgCount: 3, MCP: []string{"demo"},
		Modified: []string{" M main.go"},
		Sessions: []session.Info{{ID: "abc"}, {ID: "def"}},
	}, 28, 40, 0)
	if len(rows) != 40 {
		t.Fatalf("rows=%d", len(rows))
	}
	var joined strings.Builder
	for _, r := range rows {
		for _, s := range r.Segs {
			joined.WriteString(s.Text)
		}
		joined.WriteByte('\n')
	}
	out := joined.String()
	for _, want := range []string{"SESSION", "MODEL", "route: nano", "MCP", "demo", "MODIFIED", "main.go", "PATH", "proj"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	for _, r := range rows {
		w := 0
		for _, s := range r.Segs {
			w += displayWidth(s.Text)
		}
		if w != 28 {
			t.Fatalf("row width %d", w)
		}
	}
}

func TestRenderSidebarScroll(t *testing.T) {
	d := SidebarData{
		SessionID: "abc", Model: "qwen", Workdir: "/tmp/proj",
		Modified: make([]string, 30),
	}
	for i := range d.Modified {
		d.Modified[i] = fmt.Sprintf(" M file%02d.go", i)
	}
	lines := SidebarLines(d, 28)
	if len(lines) < 20 {
		t.Fatalf("expected tall sidebar, got %d lines", len(lines))
	}
	top := RenderSidebar(d, 28, 8, 0)
	scrolled := RenderSidebar(d, 28, 8, 5)
	topText := rowText(top[0])
	scrolledText := rowText(scrolled[0])
	if topText == scrolledText {
		t.Fatalf("scroll did not change first visible row: %q", topText)
	}
	want := padSide(lines[5], 28)
	if scrolledText != want {
		t.Fatalf("first row = %q want %q", scrolledText, want)
	}
}

func TestSidebarSessionStarMatchesFullID(t *testing.T) {
	// SessionID is the full id; the star must match even when the
	// displayed SESSION line is shortened.
	full := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	d := SidebarData{
		SessionID: full, Model: "qwen", Workdir: "/tmp/proj",
		Sessions: []session.Info{{ID: full}, {ID: "other-session-id-zzzz"}},
	}
	lines := SidebarLines(d, 28)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "* aaaaaaaa…") {
		t.Fatalf("expected starred short id, got:\n%s", joined)
	}
	if strings.Count(joined, "*") != 1 {
		t.Fatalf("want exactly one star:\n%s", joined)
	}
}

func TestSidebarSessionsCap(t *testing.T) {
	var sessions []session.Info
	for i := 0; i < 15; i++ {
		sessions = append(sessions, session.Info{ID: fmt.Sprintf("%d-sess", i)})
	}
	d := SidebarData{SessionID: "14-sess", Model: "m", Workdir: "/p", Sessions: sessions}
	joined := strings.Join(SidebarLines(d, 28), "\n")
	if !strings.Contains(joined, "5 more…") {
		t.Fatalf("expected cap hint:\n%s", joined)
	}
	if strings.Contains(joined, " 0-sess") {
		t.Fatalf("oldest session should be clipped:\n%s", joined)
	}
	if !strings.Contains(joined, "14-sess") {
		t.Fatalf("newest session missing:\n%s", joined)
	}
}

func TestSidebarSessionIDRuneSafe(t *testing.T) {
	id := strings.Repeat("世", 20)
	d := SidebarData{SessionID: "x", Model: "m", Workdir: "/p", Sessions: []session.Info{{ID: id}}}
	joined := strings.Join(SidebarLines(d, 28), "\n")
	if !utf8.ValidString(joined) {
		t.Fatalf("invalid utf8:\n%s", joined)
	}
	if !strings.Contains(joined, string([]rune(id)[:8])+"…") {
		t.Fatalf("expected rune-safe short id:\n%s", joined)
	}
}

func TestPadRowTruncatesOverflow(t *testing.T) {
	row := padRow(Row{Segs: []Segment{{Text: "hello world this is long", Attr: styleDefault}}}, 10)
	w := 0
	var b strings.Builder
	for _, s := range row.Segs {
		w += displayWidth(s.Text)
		b.WriteString(s.Text)
	}
	if w > 10 {
		t.Fatalf("width %d > 10: %q", w, b.String())
	}
	if !strings.Contains(b.String(), "…") {
		t.Fatalf("expected ellipsis, got %q", b.String())
	}
}

func rowText(r Row) string {
	var b strings.Builder
	for _, s := range r.Segs {
		b.WriteString(s.Text)
	}
	return b.String()
}
