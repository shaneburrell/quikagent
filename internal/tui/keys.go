package tui

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// KeyKind classifies a parsed key event.
type KeyKind int

const (
	KeyRune  KeyKind = iota // printable character in Rune
	KeyNamed                // special key in Name
	KeyCtrl                 // Ctrl+letter in Ctrl
	KeyPaste                // bracketed-paste text in Text
	KeyMouse                // SGR mouse in Btn/Col/Row/Press
)

// SGR mouse button codes (xterm).
const (
	MouseBtnLeft    = 0
	MouseBtnMiddle  = 1
	MouseBtnRight   = 2
	MouseBtnRelease = 3
	MouseWheelUp    = 64
	MouseWheelDown  = 65
)

// Named key identifiers.
const (
	KeyEnter      = "enter"
	KeyBackspace  = "backspace"
	KeyDelete     = "delete"
	KeyTab        = "tab"
	KeyEsc        = "esc"
	KeyUp         = "up"
	KeyDown       = "down"
	KeyLeft       = "left"
	KeyRight      = "right"
	KeyHome       = "home"
	KeyEnd        = "end"
	KeyPageUp     = "pageup"
	KeyPageDown   = "pagedown"
	KeyShiftEnter = "shift+enter"
	KeyShiftUp    = "shift+up"
	KeyShiftDown  = "shift+down"
	KeyF2         = "f2"
	KeyShiftF2    = "shift+f2"
)

// Key is a parsed keyboard or mouse event.
type Key struct {
	Kind  KeyKind
	Rune  rune
	Name  string
	Ctrl  rune
	Text  string
	Btn   int  // mouse button / wheel code
	Col   int  // 1-based column
	Row   int  // 1-based row
	Press bool // true for press (M), false for release (m)
}

func (k Key) is(name string) bool { return k.Kind == KeyNamed && k.Name == name }

// Parse turns one raw terminal read into key events. Sequences split
// across reads degrade to an Esc (rare and harmless), except an
// incomplete bracketed-paste introducer which Parse discards (use
// parseRemain to carry it across reads).
func Parse(chunk []byte) []Key {
	keys, _ := parseRemain(chunk)
	return keys
}

// parseRemain is Parse plus leftover bytes that must be prepended to
// the next chunk (incomplete \x1b[200~ paste).
func parseRemain(chunk []byte) (keys []Key, remain []byte) {
	i := 0
	for i < len(chunk) {
		b := chunk[i]
		switch {
		case b == 0x1b:
			rest := chunk[i+1:]
			if len(rest) == 0 {
				keys = append(keys, Key{Kind: KeyNamed, Name: KeyEsc})
				i++
				continue
			}
			switch rest[0] {
			case '[':
				event, consumed := parseCSI(rest)
				if consumed == 0 {
					if isIncompletePaste(rest) {
						return keys, chunk[i:]
					}
					// Incomplete CSI at end of chunk: report Esc, drop.
					keys = append(keys, Key{Kind: KeyNamed, Name: KeyEsc})
					i = len(chunk)
					continue
				}
				if event != nil {
					keys = append(keys, *event)
				}
				i += 1 + consumed
			case 'O':
				if len(rest) >= 2 {
					if k, ok := ss3Key(rest[1]); ok {
						keys = append(keys, k)
						i += 3
						continue
					}
				}
				keys = append(keys, Key{Kind: KeyNamed, Name: KeyEsc})
				i++
			default:
				// Bare ESC followed by another byte: emit Esc and let the
				// next iteration handle the following byte (Alt+key).
				keys = append(keys, Key{Kind: KeyNamed, Name: KeyEsc})
				i++
			}
		case b == '\r' || b == '\n':
			keys = append(keys, Key{Kind: KeyNamed, Name: KeyEnter})
			i++
		case b == 0x7f || b == 0x08:
			keys = append(keys, Key{Kind: KeyNamed, Name: KeyBackspace})
			i++
		case b == 0x09:
			keys = append(keys, Key{Kind: KeyNamed, Name: KeyTab})
			i++
		case b < 0x20:
			keys = append(keys, Key{Kind: KeyCtrl, Ctrl: ctrlChar(b)})
			i++
		default:
			r, size := utf8.DecodeRune(chunk[i:])
			if r == utf8.RuneError && size == 1 {
				i++
				continue
			}
			if unicode.IsControl(r) {
				i++
				continue
			}
			keys = append(keys, Key{Kind: KeyRune, Rune: r})
			i += size
		}
	}
	return keys, nil
}

// isIncompletePaste reports whether rest (starting at '[') is an
// unfinished bracketed-paste introducer or an open paste without \x1b[201~.
func isIncompletePaste(rest []byte) bool {
	intro := []byte("[200~")
	if len(rest) < len(intro) {
		return bytes.HasPrefix(intro, rest)
	}
	return bytes.HasPrefix(rest, intro)
}

