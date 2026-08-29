package tui

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	maxToolOutputChars = 6000
	maxToolOutputLines = 60
	maxInputRows       = 8
)

type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockThinking
	blockTool
	blockError
	blockNote
	blockWait
)

// block is one unit of the conversation transcript.
type block struct {
	kind      blockKind
	text      string // user / assistant / thinking / error
	name      string // tool
	args      string // tool arguments (raw JSON)
	out       string // tool output
	done      bool
	failed    bool
	collapsed bool
}

// Model accumulates transcript blocks and renders them to rows.
type Model struct {
	blocks  []block
	width   int
	spinner int
}

// NewModel builds an empty transcript.
func NewModel() *Model { return &Model{width: 80} }

// SetWidth updates the wrap width.
func (m *Model) SetWidth(w int) { m.width = w }

// User records a user message.
func (m *Model) User(text string) {
	m.blocks = append(m.blocks, block{kind: blockUser, text: text})
}

// Text appends an assistant delta, continuing the current assistant
// block when possible.
func (m *Model) Text(delta string) {
	m.clearWait()
	m.collapseFinishedThinking()
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == blockAssistant {
		m.blocks[n-1].text += delta
		return
	}
	m.blocks = append(m.blocks, block{kind: blockAssistant, text: delta})
}

// Thinking appends a reasoning delta.
func (m *Model) Thinking(delta string) {
	m.clearWait()
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == blockThinking {
		m.blocks[n-1].text += delta
		return
	}
	m.blocks = append(m.blocks, block{kind: blockThinking, text: delta})
}

// ToolStart records a tool that is about to run.
func (m *Model) ToolStart(name, args string) {
	m.clearWait()
	m.collapseFinishedThinking()
	m.blocks = append(m.blocks, block{kind: blockTool, name: name, args: args})
}

// Status upserts a tool-like wait card for a pre-stream phase (waiting, routing, compacting).
func (m *Model) Status(name string) {
	if name == "" {
		name = "waiting"
	}
	m.clearWait()
	m.blocks = append(m.blocks, block{kind: blockWait, name: name})
}

// clearWait removes any in-progress wait cards so a spinner cannot stick.
func (m *Model) clearWait() {
	if len(m.blocks) == 0 {
		return
	}
	out := m.blocks[:0]
	for _, b := range m.blocks {
		if b.kind != blockWait {
			out = append(out, b)
		}
	}
	m.blocks = out
}

// ToolDone finishes the most recent unfinished tool block. If no open
// tool matches name, the most recent unmatched ToolStart is closed
// anyway so a spinner cannot stick after a name mismatch.
func (m *Model) ToolDone(name, out string) {
	finish := func(b *block) {
		b.out = out
		b.done = true
		b.failed = strings.HasPrefix(out, "Error:")
		b.collapsed = !b.failed
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b := &m.blocks[i]
		if b.kind == blockTool && !b.done && b.name == name {
			finish(b)
			return
		}
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b := &m.blocks[i]
		if b.kind == blockTool && !b.done {
			finish(b)
			return
		}
	}
}

// closeOpenTools marks any still-running tool cards done (used on replay).
func (m *Model) closeOpenTools() {
	for i := range m.blocks {
		if m.blocks[i].kind == blockTool && !m.blocks[i].done {
			m.blocks[i].done = true
			m.blocks[i].collapsed = true
		}
	}
}

// Error records a turn-level error.
func (m *Model) Error(text string) {
	m.clearWait()
	m.blocks = append(m.blocks, block{kind: blockError, text: text})
}

// Note records a short dim annotation (mode change, command result).
func (m *Model) Note(text string) {
	m.blocks = append(m.blocks, block{kind: blockNote, text: text})
}

// AdvanceSpinner moves the spinner frame (called on idle ticks while busy).
func (m *Model) AdvanceSpinner() {
	m.spinner = (m.spinner + 1) % len(spinnerFrames)
}

// Len reports the number of blocks.
func (m *Model) Len() int { return len(m.blocks) }

