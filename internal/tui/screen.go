package tui

import (
	"fmt"
	"io"
	"strings"
)

// Segment is a run of text sharing one style.
type Segment struct {
	Text string
	Attr Attr
}

// Row is a horizontal run of segments.
type Row struct {
	Segs []Segment
}

// cell is one normalized screen position.
type cell struct {
	r rune
	a Attr
}

// Renderer diffs successive frames and emits the minimal ANSI updates:
// changed cells are written in contiguous runs, one cursor move per run.
type Renderer struct {
	w, h    int
	prev    []cell
	attr    Attr
	attrSet bool
	out     io.Writer
}

// NewRenderer builds a Renderer writing ANSI to out.
func NewRenderer(out io.Writer) *Renderer {
	return &Renderer{out: out}
}

// Resize prepares the renderer for a new terminal size. The next Render
// rewrites the whole screen.
func (r *Renderer) Resize(w, h int) {
	r.w, r.h = w, h
	r.prev = make([]cell, w*h)
	r.attrSet = false
}

// Render writes the frame, updating only cells that changed.
func (r *Renderer) Render(rows []Row) {
	if r.w <= 0 || r.h <= 0 {
		return
	}
	grid := r.normalize(rows)

	var buf strings.Builder
	for y := range r.h {
		off := y * r.w
		x := 0
		for x < r.w {
			if cellEqual(grid[off+x], r.prev[off+x]) {
				x++
				continue
			}
			fmt.Fprintf(&buf, "\x1b[%d;%dH", y+1, x+1)
			// Write the whole changed run in one go.
			for x < r.w && !cellEqual(grid[off+x], r.prev[off+x]) {
				c := grid[off+x]
				if !r.attrSet || c.a != r.attr {
					buf.WriteString(c.a.sgr())
					r.attr = c.a
					r.attrSet = true
				}
				buf.WriteRune(c.r)
				r.prev[off+x] = c
				x++
				if isWide(c.r) && x < r.w {
					// Mark the covered cell to prevent flicker
					r.prev[off+x] = c
					x++ // glyph covers the next column
				}
			}
		}
	}
	if buf.Len() > 0 {
		_, _ = r.out.Write([]byte(buf.String()))
	}
}

// normalize maps rows to a full w*h grid of cells.
func (r *Renderer) normalize(rows []Row) []cell {
	bg := styleDefault.withBG(colBg)
	grid := make([]cell, r.w*r.h)
	for i := range grid {
		grid[i] = cell{r: ' ', a: bg}
	}
	for y := range r.h {
		var row Row
		if y < len(rows) {
			row = rows[y]
		}
		x := 0
		for _, seg := range row.Segs {
			for _, ch := range seg.Text {
				if x >= r.w {
					break
				}
				grid[y*r.w+x] = cell{r: ch, a: seg.Attr}
				x++
				if isWide(ch) && x < r.w {
					// Mark the covered cell to prevent flicker
					grid[y*r.w+x] = cell{r: ch, a: seg.Attr}
					x++
				}
			}
		}
	}
	return grid
}

func cellEqual(a, b cell) bool { return a.r == b.r && a.a == b.a }

// sgr returns the ANSI SGR sequence for the attribute.
func (a Attr) sgr() string {
	var b []string
	if a.Bold {
		b = append(b, "1")
	}
	if a.Dim {
		b = append(b, "2")
	}
	if a.Italic {
		b = append(b, "3")
	}
	if a.hasFG {
		b = append(b, fmt.Sprintf("38;2;%d;%d;%d", a.FG.r, a.FG.g, a.FG.b))
	}
	if a.hasBG {
		b = append(b, fmt.Sprintf("48;2;%d;%d;%d", a.BG.r, a.BG.g, a.BG.b))
	}
	if len(b) == 0 {
		return "\x1b[0m"
	}
	return "\x1b[" + strings.Join(b, ";") + "m"
}

func isWide(r rune) bool {
	if r < 0x1100 {
		return false
	}
	return (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2E80 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		// Add emoji ranges for better wide character support
		(r >= 0x1F000 && r <= 0x1FAFF)
}

// WrapRow word-wraps a row to fit width, preserving attributes. Words
// longer than width are hard-broken. Trailing spaces are trimmed so
// transcript lines stay clean.
func WrapRow(row Row, width int) []Row {
	return wrapRow(row, width, false)
}

// WrapRowKeepTrailing is WrapRow without stripping trailing spaces.
// Used for the live input line so typed spaces stay visible.
func WrapRowKeepTrailing(row Row, width int) []Row {
	return wrapRow(row, width, true)
}

func wrapRow(row Row, width int, keepTrailing bool) []Row {
	if width <= 0 {
		return []Row{row}
	}

	type tok struct {
		r  rune
		a  Attr
		sp bool
	}
	var toks []tok
	for _, seg := range row.Segs {
		for _, r := range seg.Text {
			toks = append(toks, tok{r: r, a: seg.Attr, sp: r == ' '})
		}
	}

	var out []Row
	var line []tok
	lineW := 0
	flush := func() {
		if !keepTrailing {
			for len(line) > 0 && line[len(line)-1].sp {
				line = line[:len(line)-1]
			}
		}
		var segs []Segment
		for _, t := range line {
			if len(segs) > 0 && segs[len(segs)-1].Attr == t.a {
				segs[len(segs)-1].Text += string(t.r)
			} else {
				segs = append(segs, Segment{Text: string(t.r), Attr: t.a})
			}
		}
		if len(segs) == 0 {
			segs = []Segment{{Text: strings.Repeat(" ", width), Attr: styleDefault}}
		}
		out = append(out, Row{Segs: segs})
		line = nil
		lineW = 0
	}

	i := 0
	for i < len(toks) {
		if toks[i].sp {
			if lineW+1 > width && lineW > 0 {
				flush()
			}
			line = append(line, toks[i])
			lineW++
			i++
			continue
		}
		// Collect a word (or a single over-long rune run).
		j := i
		wordW := 0
		for j < len(toks) && !toks[j].sp {
			ww := runeWidth(toks[j].r)
			if lineW+wordW+ww > width && wordW > 0 {
				break
			}
			wordW += ww
			j++
		}
		word := toks[i:j]
		// If the word does not fit and the line is non-empty, wrap first.
		if lineW+wordW > width && lineW > 0 {
			flush()
		}
		// A single word can still be wider than width (CJK / long token).
		// Hard-break it so no emitted row exceeds width.
		for len(word) > 0 {
			take := 0
			takeW := 0
			for take < len(word) {
				rw := runeWidth(word[take].r)
				if takeW+rw > width && take > 0 {
					break
				}
				takeW += rw
				take++
				if takeW >= width {
					break
				}
			}
			if take == 0 {
				take, takeW = 1, runeWidth(word[0].r)
			}
			if lineW > 0 && lineW+takeW > width {
				flush()
			}
			line = append(line, word[:take]...)
			lineW += takeW
			word = word[take:]
			if len(word) > 0 {
				flush()
			}
		}
		i = j
		if i < len(toks) && toks[i].sp && i == j {
			// keep the separating space with the word
			line = append(line, toks[i])
			lineW++
			i++
		}
	}
	flush()
	return out
}

func runeWidth(r rune) int {
	if isWide(r) {
		return 2
	}
	return 1
}
