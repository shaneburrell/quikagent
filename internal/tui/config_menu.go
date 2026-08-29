package tui

import (
	"fmt"

	"quikagent/internal/config"
)

const (
	configItemConnection = iota
	configItemModel
	configItemRouter
	configItemSidebar
	configItemPath
	configItemCount
)

// configMenu is a Claude-style prefs list overlay.
type configMenu struct {
	idx int
}

func (m *configMenu) move(delta int) {
	m.idx += delta
	if m.idx < 0 {
		m.idx = 0
	}
	if m.idx >= configItemCount {
		m.idx = configItemCount - 1
	}
}

func (m *configMenu) lines(cfg *config.Config) []string {
	router := "off"
	if cfg.Router.Enabled {
		router = "on"
	}
	side := "off"
	if cfg.Sidebar {
		side = "on"
	}
	path, _ := config.Path()
	return []string{
		"Connection…",
		fmt.Sprintf("Model: %s", cfg.Model),
		fmt.Sprintf("Router: %s", router),
		fmt.Sprintf("Sidebar default: %s", side),
		fmt.Sprintf("Config: %s", path),
	}
}

func renderConfigMenu(m *configMenu, cfg *config.Config, width, height int) []Row {
	if width < 20 {
		width = 20
	}
	header := []string{"Settings", "", "↑/↓ move · enter select · esc close", ""}
	items := m.lines(cfg)
	lines := append(header, items...)
	out := make([]Row, height)
	itemStart := len(header)
	for y := range height {
		text := ""
		attr := styleDim
		if y < len(lines) {
			text = lines[y]
			attr = styleDefault
			if y == 0 {
				attr = styleAccent.withBold().withBG(colSubtle)
				out[y] = Row{Segs: []Segment{{Text: padClip(text, width), Attr: attr}}}
				continue
			}
			itemIdx := y - itemStart
			if itemIdx >= 0 && itemIdx < configItemCount {
				prefix := "  "
				if itemIdx == m.idx {
					prefix = "> "
					attr = styleDefault.withBold()
				}
				text = prefix + text
			}
		}
		out[y] = Row{Segs: []Segment{{Text: padClip(text, width), Attr: attr}}}
	}
	return out
}

// configAction is what the app should do after a config menu key.
type configAction int

const (
	configActNone configAction = iota
	configActClose
	configActOpenSetup
	configActEditModel
	configActToggleRouter
	configActToggleSidebar
	configActShowPath
)

func (m *configMenu) handleKey(k Key) configAction {
	if k.Kind == KeyMouse {
		return configActNone
	}
	switch {
	case k.is(KeyEsc) || (k.Kind == KeyCtrl && (k.Ctrl == 'c' || k.Ctrl == 'q')):
		return configActClose
	case k.is(KeyUp), k.is(KeyShiftUp):
		m.move(-1)
	case k.is(KeyDown), k.is(KeyTab), k.is(KeyShiftDown):
		m.move(1)
	case k.is(KeyEnter):
		switch m.idx {
		case configItemConnection:
			return configActOpenSetup
		case configItemModel:
			return configActEditModel
		case configItemRouter:
			return configActToggleRouter
		case configItemSidebar:
			return configActToggleSidebar
		case configItemPath:
			return configActShowPath
		}
	}
	return configActNone
}
