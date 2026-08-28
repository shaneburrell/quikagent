package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPreMissingIsAllow(t *testing.T) {
	if err := Pre(context.Background(), t.TempDir(), "bash", `{}`); err != nil {
		t.Fatal(err)
	}
}

func TestPreDeny(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	dir := t.TempDir()
	hookDir := filepath.Join(dir, ".quikagent", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hookDir, "pre-tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Pre(context.Background(), dir, "bash", `{}`); err == nil {
		t.Fatal("expected deny")
	}
}

func TestHooksPostSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	dir := t.TempDir()
	hookDir := filepath.Join(dir, ".quikagent", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "post-ran")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(filepath.Join(hookDir, "post-tool"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	Post(context.Background(), dir, "read", `{"path":"f.txt"}`, "ok")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("post hook did not run")
	}
}

func TestPreExecFailureNotDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	dir := t.TempDir()
	hookDir := filepath.Join(dir, ".quikagent", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hookDir, "pre-tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Pre(context.Background(), dir, "bash", `{}`)
	if err == nil {
		t.Fatal("expected exec failure")
	}
	if IsDenied(err) {
		t.Fatalf("non-executable hook should be exec failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "hook failed") {
		t.Fatalf("err = %v, want hook failed", err)
	}
}

func TestPreDenyIsDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	dir := t.TempDir()
	hookDir := filepath.Join(dir, ".quikagent", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hookDir, "pre-tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Pre(context.Background(), dir, "bash", `{}`)
	if err == nil || !IsDenied(err) {
		t.Fatalf("err = %v, want DeniedError", err)
	}
}

func TestHooksPayloadShape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	dir := t.TempDir()
	hookDir := filepath.Join(dir, ".quikagent", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(dir, "payload.json")
	script := "#!/bin/sh\ncat > " + outFile + "\n"
	if err := os.WriteFile(filepath.Join(hookDir, "pre-tool"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Pre(context.Background(), dir, "bash", `{"command":"echo"}`); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("stdin not JSON: %s", data)
	}
	for _, k := range []string{"phase", "tool", "args"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("missing %q in %s", k, data)
		}
	}
	var p Payload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if p.Phase != "pre" || p.Tool != "bash" || p.Args != `{"command":"echo"}` {
		t.Fatalf("payload = %+v raw=%s", p, data)
	}
}
