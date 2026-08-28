package tui

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// Terminal owns the tty: raw mode, alternate screen, hidden cursor,
// and resize notifications.
type Terminal struct {
	f            *os.File
	state        *term.State
	sigs         chan os.Signal
	resized      chan struct{}
	closed       bool
	shutdownOnce sync.Once
}

// NewTerminal takes over the tty. The caller must call Shutdown, which
// restores the terminal even on panic (use defer).
func NewTerminal() (*Terminal, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/tty: %w", err)
	}
	fd := int(f.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		f.Close()
		return nil, err
	}
	if _, err := term.MakeRaw(fd); err != nil {
		f.Close()
		return nil, err
	}

	t := &Terminal{
		f:       f,
		state:   state,
		sigs:    make(chan os.Signal, 1),
		resized: make(chan struct{}, 1),
	}
	signal.Notify(t.sigs, syscall.SIGWINCH)

	// Enter alternate screen, clear, hide cursor, enable bracketed paste
	// and SGR mouse tracking (wheel + clicks).
	f.WriteString("\x1b[?1049h\x1b[2J\x1b[H\x1b[?25l\x1b[?2004h\x1b[?1006h\x1b[?1000h")
	return t, nil
}

// Size returns the terminal dimensions in cells.
func (t *Terminal) Size() (int, int) {
	w, h, err := term.GetSize(int(t.f.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// Read blocks until input or EOF.
func (t *Terminal) Read(buf []byte) (int, error) { return t.f.Read(buf) }

// Resized is closed again each time SIGWINCH arrives (buffered, non-blocking).
func (t *Terminal) Resized() <-chan struct{} { return t.resized }

// Signal reports the next OS signal (SIGWINCH is consumed for resize).
func (t *Terminal) Signal() <-chan os.Signal { return t.sigs }

// ConsumeWinch drains the resize channel and reports if one was pending.
func (t *Terminal) ConsumeWinch() bool {
	select {
	case <-t.resized:
		return true
	default:
		return false
	}
}

// NotifyResize is called by the signal-forwarding goroutine.
func (t *Terminal) NotifyResize() {
	select {
	case t.resized <- struct{}{}:
	default:
	}
}

// Write writes raw bytes to the tty.
func (t *Terminal) Write(p []byte) (int, error) { return t.f.Write(p) }

// Shutdown restores the terminal. Safe to call more than once.
func (t *Terminal) Shutdown() {
	t.shutdownOnce.Do(func() {
		signal.Stop(t.sigs)
		close(t.sigs)
		// Disable mouse, leave bracketed paste, show cursor, leave alternate screen.
		t.f.WriteString("\x1b[?1000l\x1b[?1006l\x1b[?2004l\x1b[?25h\x1b[?1049l")
		_ = term.Restore(int(t.f.Fd()), t.state)
		t.f.Close()
	})
}
