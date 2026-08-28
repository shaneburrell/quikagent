package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(root, "link.txt"); err == nil {
		t.Fatal("expected sandbox error for symlink escape")
	}
}

func TestGlobRejectsDotDotEscape(t *testing.T) {
	root := t.TempDir()
	r := New(root)
	tool, ok := r.Get("glob")
	if !ok {
		t.Fatal("missing glob")
	}
	res, err := tool.Run(t.Context(), []byte(`{"pattern":"../../../etc/passwd"}`))
	if err != nil {
		return
	}
	if strings.Contains(res, "passwd") || strings.Contains(res, "/etc/") {
		t.Fatalf("glob leaked escape path: %q", res)
	}
}

func TestGitBlocksNoIndexAndOutput(t *testing.T) {
	root := t.TempDir()
	r := New(root)
	tool, _ := r.Get("git")
	if _, err := tool.Run(t.Context(), []byte(`{"action":"diff","args":["--no-index","/etc/passwd","/etc/hosts"]}`)); err == nil {
		t.Fatal("expected --no-index blocked")
	}
	if _, err := tool.Run(t.Context(), []byte(`{"action":"diff","args":["--output=/tmp/pwned"]}`)); err == nil {
		t.Fatal("expected --output blocked")
	}
}

func TestFetchBlocksLoopback(t *testing.T) {
	r := New(t.TempDir())
	tool, _ := r.Get("fetch")
	if _, err := tool.Run(t.Context(), []byte(`{"url":"http://127.0.0.1/"}`)); err == nil {
		t.Fatal("expected loopback blocked")
	}
}
