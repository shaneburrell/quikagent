package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"quikagent/internal/config"
	"quikagent/internal/llm"
	"quikagent/internal/session"
)

// TestPrintAllowToolReusesStdin verifies that the allow tool function can be
// called multiple times: printAllowTool creates one stdin reader and reuses
// it across calls (so buffered input like "y\ny\n" is not dropped).
func TestVersionStringDefault(t *testing.T) {
	if got := versionString(); got != "dev" {
		t.Fatalf("versionString() = %q, want dev", got)
	}
}

func TestExportSessionIncludesTrace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTrace(session.TraceRecord{Type: "turn_start", Turn: "t1", Mode: "build", Model: "qwen", Frontend: "print"}); err != nil {
		t.Fatal(err)
	}
	ok := true
	if err := s.AppendTrace(session.TraceRecord{Type: "turn_end", Turn: "t1", OK: &ok, Steps: 1, MS: 8}); err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = exportSession(s.ID)
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	text := string(out)
	if !strings.Contains(text, "## User") || !strings.Contains(text, "## Trace") || !strings.Contains(text, "frontend=print") {
		t.Fatalf("export = %s", text)
	}
}

func TestPrintAllowToolReusesStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})
	if _, err := w.WriteString("y\ny\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	allow := printAllowTool(false, config.Permissions{})
	if err := allow(context.Background(), "write", `{"path":"test","content":"test"}`); err != nil {
		t.Fatalf("first prompt: %v", err)
	}
	if err := allow(context.Background(), "edit", `{"path":"test2","content":"test2"}`); err != nil {
		t.Fatalf("second prompt should reuse stdin: %v", err)
	}
}

func TestPrintAllowToolAutoYes(t *testing.T) {
	allow := printAllowTool(true, config.Permissions{})
	if err := allow(context.Background(), "write", `{"path":"test","content":"test"}`); err != nil {
		t.Fatalf("auto-yes should succeed: %v", err)
	}
}

func TestWebRejectsPrintFlag(t *testing.T) {
	err := run("hello", false, "", false, "8080", false, false, false, "", nil)
	if err == nil || !strings.Contains(err.Error(), "--web cannot be combined with -p") {
		t.Fatalf("got %v", err)
	}
}

func TestPrintAllowToolHonorsCancel(t *testing.T) {
	allow := printAllowTool(false, config.Permissions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := allow(ctx, "write", `{"path":"x","content":"y"}`)
	if err == nil || err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}

func TestPrintAllowToolPermissions(t *testing.T) {
	// Deny rules block even with auto-yes; allow rules skip the prompt.
	perms := config.Permissions{
		Allow: []string{"bash(git status*)"},
		Deny:  []string{"bash(rm *)"},
	}
	allow := printAllowTool(true, perms)

	if err := allow(context.Background(), "bash", `{"command":"rm -rf /tmp/x"}`); err == nil {
		t.Fatal("deny rule should block rm")
	} else if !strings.Contains(err.Error(), "denied by permissions") {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := allow(context.Background(), "bash", `{"command":"git status --short"}`); err != nil {
		t.Fatalf("allow rule should pass git status: %v", err)
	}
}
