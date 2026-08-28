package tui

import (
	"strings"
	"unicode/utf8"

	"quikagent/internal/config"
)

const modelPickAuto = "auto"

// modelPicker is the /models overlay state.
type modelPicker struct {
	filter   string
	idx      int
	items    []string // first may be "auto"; rest are model IDs
	showAuto bool
	current  string
	autoMode bool // router currently active
	pinned   bool
}

func newModelPicker(models []string, showAuto bool, current string, autoMode, pinned bool) *modelPicker {
	items := make([]string, 0, len(models)+1)
	if showAuto {
		items = append(items, modelPickAuto)
	}
	items = append(items, models...)
	p := &modelPicker{
		items:    items,
		showAuto: showAuto,
		current:  current,
		autoMode: autoMode,
		pinned:   pinned,
	}
	// Focus current selection.
	want := current
	if autoMode {
		want = modelPickAuto
	}
	for i, id := range p.items {
		if id == want {
			p.idx = i
			break
		}
	}
	return p
}

func (p *modelPicker) filtered() []string {
	q := strings.ToLower(strings.TrimSpace(p.filter))
	if q == "" {
		return p.items
	}
	var out []string
	for _, id := range p.items {
		label := id
		if id == modelPickAuto {
			label = "auto arch-router"
		}
		if strings.Contains(strings.ToLower(label), q) || strings.Contains(strings.ToLower(id), q) {
			out = append(out, id)
		}
	}
	return out
}

func (p *modelPicker) move(delta int) {
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

func (p *modelPicker) selected() (string, bool) {
	items := p.filtered()
	if len(items) == 0 || p.idx < 0 || p.idx >= len(items) {
		return "", false
	}
	return items[p.idx], true
}

type modelPickAction int

const (
	modelPickNone modelPickAction = iota
	modelPickClose
	modelPickSelect
)

func (p *modelPicker) handleKey(k Key) modelPickAction {
	if k.Kind == KeyMouse {
		return modelPickNone
	}
	switch {
	case k.is(KeyEsc) || (k.Kind == KeyCtrl && k.Ctrl == 'c'):
		return modelPickClose
	case k.is(KeyUp), k.is(KeyShiftUp):
		p.move(-1)
	case k.is(KeyDown), k.is(KeyTab), k.is(KeyShiftDown):
		p.move(1)
	case k.is(KeyEnter):
		return modelPickSelect
	case k.is(KeyBackspace):
		if p.filter != "" {
			_, size := utf8.DecodeLastRuneInString(p.filter)
			p.filter = p.filter[:len(p.filter)-size]
			p.idx = 0
		}
	case k.Kind == KeyRune:
		p.filter += string(k.Rune)
		p.idx = 0
	case k.Kind == KeyPaste:
		p.filter += k.Text
		p.idx = 0
	}
	return modelPickNone
}

func renderModelPicker(p *modelPicker, width, height int) []Row {
	items := p.filtered()
	lines := make([]string, 0, len(items))
	for i, id := range items {
		mark := "  "
		if i == p.idx {
			mark = "> "
		}
		label := id
		if id == modelPickAuto {
			label = "auto (Arch-Router)"
		}
		suffix := ""
		if id == modelPickAuto && p.autoMode {
			suffix = "  *"
		} else if id != modelPickAuto && id == p.current && p.pinned {
			suffix = "  *pin"
		} else if id != modelPickAuto && id == p.current && !p.autoMode {
			suffix = "  *"
		}
		lines = append(lines, mark+label+suffix)
	}
	return renderOverlayList(overlayListOpts{
		Title:    "Models",
		Filter:   p.filter,
		ShowFilt: true,
		Items:    lines,
		Idx:      p.idx,
		Hint:     "type to filter · ↑/↓ · enter select · esc close",
	}, width, height)
}

// FavoriteModels returns the F2 cycle list: auto (if routes exist) + current + route targets.
func FavoriteModels(cfg *config.Config, current string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	if cfg != nil && len(cfg.Router.Routes) > 0 {
		add(modelPickAuto)
	}
	add(current)
	if cfg != nil && cfg.Router.Routes != nil {
		for _, name := range []string{"nano", "coder", "qwen", "other"} {
			if t, ok := cfg.Router.Routes[name]; ok {
				add(t.Model)
			}
		}
		for _, t := range cfg.Router.Routes {
			add(t.Model)
		}
	}
	return out
}