// Reset empties the transcript.
func (m *Model) Reset() { m.blocks = nil }

// Rows renders the whole transcript wrapped to the model width.
func (m *Model) Rows() []Row {
	var out []Row
	for i, b := range m.blocks {
		if i > 0 {
			out = append(out, Row{})
		}
		switch b.kind {
		case blockUser:
			out = append(out, m.userRows(b)...)
		case blockAssistant:
			out = append(out, RenderMarkdown(b.text, m.width)...)
		case blockThinking:
			out = append(out, m.thinkingRows(b)...)
		case blockTool:
			out = append(out, m.toolRows(b)...)
		case blockWait:
			out = append(out, m.waitRows(b)...)
		case blockError:
			out = append(out, m.errorRows(b)...)
		case blockNote:
			for _, line := range strings.Split(b.text, "\n") {
				out = append(out, WrapRow(Row{Segs: []Segment{{Text: line, Attr: styleNote}}}, m.width)...)
			}
		}
	}
	return out
}

func (m *Model) userRows(b block) []Row {
	var out []Row
	for _, line := range strings.Split(b.text, "\n") {
		first := true
		for _, wr := range WrapRow(Row{Segs: []Segment{{Text: line, Attr: styleDefault}}}, m.width-2) {
			prefix := "  "
			if first {
				prefix = "❯ "
			}
			wr.Segs = append([]Segment{{Text: prefix, Attr: styleAccent}}, wr.Segs...)
			out = append(out, wr)
			first = false
		}
	}
	return out
}

func (m *Model) thinkingRows(b block) []Row {
	lines := strings.Split(strings.TrimRight(b.text, "\n"), "\n")
	if b.collapsed {
		n := len(lines)
		if strings.TrimSpace(b.text) == "" {
			n = 0
		}
		return []Row{{Segs: []Segment{{Text: fmt.Sprintf("  thinking · %d lines", n), Attr: styleDim.withItalic()}}}}
	}
	var out []Row
	out = append(out, Row{Segs: []Segment{{Text: "  thinking", Attr: styleDim.withItalic()}}})
	for _, line := range lines {
		out = append(out, WrapRow(Row{Segs: []Segment{{Text: "  " + line, Attr: styleDimItalic}}}, m.width)...)
	}
	return out
}

func (m *Model) collapseFinishedThinking() {
	for i := range m.blocks {
		if m.blocks[i].kind == blockThinking {
			m.blocks[i].collapsed = true
		}
	}
}

func (m *Model) toolRows(b block) []Row {
	var out []Row
	card := styleDefault.withBG(colSubtle)
	var head []Segment
	head = append(head, Segment{Text: "│ ", Attr: styleDim.withBG(colSubtle)})
	switch {
	case !b.done:
		head = append(head, Segment{Text: string(spinnerFrames[m.spinner]) + " ", Attr: styleAccent.withBG(colSubtle)})
	case b.failed:
		head = append(head, Segment{Text: "✗ ", Attr: styleRed.withBG(colSubtle)})
	default:
		head = append(head, Segment{Text: "✓ ", Attr: styleGreen.withBG(colSubtle)})
	}
	head = append(head, Segment{Text: b.name, Attr: styleMagenta.withBold().withBG(colSubtle)})
	if s := argSummary(b.args); s != "" {
		max := m.width - 10
		if max < 1 {
			max = 1
		}
		if displayWidth(s) > max {
			s = trimDisplay(s, max)
		}
		head = append(head, Segment{Text: "  " + s, Attr: styleDim.withBG(colSubtle)})
	}
	// Pad header to width for card look.
	hw := 0
	for _, s := range head {
		hw += displayWidth(s.Text)
	}
	if hw < m.width {
		head = append(head, Segment{Text: strings.Repeat(" ", m.width-hw), Attr: card})
	}
	out = append(out, Row{Segs: head})

	if b.done && b.collapsed {
		return out
	}
	if b.done {
		outLines, truncated := clipLines(b.out, maxToolOutputLines)
		for _, l := range outLines {
			line := "│ " + l
			segs := []Segment{{Text: line, Attr: styleDim.withBG(colSubtle)}}
			lw := displayWidth(line)
			if lw < m.width {
				segs = append(segs, Segment{Text: strings.Repeat(" ", m.width-lw), Attr: card})
			}
			out = append(out, WrapRow(Row{Segs: segs}, m.width)...)
		}
		if truncated {
			pad := m.width - 3
			if pad < 0 {
				pad = 0
			}
			out = append(out, Row{Segs: []Segment{
				{Text: "│ …", Attr: styleDim.withBG(colSubtle)},
				{Text: strings.Repeat(" ", pad), Attr: card},
			}})
		}
	}
	return out
}

