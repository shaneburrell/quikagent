package tui

import (
	"bytes"
	"testing"
)

func TestParseBasic(t *testing.T) {
	keys := Parse([]byte("hi\r"))
	if len(keys) != 3 {
		t.Fatalf("keys = %+v", keys)
	}
	if keys[0].Kind != KeyRune || keys[0].Rune != 'h' {
		t.Fatalf("k0 = %+v", keys[0])
	}
	if keys[1].Kind != KeyRune || keys[1].Rune != 'i' {
		t.Fatalf("k1 = %+v", keys[1])
	}
	if !keys[2].is(KeyEnter) {
		t.Fatalf("k2 = %+v", keys[2])
	}
}

func TestParseArrowsAndEdit(t *testing.T) {
	keys := Parse([]byte("\x1b[A\x1b[B\x1b[C\x1b[D\x1b[H\x1b[F\x1b[3~\x7f"))
	want := []string{KeyUp, KeyDown, KeyRight, KeyLeft, KeyHome, KeyEnd, KeyDelete, KeyBackspace}
	if len(keys) != len(want) {
		t.Fatalf("keys = %+v", keys)
	}
	for i, k := range keys {
		if !k.is(want[i]) {
			t.Fatalf("k%d = %+v want %s", i, k, want[i])
		}
	}
}

func TestParseCtrlAndTab(t *testing.T) {
	keys := Parse([]byte("\x03\x04\t\x01"))
	if len(keys) != 4 {
		t.Fatalf("keys = %+v", keys)
	}
	for i, want := range []rune{'c', 'd', 0, 'a'} {
		k := keys[i]
		if i == 2 {
			if !k.is(KeyTab) {
				t.Fatalf("k2 = %+v", k)
			}
			continue
		}
		if k.Kind != KeyCtrl || k.Ctrl != want {
			t.Fatalf("k%d = %+v want ctrl+%c", i, k, want)
		}
	}
}

func TestParseEscAlone(t *testing.T) {
	keys := Parse([]byte{0x1b})
	if len(keys) != 1 || !keys[0].is(KeyEsc) {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestParseEscThenChar(t *testing.T) {
	// Alt/Meta (ESC + printable) is a single rune, not Esc then the char.
	keys := Parse([]byte{0x1b, 'x'})
	if len(keys) != 1 || keys[0].Kind != KeyRune || keys[0].Rune != 'x' {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestParseShiftEnterXterm(t *testing.T) {
	for _, seq := range []string{"\x1b[13;2~", "\x1b[27;2;13~"} {
		keys := Parse([]byte(seq))
		if len(keys) != 1 || !keys[0].is(KeyShiftEnter) {
			t.Fatalf("%q -> %+v", seq, keys)
		}
	}
}

func TestParseShiftEnterKitty(t *testing.T) {
	keys := Parse([]byte("\x1b[13;2u"))
	if len(keys) != 1 || !keys[0].is(KeyShiftEnter) {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestParseBracketedPaste(t *testing.T) {
	keys := Parse([]byte("\x1b[200~hello world\r\x1b[201~"))
	if len(keys) != 1 {
		t.Fatalf("keys = %+v", keys)
	}
	if keys[0].Kind != KeyPaste || keys[0].Text != "hello world\r" {
		t.Fatalf("paste = %+v", keys[0])
	}
}

func TestParseUTF8(t *testing.T) {
	keys := Parse([]byte("héllo"))
	if len(keys) != 5 {
		t.Fatalf("keys = %+v", keys)
	}
	if keys[1].Rune != 'é' {
		t.Fatalf("k1 = %+v", keys[1])
	}
}

func TestParseIncompleteCSI(t *testing.T) {
	keys := Parse([]byte("\x1b[1"))
	if len(keys) != 1 || !keys[0].is(KeyEsc) {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestParseUnknownCSIIgnored(t *testing.T) {
	keys := Parse([]byte("\x1b[999;99Hx"))
	if len(keys) != 1 || keys[0].Rune != 'x' {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestParseSGRMouseWheel(t *testing.T) {
	keys := Parse([]byte("\x1b[<64;10;5M\x1b[<65;12;8M"))
	if len(keys) != 2 {
		t.Fatalf("keys = %+v", keys)
	}
	if keys[0].Kind != KeyMouse || keys[0].Btn != MouseWheelUp || keys[0].Col != 10 || keys[0].Row != 5 || !keys[0].Press {
		t.Fatalf("wheel up = %+v", keys[0])
	}
	if keys[1].Kind != KeyMouse || keys[1].Btn != MouseWheelDown || keys[1].Col != 12 || keys[1].Row != 8 || !keys[1].Press {
		t.Fatalf("wheel down = %+v", keys[1])
	}
}

func TestParseSGRMouseClick(t *testing.T) {
	keys := Parse([]byte("\x1b[<0;3;4M\x1b[<0;3;4m"))
	if len(keys) != 2 {
		t.Fatalf("keys = %+v", keys)
	}
	if keys[0].Kind != KeyMouse || keys[0].Btn != MouseBtnLeft || !keys[0].Press {
		t.Fatalf("press = %+v", keys[0])
	}
	if keys[1].Kind != KeyMouse || keys[1].Btn != MouseBtnLeft || keys[1].Press {
		t.Fatalf("release = %+v", keys[1])
	}
}

func TestParseF2(t *testing.T) {
	keys := Parse([]byte("\x1b[12~\x1b[12;2~\x1bOQ"))
	if len(keys) != 3 {
		t.Fatalf("keys = %+v", keys)
	}
	if !keys[0].is(KeyF2) || !keys[1].is(KeyShiftF2) || !keys[2].is(KeyF2) {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestParsePageUpDown(t *testing.T) {
	keys := Parse([]byte("\x1b[5~\x1b[6~"))
	if len(keys) != 2 || !keys[0].is(KeyPageUp) || !keys[1].is(KeyPageDown) {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestParseInsertAndEndTilde(t *testing.T) {
	keys := Parse([]byte("\x1b[2~\x1b[4~"))
	if len(keys) != 1 || !keys[0].is(KeyEnd) {
		t.Fatalf("2~ should be ignored, 4~ is End; keys=%+v", keys)
	}
}

func TestParseIncompletePasteBuffered(t *testing.T) {
	keys, rest := parseRemain([]byte("\x1b[200~hello"))
	if len(keys) != 0 {
		t.Fatalf("incomplete paste should not emit keys: %+v", keys)
	}
	if !bytes.HasPrefix(rest, []byte("\x1b[200~")) {
		t.Fatalf("rest=%q", rest)
	}
	keys, rest = parseRemain(append(append([]byte{}, rest...), []byte(" world\x1b[201~")...))
	if len(keys) != 1 || keys[0].Kind != KeyPaste || keys[0].Text != "hello world" {
		t.Fatalf("paste=%+v rest=%q", keys, rest)
	}
	if len(rest) != 0 {
		t.Fatalf("remain after close: %q", rest)
	}
}

func TestParseIncompletePasteIntroducer(t *testing.T) {
	keys, rest := parseRemain([]byte("\x1b[200"))
	if len(keys) != 0 || string(rest) != "\x1b[200" {
		t.Fatalf("keys=%+v rest=%q", keys, rest)
	}
}
