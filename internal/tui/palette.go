package tui

import (
	"strings"
)

// paletteCmd is one Ctrl+P command palette entry.
type paletteCmd struct {
	ID, Label, Hint string
}

// defaultPaletteCommands lists discoverable actions (OpenCode-style).
func defaultPaletteCommands() []paletteCmd {
	return []paletteCmd{
		{ID: "setup", Label: "Setup / Connect", Hint: "edit API key, URL, model"},
		{ID: "config", Label: "Settings", Hint: "/config"},
		{ID: "model", Label: "Pick model", Hint: "/models · f2"},
		{ID: "router", Label: "Toggle router", Hint: "/router"},
		{ID: "sidebar", Label: "Toggle sidebar", Hint: "ctrl+b"},
		{ID: "sessions", Label: "List sessions", Hint: "/sessions"},
		{ID: "resume", Label: "Resume session", Hint: "/resume"},
		{ID: "plan", Label: "Toggle plan/build", Hint: "tab"},
		{ID: "compact", Label: "Compact conversation", Hint: "/compact"},
		{ID: "undo", Label: "Undo last turn", Hint: "/undo"},
		{ID: "redo", Label: "Redo undone turn", Hint: "/redo"},
		{ID: "refresh", Label: "Refresh sidebar", Hint: "/refresh"},
		{ID: "clear", Label: "New session", Hint: "/clear"},
		{ID: "init", Label: "Write AGENTS.md", Hint: "/init"},
		{ID: "help", Label: "Help", Hint: "/help"},
		{ID: "quit", Label: "Quit", Hint: "ctrl+q"},
	}
}

type paletteState struct {
	filter string
	idx    int
	all    []paletteCmd
}

func newPalette() *paletteState {
	return &paletteState{all: defaultPaletteCommands()}
}

func (p *paletteState) filtered() []paletteCmd {
	q := strings.ToLower(strings.TrimSpace(p.filter))
	if q == "" {
		return p.all
	}
	var out []paletteCmd
	for _, c := range p.all {
		hay := strings.ToLower(c.ID + " " + c.Label + " " + c.Hint)
		if strings.Contains(hay, q) {
			out = append(out, c)
		}
	}
	return out
}

func (p *paletteState) move(delta int) {
	items := p.filtered()
	if len(items) == 0 {
		p.idx = 0
		return
	}
	p.idx += delta
	if p.idx < 0 {
		p.idx = 0
	}
	if p.idx >= len(items) {
		p.idx = len(items) - 1
	}
}

func (p *paletteState) selected() (paletteCmd, bool) {
	items := p.filtered()
	if len(items) == 0 || p.idx < 0 || p.idx >= len(items) {
		return paletteCmd{}, false
	}
	return items[p.idx], true
}

type paletteAction int

const (
	paletteActNone paletteAction = iota
	paletteActClose
	paletteActRun
)

func (p *paletteState) handleKey(k Key) paletteAction {
	if k.Kind == KeyMouse {
		return paletteActNone
	}
	switch {
	case k.is(KeyEsc) || (k.Kind == KeyCtrl && k.Ctrl == 'c'):
		return paletteActClose
	case k.is(KeyUp), k.is(KeyShiftUp):
		p.move(-1)
	case k.is(KeyDown), k.is(KeyTab), k.is(KeyShiftDown):
		p.move(1)
	case k.is(KeyEnter):
		return paletteActRun
	case k.is(KeyBackspace):
		if p.filter != "" {
			r := []rune(p.filter)
			p.filter = string(r[:len(r)-1])
			p.idx = 0
		}
	case k.Kind == KeyRune:
		p.filter += string(k.Rune)
		p.idx = 0
	case k.Kind == KeyPaste:
		p.filter += k.Text
		p.idx = 0
	}
	return paletteActNone
}

func renderPalette(p *paletteState, width, height int) []Row {
	items := p.filtered()
	lines := make([]string, 0, len(items))
	for i, c := range items {
		mark := "  "
		if i == p.idx {
			mark = "> "
		}
		lines = append(lines, mark+c.Label+"  "+c.Hint)
	}
	return renderOverlayList(overlayListOpts{
		Title:    "Commands",
		Filter:   p.filter,
		ShowFilt: true,
		Items:    lines,
		Idx:      p.idx,
		Hint:     "type to filter · ↑/↓ · enter run · esc close",
	}, width, height)
}