func (m *Model) waitRows(b block) []Row {
	card := styleDefault.withBG(colSubtle)
	var head []Segment
	head = append(head, Segment{Text: "│ ", Attr: styleDim.withBG(colSubtle)})
	head = append(head, Segment{Text: string(spinnerFrames[m.spinner]) + " ", Attr: styleAccent.withBG(colSubtle)})
	head = append(head, Segment{Text: b.name, Attr: styleMagenta.withBold().withBG(colSubtle)})
	hw := 0
	for _, s := range head {
		hw += displayWidth(s.Text)
	}
	if hw < m.width {
		head = append(head, Segment{Text: strings.Repeat(" ", m.width-hw), Attr: card})
	}
	return []Row{{Segs: head}}
}

func (m *Model) errorRows(b block) []Row {
	var out []Row
	for _, line := range strings.Split(b.text, "\n") {
		out = append(out, WrapRow(Row{Segs: []Segment{{Text: "✗ " + line, Attr: styleRed}}}, m.width)...)
	}
	return out
}

// argSummary extracts a one-line human summary from raw tool JSON args.
func argSummary(args string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "path", "pattern", "query", "description"} {
		v, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			return strings.TrimSpace(s)
		}
	}
	if v, ok := m["patch"]; ok {
		var patch string
		if json.Unmarshal(v, &patch) == nil {
			for _, line := range strings.Split(patch, "\n") {
				line = strings.TrimSpace(line)
				for _, p := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
					if strings.HasPrefix(line, p) {
						return strings.TrimSpace(strings.TrimPrefix(line, p))
					}
				}
			}
		}
	}
	return ""
}

// clipLines caps output at n lines, reporting whether it was cut.
func clipLines(s string, n int) ([]string, bool) {
	truncated := false
	if len(s) > maxToolOutputChars {
		r := []rune(s)
		if len(r) > maxToolOutputChars {
			r = r[:maxToolOutputChars]
		} // rune cap ≈ char cap
		s = string(r)
		truncated = true
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n]
		truncated = true
	}
	return lines, truncated
}

// StatusOpts configures a width-aware status bar.
type StatusOpts struct {
	Mode, Model, Route, SessionID string
	Auto, Pin                     bool
	Prompt, Completion            int
	Busy                          bool
	Phase                         string
	Spinner                       int
	Width                         int
	ScrollHint                    string
	HasNew                        bool
}

// StatusRow renders the bottom status bar, truncating by priority when Width > 0.
func StatusRow(modeName, model, sessionID string, prompt, completion int, busy bool, spinner int) Row {
	return StatusRowOpts(StatusOpts{
		Mode: modeName, Model: model, SessionID: sessionID,
		Prompt: prompt, Completion: completion, Busy: busy, Spinner: spinner,
	})
}

// statusPart is one status-bar segment with a drop priority.
type statusPart struct {
	text string
	attr Attr
	pri  int // lower = keep longer
}

