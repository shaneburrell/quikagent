package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLSPNotConfigured(t *testing.T) {
	_, err := NewLSP(t.TempDir(), LSPConfig{}).Run(context.Background(), json.RawMessage(`{"op":"hover","path":"x.go"}`))
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v", err)
	}
}

func TestLSPCallTimeoutClearsPending(t *testing.T) {
	var discarded bytes.Buffer
	c := &lspClient{
		stdin:   nopWriteCloser{&discarded},
		scan:    bufio.NewReader(bytes.NewReader(nil)),
		pending: map[int64]chan lspResp{},
		done:    make(chan struct{}),
	}
	// do not start readLoop; call should time out and drop pending
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.call(ctx, "initialize", map[string]any{})
	if err == nil {
		t.Fatal("expected timeout")
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending leaked: %d", n)
	}
}

func TestLSPMalformedJSONUnblocksCaller(t *testing.T) {
	pr, pw := io.Pipe()
	c := &lspClient{
		stdin:   nopWriteCloser{io.Discard},
		scan:    bufio.NewReader(pr),
		pending: map[int64]chan lspResp{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	t.Cleanup(func() { _ = pw.Close() })

	errCh := make(chan error, 1)
	go func() {
		_, err := c.call(context.Background(), "initialize", map[string]any{})
		errCh <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.pending)
		c.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	body := []byte("{not-json")
	if _, err := pw.Write([]byte("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write(body); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "malformed JSON") {
			t.Fatalf("err = %v, want malformed JSON", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("caller hung on malformed JSON")
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestLSPInvalidOp(t *testing.T) {
	// Command set so we pass the config check, but start will fail — use a missing binary
	// after op validation. Invalid op should fail before spawn.
	l := &lspTool{workdir: t.TempDir(), cfg: LSPConfig{Command: "definitely-not-an-lsp"}}
	_, err := l.Run(context.Background(), json.RawMessage(`{"op":"nope"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Fatalf("err = %v", err)
	}
}
