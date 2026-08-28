package tui

import "testing"

func TestInputInsertAndText(t *testing.T) {
	i := NewInput()
	for _, r := range "hello" {
		i.Insert(r)
	}
	if i.Text() != "hello" {
		t.Fatalf("text = %q", i.Text())
	}
	if i.CursorCol() != 5 {
		t.Fatalf("col = %d", i.CursorCol())
	}
}

func TestInputBackspace(t *testing.T) {
	i := NewInput()
	i.SetText("hello")
	i.Backspace()
	if i.Text() != "hell" || i.CursorCol() != 4 {
		t.Fatalf("text=%q col=%d", i.Text(), i.CursorCol())
	}
}

func TestInputBackspaceJoinsLines(t *testing.T) {
	i := NewInput()
	i.SetText("ab\ncd")
	// Cursor at start of line 2; backspace joins "cd" onto line one.
	i.row, i.col = 1, 0
	i.Backspace()
	if i.Text() != "abcd" || i.CursorLine() != 0 || i.CursorCol() != 2 {
		t.Fatalf("text=%q row=%d col=%d", i.Text(), i.CursorLine(), i.CursorCol())
	}
}

func TestInputNewline(t *testing.T) {
	i := NewInput()
	i.SetText("abcdef")
	i.col = 3
	i.Newline()
	if i.Text() != "abc\ndef" || i.CursorLine() != 1 || i.CursorCol() != 0 {
		t.Fatalf("text=%q row=%d col=%d", i.Text(), i.CursorLine(), i.CursorCol())
	}
}

func TestInputMultiNav(t *testing.T) {
	i := NewInput()
	i.SetText("abc\ndef") // cursor ends at row 1, col 3
	i.Up()
	i.Up() // stays on line 0, col clamps to 3
	if i.CursorLine() != 0 || i.CursorCol() != 3 {
		t.Fatalf("up: row=%d col=%d", i.CursorLine(), i.CursorCol())
	}
	i.Left()
	i.Left()
	i.Left()
	i.Left() // clamps at start of line 0
	if i.CursorCol() != 0 {
		t.Fatal("left clamp failed")
	}
	i.Right()
	i.Right()
	i.Right()
	i.Right() // wraps to line 1
	if i.CursorLine() != 1 || i.CursorCol() != 0 {
		t.Fatalf("right wrap: row=%d col=%d", i.CursorLine(), i.CursorCol())
	}
	i.Left() // wraps back to end of line 0
	if i.CursorLine() != 0 || i.CursorCol() != 3 {
		t.Fatalf("left wrap: row=%d col=%d", i.CursorLine(), i.CursorCol())
	}
	i.End()
	i.Down()
	i.Down() // clamps
	if i.CursorLine() != 1 || i.CursorCol() != 3 {
		t.Fatalf("down clamp: row=%d col=%d", i.CursorLine(), i.CursorCol())
	}
	i.Home()
	if i.CursorCol() != 0 {
		t.Fatal("home failed")
	}
}

func TestInputDelete(t *testing.T) {
	i := NewInput()
	i.SetText("abcd")
	i.col = 1
	i.Delete()
	if i.Text() != "acd" {
		t.Fatalf("text = %q", i.Text())
	}
	i.SetText("ab\ncd\nef") // cursor at end of "ef"
	i.row, i.col = 1, 2     // EOL of "cd"
	i.Delete()              // merges next line
	if i.Text() != "ab\ncdef" {
		t.Fatalf("merge text = %q", i.Text())
	}
}

func TestInputPasteSingleLine(t *testing.T) {
	i := NewInput()
	i.SetText("aXX")
	i.col = 1
	i.Paste("bc")
	if i.Text() != "abcXX" || i.CursorCol() != 3 {
		t.Fatalf("text=%q col=%d", i.Text(), i.CursorCol())
	}
}

func TestInputPasteMultiLine(t *testing.T) {
	i := NewInput()
	i.SetText("aXX")
	i.col = 1
	i.Paste("l1\nl2\nl3")
	want := "al1\nl2\nl3XX"
	if i.Text() != want {
		t.Fatalf("text = %q want %q", i.Text(), want)
	}
	if i.CursorLine() != 2 || i.CursorCol() != 2 {
		t.Fatalf("row=%d col=%d", i.CursorLine(), i.CursorCol())
	}
}

func TestInputClear(t *testing.T) {
	i := NewInput()
	i.SetText("abc")
	i.Clear()
	if i.Text() != "" || i.LineCount() != 1 {
		t.Fatalf("text=%q lines=%d", i.Text(), i.LineCount())
	}
}