// StatusRowOpts renders a status bar with optional route and width truncation.
func StatusRowOpts(o StatusOpts) Row {
	hint := "tab plan/build · f2 model · ctrl+p · wheel · ctrl+q"
	if o.Busy {
		phase := o.Phase
		if phase == "" {
			phase = "working"
		}
		hint = fmt.Sprintf("%s %s…", string(spinnerFrames[o.Spinner%len(spinnerFrames)]), phase)
	}
	if o.HasNew && !o.Busy {
		hint = "↓ new · " + hint
	}
	if o.ScrollHint != "" && !o.Busy {
		hint = o.ScrollHint + " · " + hint
	}

	modeAttr := styleGreen.withBold()
	if o.Mode == "plan" {
		modeAttr = styleYellow.withBold()
	}
	modeAttr = modeAttr.withBG(colSubtle)

	modelPrefix := ""
	if o.Auto {
		if o.Route != "" {
			modelPrefix = "auto·" + o.Route + "→ "
		} else {
			modelPrefix = "auto· "
		}
	} else if o.Pin {
		modelPrefix = "pin· "
	}

	parts := []statusPart{
		{text: "[" + o.Mode + "] ", attr: modeAttr, pri: 0},
		{text: "│ ", attr: styleDim.withBG(colSubtle), pri: 0},
		{text: modelPrefix + o.Model + "  ", attr: styleDim.withBG(colSubtle), pri: 1},
	}
	if o.Prompt > 0 || o.Completion > 0 {
		parts = append(parts, statusPart{text: fmt.Sprintf("↑%d ↓%d  ", o.Prompt, o.Completion), attr: styleDim.withBG(colSubtle), pri: 3})
	}
	if o.SessionID != "" {
		parts = append(parts, statusPart{text: o.SessionID + "  ", attr: styleDim.withBG(colSubtle), pri: 4})
	}
	parts = append(parts, statusPart{text: hint, attr: styleDim.withBG(colSubtle), pri: 5})

	if o.Width > 0 {
		parts = truncateStatusParts(parts, o.Width)
	}
	segs := make([]Segment, len(parts))
	for i, p := range parts {
		segs[i] = Segment{Text: p.text, Attr: p.attr}
	}
	// Pad status to full width with subtle bg.
	if o.Width > 0 {
		w := 0
		for _, s := range segs {
			w += displayWidth(s.Text)
		}
		if w < o.Width {
			segs = append(segs, Segment{Text: strings.Repeat(" ", o.Width-w), Attr: styleDim.withBG(colSubtle)})
		}
	}
	return Row{Segs: segs}
}

func truncateStatusParts(parts []statusPart, width int) []statusPart {
	measure := func(ps []statusPart) int {
		n := 0
		for _, p := range ps {
			n += displayWidth(p.text)
		}
		return n
	}
	if measure(parts) <= width {
		return parts
	}
	out := append([]statusPart(nil), parts...)
	for maxPri := 5; maxPri >= 2 && measure(out) > width; maxPri-- {
		filtered := out[:0]
		for _, p := range out {
			if p.pri == maxPri && len(filtered) > 0 {
				continue
			}
			filtered = append(filtered, p)
		}
		out = filtered
	}
	for measure(out) > width && len(out) > 0 {
		last := &out[len(out)-1]
		w := displayWidth(last.text)
		over := measure(out) - width
		if over >= w {
			out = out[:len(out)-1]
			continue
		}
		last.text = trimDisplay(last.text, w-over)
	}
	return out
}

func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func trimDisplay(s string, max int) string {
	if max <= 1 {
		return "…"
	}
	n := 0
	for i, r := range s {
		rw := runeWidth(r)
		if n+rw > max-1 {
			return s[:i] + "…"
		}
		n += rw
	}
	return s
}

// SeparatorRow is a dim horizontal rule for the transcript/input boundary.
func SeparatorRow(width int) Row {
	if width < 1 {
		width = 1
	}
	return Row{Segs: []Segment{{Text: strings.Repeat("─", width), Attr: styleDim}}}
}

// CursorScreenRow maps input-relative cursor Y onto the absolute screen row
// (1-based ANSI), given the transcript viewport height.
func CursorScreenRow(histH, cy int) int {
	if histH < 0 {
		histH = 0
	}
	if cy < 0 {
		cy = 0
	}
	return histH + cy + 1
}
