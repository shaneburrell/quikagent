package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runPatch(t *testing.T, dir, patch string) (string, error) {
	t.Helper()
	tool := newApplyPatch(dir)
	return tool.Run(context.Background(), mustJSON(map[string]string{"patch": patch}))
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestApplyPatchAddUpdateDeleteMove(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("bye\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: nested/new.txt",
		"+fresh",
		"*** Update File: old.txt",
		"*** Move to: renamed.txt",
		"@@",
		"-hello world",
		"+hello there",
		"*** Delete File: gone.txt",
		"*** End Patch",
	}, "\n")

	out, err := runPatch(t, dir, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 added") || !strings.Contains(out, "1 updated") || !strings.Contains(out, "1 deleted") {
		t.Fatalf("summary = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "nested", "new.txt")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "renamed.txt"))
	if !strings.Contains(string(data), "hello there") {
		t.Fatalf("renamed = %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("old.txt should be gone after move")
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); !os.IsNotExist(err) {
		t.Fatal("gone.txt should be deleted")
	}
}

func TestApplyPatchHunkNotFound(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("aaa\n"), 0o644)
	patch := "*** Begin Patch\n*** Update File: f.txt\n@@\n-zzz\n+yyy\n*** End Patch"
	_, err := runPatch(t, dir, patch)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyPatchMalformed(t *testing.T) {
	_, err := runPatch(t, t.TempDir(), "not a patch")
	if err == nil {
		t.Fatal("expected malformed error")
	}
}

func TestApplyPatchSandbox(t *testing.T) {
	dir := t.TempDir()
	_, err := runPatch(t, dir, "*** Begin Patch\n*** Add File: ../x.txt\n+no\n*** End Patch")
	if err == nil {
		t.Fatal("expected escape error")
	}
}

func TestApplyPatchInsertOnlyOnNonEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: f.txt",
		"@@",
		"+world",
		"*** End Patch",
	}, "\n")
	if _, err := runPatch(t, dir, patch); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\nworld\n" {
		t.Fatalf("got %q", data)
	}
}

func TestApplyPatchAmbiguousHunk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("foo\nbar\nfoo\nbar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: f.txt",
		"@@",
		"-foo",
		"-bar",
		"+baz",
		"*** End Patch",
	}, "\n")
	_, err := runPatch(t, dir, patch)
	if err == nil || !strings.Contains(err.Error(), "multiple locations") {
		t.Fatalf("err = %v, want ambiguous hunk", err)
	}
}

func TestApplyPatchRollbackOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro.txt")
	if err := os.WriteFile(ro, []byte("keep\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o644) })

	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: first.txt",
		"+hello",
		"*** Update File: ro.txt",
		"@@",
		"-keep",
		"+changed",
		"*** End Patch",
	}, "\n")
	if _, err := runPatch(t, dir, patch); err == nil {
		t.Fatal("expected write failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "first.txt")); !os.IsNotExist(err) {
		t.Fatal("first.txt should be rolled back after mid-apply failure")
	}
	data, err := os.ReadFile(ro)
	if err != nil || string(data) != "keep\n" {
		t.Fatalf("ro.txt = %q err=%v", data, err)
	}
}

func TestApplyMoveRollbackDestWhenRemoveFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "srcdir")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "stuck"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "renamed.txt")
	if err := applyMove(src, dest, []byte("hello there\n")); err == nil {
		t.Fatal("expected remove of non-empty dir to fail")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("dest should be rolled back when src remove fails")
	}
}

func TestApplyPatchAtomic(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep\n"), 0o644)
	// second section fails (hunk not found); first add must not land
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: first.txt",
		"+hello",
		"*** Update File: keep.txt",
		"@@",
		"-missing",
		"+nope",
		"*** End Patch",
	}, "\n")
	_, err := runPatch(t, dir, patch)
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "first.txt")); !os.IsNotExist(err) {
		t.Fatal("first.txt should not exist after failed patch")
	}
}
