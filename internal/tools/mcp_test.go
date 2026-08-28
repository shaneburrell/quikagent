package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quikagent/internal/config"
)

type discardCloser struct{ io.Writer }

func (discardCloser) Close() error { return nil }

func waitPending(t *testing.T, c *mcpClient) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.pending)
		c.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("pending never registered")
}

// TestAttachMCP spins up a tiny fake MCP server script and registers its tools.
func TestAttachMCP(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-mcp.sh")
	content := `#!/bin/bash
while IFS= read -r line; do
  method=$(printf '%s' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake"}}}\n' "$id"
      ;;
    tools/list)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"echo back","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}}]}}\n' "$id"
      ;;
    tools/call)
      text=$(printf '%s' "$line" | sed -n 's/.*"text":"\([^"]*\)".*/\1/p')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"echo:%s"}]}}\n' "$id" "$text"
      ;;
    *)
      if [ -n "$id" ]; then
        printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      fi
      ;;
  esac
done
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	r := New(dir)
	warnings, err := AttachMCP(r, map[string]config.MCPServer{
		"fake": {Command: script},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	if len(warnings) > 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	tool, ok := r.Get("mcp_fake_echo")
	if !ok {
		t.Fatal("mcp tool not registered")
	}
	out, err := tool.Run(t.Context(), json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "echo:hi") {
		t.Fatalf("out = %q", out)
	}
}

func TestAttachMCPEmpty(t *testing.T) {
	r := New(t.TempDir())
	if _, err := AttachMCP(r, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachMCP(r, map[string]config.MCPServer{"x": {Command: ""}}); err != nil {
		t.Fatal(err)
	}
}

func TestAttachMCPRemoteURLWarns(t *testing.T) {
	r := New(t.TempDir())
	warns, err := AttachMCP(r, map[string]config.MCPServer{"remote": {URL: "https://example.com/mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "not yet supported") {
		t.Fatalf("warns = %v", warns)
	}
}

func TestMCPCallTimeoutClearsPending(t *testing.T) {
	pr, pw := io.Pipe()
	c := &mcpClient{
		stdin:   discardCloser{io.Discard},
		scan:    bufio.NewScanner(pr),
		pending: map[int64]chan mcpResponse{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	t.Cleanup(func() { _ = pw.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.call(ctx, "ping", nil)
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

func TestMCPMalformedJSONUnblocksCaller(t *testing.T) {
	pr, pw := io.Pipe()
	c := &mcpClient{
		stdin:   discardCloser{io.Discard},
		scan:    bufio.NewScanner(pr),
		pending: map[int64]chan mcpResponse{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	t.Cleanup(func() { _ = pw.Close() })

	errCh := make(chan error, 1)
	go func() {
		_, err := c.call(context.Background(), "ping", nil)
		errCh <- err
	}()
	waitPending(t, c)
	if _, err := pw.Write([]byte("{not-json\n")); err != nil {
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

func TestRegistryClose(t *testing.T) {
	r := New(t.TempDir())
	called := false
	r.AddCloser(func() { called = true })
	r.Close()
	if !called {
		t.Fatal("closer not called")
	}
}
