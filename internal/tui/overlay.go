package tui

import "strings"

// padClip truncates or right-pads s to exactly width display cells.
func padClip(s string, width int) string {
	if width < 1 {
		return ""
	}
	if displayWidth(s) > width {
		return trimDisplay(s, width)
	}
	pad := width - displayWidth(s)
	if pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// overlayListOpts describes a titled, optionally filtered list overlay.
type overlayListOpts struct {
	Title    string
	Filter   string // shown as "> filter▌"; empty still shows the filter row
	ShowFilt bool
	Items    []string
	Idx      int
	Hint     string
}

// renderOverlayList draws a full-screen overlay that viewport-scrolls so
// the selected item stays visible.
func renderOverlayList(o overlayListOpts, width, height int) []Row {
	if width < 20 {
		width = 20
	}
	if height < 1 {
		return nil
	}
	header := []string{o.Title}
	if o.ShowFilt {
		header = append(header, "> "+o.Filter+"▌")
	}
	header = append(header, "")
	footer := []string{""}
	if o.Hint != "" {
		footer = append(footer, o.Hint)
	}

	items := o.Items
	if len(items) == 0 {
		items = []string{"  (no matches)"}
	}
	bodyH := height - len(header) - len(footer)
	if bodyH < 1 {
		bodyH = 1
	}
	start := 0
	if o.Idx >= 0 && len(o.Items) > 0 {
		if o.Idx >= start+bodyH {
			start = o.Idx - bodyH + 1
		}
		if o.Idx < start {
			start = o.Idx
		}
	}
	if start < 0 {
		start = 0
	}
	maxStart := len(items) - bodyH
	if maxStart < 0 {
		maxStart = 0
	}
	if start > maxStart {
		start = maxStart
	}

	lines := make([]string, 0, height)
	lines = append(lines, header...)
	end := start + bodyH
	if end > len(items) {
		end = len(items)
	}
	for i := start; i < end; i++ {
		lines = append(lines, items[i])
	}
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	lines = append(lines, footer...)
	if len(lines) > height {
		lines = lines[:height]
	}

	out := make([]Row, height)
	for y := range height {
		text := ""
		if y < len(lines) {
			text = lines[y]
		}
		attr := styleDim
		if y == 0 {
			attr = styleAccent.withBold().withBG(colSubtle)
		} else if y > 0 && y < len(header) {
			attr = styleDefault
		} else if strings.HasPrefix(text, "> ") && y >= len(header) {
			attr = styleDefault.withBold()
		} else if text != "" && y < height-len(footer) {
			attr = styleDefault
		}
		out[y] = Row{Segs: []Segment{{Text: padClip(text, width), Attr: attr}}}
	}
	return out
}
