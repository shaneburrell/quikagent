package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"quikagent/internal/session"
	"quikagent/internal/tools"
)

const (
	defaultSideW      = 30
	minSideW          = 24
	maxSideW          = 36
	minMainForSide    = 100
	sidebarSessionCap = 10
)

// SidebarData is everything the right sidebar needs to render.
type SidebarData struct {
	SessionID, Preview, Model, Route, Workdir string
	ModelMode                                 string // "auto" or "pin"
	ToolsMode                                 string // "plan" or "build"
	RouteMap                                  []string
	PromptTokens, CompletionTokens, MsgCount  int
	MCP                                       []string
	Modified                                  []string
	Sessions                                  []session.Info
	Todos                                     []tools.TodoItem
	Approve                                   string
	ScrollHint                                string
}

// SideWidth returns the sidebar column width for raw terminal columns
// (tput cols). Returns 0 when the terminal is narrower than 100 cols.
func SideWidth(termWidth int) int {
	if termWidth < minMainForSide {
		return 0
	}
	w := defaultSideW
	if w > maxSideW {
		w = maxSideW
	}
	if w < minSideW {
		w = minSideW
	}
	if termWidth-w-1 < 40 {
		return 0
	}
	return w
}

// RenderSidebar draws a sidebar column of exactly height rows, each
// truncated/padded to width cells. scroll skips that many logical lines
// from the top of the full sidebar content.
func RenderSidebar(d SidebarData, width, height, scroll int) []Row {
	if width < 1 || height < 1 {
		return nil
	}
	lines := SidebarLines(d, width)
	maxScroll := len(lines) - height
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}

	out := make([]Row, height)
	for y := range height {
		text := strings.Repeat(" ", width)
		attr := styleDim
		li := scroll + y
		if li < len(lines) {
			text = padSide(lines[li], width)
			if isSideHeader(lines[li]) {
				attr = styleAccent.withBold()
			} else if isSideRule(lines[li]) {
				attr = styleDim
			}
		}
		out[y] = Row{Segs: []Segment{{Text: text, Attr: attr}}}
	}
	return out
}

// SidebarLines builds the full logical content of the sidebar (unclipped
// by viewport height). Used for scrolling and hit-test max clamp.
func SidebarLines(d SidebarData, width int) []string {
	if width < 1 {
		return nil
	}
	var lines []string
	add := func(s string) { lines = append(lines, s) }
	addHeader := func(title string) {
		if len(lines) > 0 {
			add("")
		}
		add(title)
		rule := strings.Repeat("─", width-1)
		if len(rule) > 0 {
			add(rule)
		}
	}

	addHeader("SESSION")
	if d.SessionID != "" {
		add(" " + clipSide(shortID(d.SessionID), width-1))
	}
	if d.Preview != "" {
		add(" " + clipSide(d.Preview, width-1))
	}

	addHeader("MODEL")
	add(" " + clipSide(d.Model, width-1))
	if d.ToolsMode != "" {
		add(" tools: " + clipSide(d.ToolsMode, width-8))
	}
	if d.ModelMode != "" {
		add(" mode: " + clipSide(d.ModelMode, width-7))
	}
	if d.Route != "" {
		add(" route: " + clipSide(d.Route, width-8))
	}
	for _, line := range d.RouteMap {
		add(" " + clipSide(line, width-1))
	}

	addHeader("CONTEXT")
	add(fmt.Sprintf(" ↑%d ↓%d", d.PromptTokens, d.CompletionTokens))
	add(fmt.Sprintf(" msgs %d", d.MsgCount))
	if d.MsgCount >= 36 {
		add(" /compact?")
	}

	addHeader("MCP")
	if len(d.MCP) == 0 {
		add(" none")
	} else {
		for _, m := range d.MCP {
			add(" • " + clipSide(m, width-3))
		}
	}

	if len(d.Todos) > 0 {
		addHeader("TODOS")
		for _, td := range d.Todos {
			mark := "○"
			switch td.Status {
			case "in_progress":
				mark = "◐"
			case "completed":
				mark = "●"
			case "cancelled":
				mark = "✗"
			}
			add(" " + mark + " " + clipSide(td.Content, width-4))
		}
	}

	addHeader("MODIFIED")
	if len(d.Modified) == 0 {
		add(" clean")
	} else {
		for _, m := range d.Modified {
			add(" " + clipSide(m, width-1))
		}
	}

	addHeader("SESSIONS")
	if len(d.Sessions) == 0 {
		add(" none")
	} else {
		list := d.Sessions
		extra := 0
		if len(list) > sidebarSessionCap {
			extra = len(list) - sidebarSessionCap
			list = list[len(list)-sidebarSessionCap:]
		}
		if extra > 0 {
			add(fmt.Sprintf(" %d more…", extra))
		}
		for _, s := range list {
			mark := " "
			if d.SessionID != "" && s.ID == d.SessionID {
				mark = "*"
			}
			rs := []rune(s.ID)
			id := s.ID
			if len(rs) > 12 {
				id = string(rs[:8]) + "…"
			}
			add(mark + " " + clipSide(id, width-2))
		}
	}

	if d.Approve != "" {
		addHeader("APPROVE")
		add(" " + clipSide(d.Approve, width-1))
		add(" y/n")
	}

	if d.ScrollHint != "" {
		addHeader("SCROLL")
		add(" " + clipSide(d.ScrollHint, width-1))
	}

	addHeader("PATH")
	path := d.Workdir
	if i := strings.LastIndex(path, "/"); i >= 0 && i < len(path)-1 {
		path = path[i+1:]
	}
	add(" " + clipSide(path, width-1))
	return lines
}

func isSideHeader(s string) bool {
	switch s {
	case "SESSION", "MODEL", "CONTEXT", "MCP", "TODOS", "MODIFIED", "SESSIONS", "APPROVE", "SCROLL", "PATH":
		return true
	}
	return false
}

func isSideRule(s string) bool {
	return strings.Trim(s, "─") == "" && strings.Contains(s, "─")
}

func clipSide(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if max < 1 {
		return ""
	}
	if displayWidth(s) <= max {
		return s
	}
	return trimDisplay(s, max)
}

func padSide(s string, width int) string {
	s = clipSide(s, width)
	pad := width - displayWidth(s)
	if pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// FetchGitStatus returns short git status lines for workdir (nil if not a repo).
func FetchGitStatus(workdir string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--short")
	cmd.Dir = workdir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// MCPNameList returns a sorted copy of MCP server names.
func MCPNameList(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}
