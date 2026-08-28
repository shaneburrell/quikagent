package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListBasicAndIgnore(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("secret.txt\nbuild/\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "readme.md"), []byte("hi"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("no"), 0o644)
	_ = os.Mkdir(filepath.Join(dir, "src"), 0o755)
	_ = os.Mkdir(filepath.Join(dir, "build"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "build", "out"), []byte("x"), 0o644)

	l := newList(dir)
	out, err := l.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "readme.md") || !strings.Contains(out, "src/") {
		t.Fatalf("listing = %q", out)
	}
	if strings.Contains(out, "secret.txt") || strings.Contains(out, "build/") {
		t.Fatalf("ignored entries leaked: %q", out)
	}
}

func TestListSandbox(t *testing.T) {
	dir := t.TempDir()
	l := newList(dir)
	_, err := l.Run(context.Background(), json.RawMessage(`{"path":"../"}`))
	if err == nil {
		t.Fatal("expected sandbox error")
	}
}

func TestListMissing(t *testing.T) {
	dir := t.TempDir()
	l := newList(dir)
	_, err := l.Run(context.Background(), json.RawMessage(`{"path":"nope"}`))
	if err == nil {
		t.Fatal("expected missing dir error")
	}
}

func TestIgnoreReinclude(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("dist/\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".ignore"), []byte("!dist/\n"), 0o644)
	m := newIgnoreMatcher(dir)
	if m.Ignored("dist", true) {
		t.Fatal(".ignore !dist/ should re-include")
	}
}

func TestIgnoreGlob(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n/tmpkeep\n"), 0o644)
	m := newIgnoreMatcher(dir)
	if !m.Ignored("foo.log", false) {
		t.Fatal("*.log should ignore")
	}
	if !m.Ignored("tmpkeep", false) {
		t.Fatal("/tmpkeep should ignore at root")
	}
}