func ctrlChar(b byte) rune {
	if b == 0x1f {
		return '/'
	}
	return rune(b + 0x60) // 0x01 -> 'a', 0x03 -> 'c', ...
}

func ss3Key(b byte) (Key, bool) {
	switch b {
	case 'A':
		return Key{Kind: KeyNamed, Name: KeyUp}, true
	case 'B':
		return Key{Kind: KeyNamed, Name: KeyDown}, true
	case 'C':
		return Key{Kind: KeyNamed, Name: KeyRight}, true
	case 'D':
		return Key{Kind: KeyNamed, Name: KeyLeft}, true
	case 'Q':
		return Key{Kind: KeyNamed, Name: KeyF2}, true
	}
	return Key{}, false
}

// parseCSI handles a CSI sequence starting after "\x1b[" (i.e. rest begins
// with '['). It returns the key (or nil for ignored sequences) and the
// number of bytes consumed including the leading '['.
func parseCSI(rest []byte) (event *Key, consumed int) {
	// Find the final byte (0x40..0x7E).
	end := -1
	for j := 1; j < len(rest); j++ {
		if rest[j] >= 0x40 && rest[j] <= 0x7E {
			end = j
			break
		}
	}
	if end == -1 {
		return nil, 0
	}
	params := string(rest[1:end])
	final := byte(rest[end])
	consumed = end + 1

	// Bracketed paste: "\x1b[200~<text>\x1b[201~"
	if params == "200" && final == '~' {
		// Look for the closing marker; paste spans into this chunk.
		rest2 := rest[consumed:]
		open := []byte{0x1b, '[', '2', '0', '1', '~'}
		for j := 0; j+len(open) <= len(rest2); j++ {
			if equalBytes(rest2[j:j+len(open)], open) {
				text := string(rest2[:j])
				return &Key{Kind: KeyPaste, Text: text}, consumed + j + len(open)
			}
		}
		// Close marker missing: leave the introducer + body for the next chunk.
		return nil, 0
	}

	switch final {
	case 'A', 'B', 'C', 'D':
		base := map[byte]string{'A': KeyUp, 'B': KeyDown, 'C': KeyRight, 'D': KeyLeft}[final]
		if params == "1;2" || params == "2" { // Shift+arrow (xterm)
			switch final {
			case 'A':
				return keyName(KeyShiftUp), consumed
			case 'B':
				return keyName(KeyShiftDown), consumed
			}
		}
		if params != "" && params != "1" {
			return nil, consumed
		}
		return keyName(base), consumed
	case 'H', 'F':
		// Bare "\x1b[H"/"\x1b[F" are Home/End; with params they are
		// cursor-position (CUP) reports, which we ignore.
		if params == "" {
			if final == 'H' {
				return keyName(KeyHome), consumed
			}
			return keyName(KeyEnd), consumed
		}
		return nil, consumed
	case '~':
		switch params {
		case "1":
			return keyName(KeyHome), consumed
		case "2":
			// CSI 2~ is Insert; no insert mode, so ignore.
			return nil, consumed
		case "3":
			return keyName(KeyDelete), consumed
		case "4":
			return keyName(KeyEnd), consumed
		case "5":
			return keyName(KeyPageUp), consumed
		case "6":
			return keyName(KeyPageDown), consumed
		case "12":
			return keyName(KeyF2), consumed
		case "12;2":
			return keyName(KeyShiftF2), consumed
		}
		return nil, consumed
	case 'Q':
		// xterm: CSI 1;2 Q = Shift+F2
		if params == "1;2" {
			return keyName(KeyShiftF2), consumed
		}
		if params == "" || params == "1" {
			return keyName(KeyF2), consumed
		}
		return nil, consumed
	case 'u':
		// kitty keyboard protocol: "13;2u" = Shift+Enter
		if params == "13;2" {
			return keyName(KeyShiftEnter), consumed
		}
		return nil, consumed
	case 'M', 'm':
		// SGR mouse: "\x1b[<btn;col;rowM" (press) or "...m" (release).
		if len(params) > 0 && params[0] == '<' {
			if k, ok := parseSGRMouse(params[1:], final == 'M'); ok {
				return &k, consumed
			}
		}
		return nil, consumed
	}
	return nil, consumed
}

func parseSGRMouse(params string, press bool) (Key, bool) {
	parts := strings.Split(params, ";")
	if len(parts) != 3 {
		return Key{}, false
	}
	btn, err1 := strconv.Atoi(parts[0])
	col, err2 := strconv.Atoi(parts[1])
	row, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return Key{}, false
	}
	if col < 1 || row < 1 {
		return Key{}, false
	}
	return Key{Kind: KeyMouse, Btn: btn, Col: col, Row: row, Press: press}, true
}

func keyName(name string) *Key {
	k := Key{Kind: KeyNamed, Name: name}
	return &k
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
