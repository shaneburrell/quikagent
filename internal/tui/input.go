package tui

// Input is a multiline text buffer with a (row, col) cursor.
type Input struct {
	lines []string
	row   int
	col   int
}

// NewInput builds an empty buffer with a single blank line.
func NewInput() *Input { return &Input{lines: []string{""}} }

// Text returns the full buffer content.
func (i *Input) Text() string {
	out := make([]string, 0, len(i.lines))
	for _, l := range i.lines {
		out = append(out, l)
	}
	return join(out, "\n")
}

// SetText replaces the buffer and moves the cursor to the end.
func (i *Input) SetText(s string) {
	i.lines = splitLines(s)
	i.row = len(i.lines) - 1
	i.col = len([]rune(i.lines[i.row]))
}

// Insert adds a printable rune at the cursor.
func (i *Input) Insert(r rune) {
	line := []rune(i.lines[i.row])
	i.lines[i.row] = string(append(line[:i.col], append([]rune{r}, line[i.col:]...)...))
	i.col++
}

// Paste inserts a (possibly multiline) chunk at the cursor.
func (i *Input) Paste(s string) {
	parts := splitLines(s)
	if len(parts) == 1 {
		for _, r := range parts[0] {
			i.Insert(r)
		}
		return
	}
	line := []rune(i.lines[i.row])
	tail := string(line[i.col:]) // capture before append may reuse the backing array
	head := string(append(append([]rune(nil), line[:i.col]...), []rune(parts[0])...))

	newLines := make([]string, 0, len(i.lines)+len(parts)-1)
	newLines = append(newLines, i.lines[:i.row]...)
	newLines = append(newLines, head)
	newLines = append(newLines, parts[1:len(parts)-1]...)
	newLines = append(newLines, join([]string{parts[len(parts)-1], tail}, ""))
	newLines = append(newLines, i.lines[i.row+1:]...)

	i.lines = newLines
	i.row = i.row + len(parts) - 1
	i.col = len([]rune(parts[len(parts)-1]))
}

// Newline splits the line at the cursor.
func (i *Input) Newline() {
	line := []rune(i.lines[i.row])
	first := string(line[:i.col])
	second := string(line[i.col:])
	newLines := make([]string, 0, len(i.lines)+1)
	newLines = append(newLines, i.lines[:i.row]...)
	newLines = append(newLines, first, second)
	newLines = append(newLines, i.lines[i.row+1:]...)
	i.lines = newLines
	i.row++
	i.col = 0
}

// Backspace deletes the rune before the cursor, joining lines as needed.
func (i *Input) Backspace() {
	if i.col > 0 {
		line := []rune(i.lines[i.row])
		i.lines[i.row] = string(append(line[:i.col-1], line[i.col:]...))
		i.col--
		return
	}
	if i.row == 0 {
		return
	}
	prev := []rune(i.lines[i.row-1])
	rest := []rune(i.lines[i.row])
	i.lines[i.row-1] = string(append(prev, rest...))
	// Drop the now-merged line (sits at index i.row after the merge).
	i.lines = append(i.lines[:i.row], i.lines[i.row+1:]...)
	i.row--
	i.col = len(prev)
}

// Delete removes the rune at the cursor.
func (i *Input) Delete() {
	line := []rune(i.lines[i.row])
	if i.col < len(line) {
		i.lines[i.row] = string(append(line[:i.col], line[i.col+1:]...))
		return
	}
	if i.row < len(i.lines)-1 {
		i.lines[i.row] = string(line) + i.lines[i.row+1]
		i.lines = append(i.lines[:i.row+1], i.lines[i.row+2:]...)
	}
}

// Left moves the cursor left, wrapping to the previous line.
func (i *Input) Left() {
	if i.col > 0 {
		i.col--
		return
	}
	if i.row > 0 {
		i.row--
		i.col = len([]rune(i.lines[i.row]))
	}
}

// Right moves the cursor right, wrapping to the next line.
func (i *Input) Right() {
	if i.col < len([]rune(i.lines[i.row])) {
		i.col++
		return
	}
	if i.row < len(i.lines)-1 {
		i.row++
		i.col = 0
	}
}

// Up moves to the previous line, clamping the column.
func (i *Input) Up() {
	if i.row > 0 {
		i.row--
		if i.col > len([]rune(i.lines[i.row])) {
			i.col = len([]rune(i.lines[i.row]))
		}
	}
}

// Down moves to the next line, clamping the column.
func (i *Input) Down() {
	if i.row < len(i.lines)-1 {
		i.row++
		if i.col > len([]rune(i.lines[i.row])) {
			i.col = len([]rune(i.lines[i.row]))
		}
	}
}

// Home moves to the start of the line.
func (i *Input) Home() { i.col = 0 }

// End moves to the end of the line.
func (i *Input) End() { i.col = len([]rune(i.lines[i.row])) }

// Clear empties the buffer.
func (i *Input) Clear() { i.SetText("") }

// CursorLine returns the cursor's line index.
func (i *Input) CursorLine() int { return i.row }

// CursorCol returns the cursor's column in runes.
func (i *Input) CursorCol() int { return i.col }

// LineCount returns how many lines the buffer holds.
func (i *Input) LineCount() int { return len(i.lines) }

// LineText returns the cursor's line.
func (i *Input) LineText() string { return i.lines[i.row] }

func splitLines(s string) []string {
	var out []string
	cur := []rune{}
	for _, r := range s {
		if r == '\n' {
			out = append(out, string(cur))
			cur = cur[:0]
			continue
		}
		cur = append(cur, r)
	}
	out = append(out, string(cur))
	return out
}

func join(parts []string, sep string) string {
	total := 0
	for i, p := range parts {
		total += len(p)
		if i > 0 {
			total += len(sep)
		}
	}
	b := make([]byte, 0, total)
	for i, p := range parts {
		if i > 0 {
			b = append(b, sep...)
		}
		b = append(b, p...)
	}
	return string(b)
}
