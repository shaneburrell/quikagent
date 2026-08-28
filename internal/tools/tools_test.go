package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"quikagent/internal/llm"
)

func run(t *testing.T, r *Registry, name string, args any) (string, error) {
	t.Helper()
	tool, ok := r.Get(name)
	if !ok {
		t.Fatalf("tool %s not registered", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Run(context.Background(), raw)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hello.txt"), "hello\n")
	r := New(dir)

	out, err := run(t, r, "bash", map[string]any{"command": "cat hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello\n" {
		t.Fatalf("out = %q", out)
	}

	out, err = run(t, r, "bash", map[string]any{"command": "false"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Exit code 1") {
		t.Fatalf("out = %q", out)
	}

	out, err = run(t, r, "bash", map[string]any{"command": "pwd"})
	wantDir, _ := filepath.EvalSymlinks(dir)
	gotDir := strings.TrimSuffix(out, "\n")
	if err != nil || gotDir != wantDir && gotDir != dir {
		t.Fatalf("cwd out = %q want %q or %q err = %v", out, wantDir, dir, err)
	}
}

func TestBashTimeout(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	out, err := run(t, r, "bash", map[string]any{"command": "sleep 5", "timeout": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("out = %q", out)
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "one\ntwo\nthree\n")
	r := New(dir)

	out, err := run(t, r, "read", map[string]any{"path": "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	want := "1: one\n2: two\n3: three"
	if out != want {
		t.Fatalf("out = %q", out)
	}

	out, err = run(t, r, "read", map[string]any{"path": "a.txt", "offset": 2, "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if out != "2: two" {
		t.Fatalf("out = %q", out)
	}

	if _, err = run(t, r, "read", map[string]any{"path": "nope.txt"}); err == nil {
		t.Fatal("expected error for missing file")
	}
	if _, err = run(t, r, "read", map[string]any{"path": "a.txt", "offset": 99}); err == nil {
		t.Fatal("expected error for offset beyond EOF")
	}
}

func TestReadDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "a.txt"), "x")
	writeFile(t, filepath.Join(dir, "b.txt"), "y")
	r := New(dir)
	out, err := run(t, r, "read", map[string]any{"path": "."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sub/") || !strings.Contains(out, "b.txt") {
		t.Fatalf("listing = %q", out)
	}
}

func TestReadDirRespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "secret.txt\nbuild/\n")
	writeFile(t, filepath.Join(dir, "readme.md"), "hi")
	writeFile(t, filepath.Join(dir, "secret.txt"), "no")
	writeFile(t, filepath.Join(dir, "build", "out"), "x")
	r := New(dir)
	out, err := run(t, r, "read", map[string]any{"path": "."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "readme.md") {
		t.Fatalf("listing missing readme: %q", out)
	}
	if strings.Contains(out, "secret.txt") || strings.Contains(out, "build/") {
		t.Fatalf("gitignore entries leaked: %q", out)
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)

	out, err := run(t, r, "write", map[string]any{"path": "deep/dir/file.txt", "content": "data"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Wrote deep/dir/file.txt" {
		t.Fatalf("out = %q", out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "deep/dir/file.txt"))
	if err != nil || string(got) != "data" {
		t.Fatalf("file = %q err = %v", got, err)
	}
}

func TestWriteEmptyTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	writeFile(t, path, "old")
	r := New(dir)
	if _, err := run(t, r, "write", map[string]any{"path": "f.txt", "content": ""}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("file = %q err = %v", got, err)
	}
}

func TestWritePreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(dir)
	if _, err := run(t, r, "write", map[string]any{"path": "f.txt", "content": "new"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %o, want 644", st.Mode().Perm())
	}
}

func TestWriteEditReadHonorContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "f.txt"), "hello")
	r := New(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		args json.RawMessage
	}{
		{"write", json.RawMessage(`{"path":"x.txt","content":"no"}`)},
		{"edit", json.RawMessage(`{"path":"f.txt","old_string":"hello","new_string":"x"}`)},
		{"read", json.RawMessage(`{"path":"f.txt"}`)},
	} {
		tool, ok := r.Get(tc.name)
		if !ok {
			t.Fatalf("missing %s", tc.name)
		}
		if _, err := tool.Run(ctx, tc.args); err == nil {
			t.Fatalf("%s should honor canceled ctx", tc.name)
		}
	}
}

func TestEditPreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("func foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(dir)
	if _, err := run(t, r, "edit", map[string]any{
		"path": "a.go", "old_string": "foo", "new_string": "Foo",
	}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %o, want 644", st.Mode().Perm())
	}
}

func TestEdit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package main\n\nfunc foo() {}\nfunc bar() {}\n")
	r := New(dir)

	out, err := run(t, r, "edit", map[string]any{
		"path": "a.go", "old_string": "func foo() {}", "new_string": "func Foo() {}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Edited a.go" {
		t.Fatalf("out = %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if !strings.Contains(string(got), "func Foo() {}") {
		t.Fatalf("file = %q", got)
	}

	// Ambiguous match without replace_all must fail.
	if _, err = run(t, r, "edit", map[string]any{
		"path": "a.go", "old_string": "func ", "new_string": "func",
	}); err == nil {
		t.Fatal("expected ambiguity error")
	}

	out, err = run(t, r, "edit", map[string]any{
		"path": "a.go", "old_string": "func ", "new_string": "func", "replace_all": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Replaced") {
		t.Fatalf("out = %q", out)
	}

	if _, err = run(t, r, "edit", map[string]any{
		"path": "a.go", "old_string": "absent", "new_string": "x",
	}); err == nil {
		t.Fatal("expected not-found error")
	}
	if _, err = run(t, r, "edit", map[string]any{
		"path": "a.go", "old_string": "x", "new_string": "x",
	}); err == nil {
		t.Fatal("expected same-string error")
	}
}

func TestGlob(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "x")
	writeFile(t, filepath.Join(dir, "pkg", "a.go"), "x")
	writeFile(t, filepath.Join(dir, "pkg", "deep", "b.go"), "x")
	writeFile(t, filepath.Join(dir, "pkg", "notes.txt"), "x")
	writeFile(t, filepath.Join(dir, "node_modules", "junk.go"), "x")
	r := New(dir)

	out, err := run(t, r, "glob", map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main.go", "pkg/a.go", "pkg/deep/b.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %q", want, out)
		}
	}
	if strings.Contains(out, "node_modules") {
		t.Fatalf("node_modules leaked into results: %q", out)
	}

	out, err = run(t, r, "glob", map[string]any{"pattern": "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "pkg/") {
		t.Fatalf("non-recursive pattern matched nested: %q", out)
	}

	out, err = run(t, r, "glob", map[string]any{"pattern": "**/*.xyz"})
	if err != nil || out != "No files found." {
		t.Fatalf("out = %q err = %v", out, err)
	}
}

func TestGlobRespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "secret.txt\nbuild/\n*.log\n")
	writeFile(t, filepath.Join(dir, "readme.md"), "hi")
	writeFile(t, filepath.Join(dir, "secret.txt"), "no")
	writeFile(t, filepath.Join(dir, "notes.log"), "no")
	writeFile(t, filepath.Join(dir, "src", "main.go"), "x")
	writeFile(t, filepath.Join(dir, "build", "out.go"), "x")
	r := New(dir)

	out, err := run(t, r, "glob", map[string]any{"pattern": "**/*"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "readme.md") || !strings.Contains(out, "src/main.go") {
		t.Fatalf("expected visible files in %q", out)
	}
	for _, leaked := range []string{"secret.txt", "notes.log", "build/"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("gitignore entry %q leaked: %q", leaked, out)
		}
	}

	out, err = run(t, r, "glob", map[string]any{"pattern": "secret.txt"})
	if err != nil || out != "No files found." {
		t.Fatalf("ignored basename still matched: out=%q err=%v", out, err)
	}

	l := newList(dir)
	listing, err := l.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listing, "secret.txt") || strings.Contains(listing, "build/") || strings.Contains(listing, "notes.log") {
		t.Fatalf("list ignored entries leaked: %q", listing)
	}
}

func TestGrep(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package main\nfunc target() {}\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "target here\n")
	writeFile(t, filepath.Join(dir, "vendor", "v.go"), "target\n")
	r := New(dir)

	out, err := run(t, r, "grep", map[string]any{"pattern": "target"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.go:2: func target() {}") || !strings.Contains(out, "b.txt:1: target here") {
		t.Fatalf("out = %q", out)
	}
	if strings.Contains(out, "vendor") {
		t.Fatalf("vendor leaked: %q", out)
	}

	out, err = run(t, r, "grep", map[string]any{"pattern": "target", "include": "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "b.txt") {
		t.Fatalf("include filter failed: %q", out)
	}

	out, err = run(t, r, "grep", map[string]any{"pattern": "zzz-absent"})
	if err != nil || out != "No matches found." {
		t.Fatalf("out = %q err = %v", out, err)
	}

	if _, err = run(t, r, "grep", map[string]any{"pattern": "(["}); err == nil {
		t.Fatal("expected bad regex error")
	}
}

func TestGrepSurfacesPerFileErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ok.go"), "target\n")
	blocked := filepath.Join(dir, "blocked.txt")
	writeFile(t, blocked, "target\n")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o644) })
	r := New(dir)
	out, err := run(t, r, "grep", map[string]any{"pattern": "target"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok.go") {
		t.Fatalf("missing match: %q", out)
	}
	if !strings.Contains(out, "Search errors:") || !strings.Contains(out, "blocked.txt") {
		t.Fatalf("per-file error not surfaced: %q", out)
	}
}

func TestGlobSurfacesWalkErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ok.go"), "x")
	blocked := filepath.Join(dir, "hidden")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(blocked, "x.go"), "x")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	r := New(dir)
	out, err := run(t, r, "glob", map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok.go") {
		t.Fatalf("missing match: %q", out)
	}
	if !strings.Contains(out, "error:") {
		t.Fatalf("walk error not surfaced: %q", out)
	}
}

func TestSandboxEscape(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "outside.txt")
	writeFile(t, secret, "secret")
	r := New(filepath.Join(dir, "sub"))

	if _, err := run(t, r, "read", map[string]any{"path": "../outside.txt"}); err == nil {
		t.Fatal("read escaped sandbox")
	}
	if _, err := run(t, r, "read", map[string]any{"path": secret}); err == nil {
		t.Fatal("absolute path escaped sandbox")
	}
	if _, err := run(t, r, "write", map[string]any{"path": "../outside2.txt", "content": "x"}); err == nil {
		t.Fatal("write escaped sandbox")
	}
	if _, err := run(t, r, "edit", map[string]any{"path": "../../outside.txt", "old_string": "a", "new_string": "b"}); err == nil {
		t.Fatal("edit escaped sandbox")
	}
}

func TestRegistryReadOnly(t *testing.T) {
	r := New(t.TempDir()).ReadOnly()
	names := []string{}
	for _, s := range r.List() {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "read,glob,grep,list,fetch,git,question" {
		t.Fatalf("read-only tools = %v", names)
	}
	if _, ok := r.Get("bash"); ok {
		t.Fatal("bash should be absent in read-only mode")
	}
}

func TestBashLooksMutating(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{`{"command":"git status"}`, false},
		{`{"command":"git commit -m x"}`, true},
		{`{"command":"git -C dir commit -m x"}`, true},
		{`{"command":"git -C /tmp/repo -c user.name=x commit -m hi"}`, true},
		{`{"command":"sed -i s/a/b/ file"}`, true},
		{`{"command":"find . -name '*.o' -delete"}`, true},
		{`{"command":"perl -pi -e s/a/b/ file"}`, true},
		{`{"command":"ls -la"}`, false},
	}
	for _, tt := range tests {
		if got := BashLooksMutating(tt.cmd); got != tt.want {
			t.Errorf("BashLooksMutating(%s)=%v want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestFetch(t *testing.T) {
	allowPrivateFetchForTest = true
	t.Cleanup(func() { allowPrivateFetchForTest = false })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>Hi</h1><p>Hello world</p></body></html>")
	}))
	defer srv.Close()

	r := New(t.TempDir())
	out, err := run(t, r, "fetch", map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "HTTP 200") || !strings.Contains(out, "Hello world") {
		t.Fatalf("out = %q", out)
	}
}

func TestFetchNon2xxIsError(t *testing.T) {
	allowPrivateFetchForTest = true
	t.Cleanup(func() { allowPrivateFetchForTest = false })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	r := New(t.TempDir())
	_, err := run(t, r, "fetch", map[string]any{"url": srv.URL})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("want HTTP 404 error, got %v", err)
	}
}

func TestBlockedFetchIPs(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1", "::1", "10.0.0.1", "192.168.1.1", "172.16.0.1",
		"169.254.169.254", "0.0.0.0",
	} {
		ip := net.ParseIP(raw)
		if ip == nil || !isBlockedIP(ip) {
			t.Errorf("%s should be blocked", raw)
		}
	}
	if isBlockedIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("1.1.1.1 should be allowed")
	}
}

func TestFetchDialPinsBlockedIP(t *testing.T) {
	tr := pinnedFetchTransport()
	_, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:80")
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("want blocked dial, got %v", err)
	}
}

func TestGitStatus(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Skip("git not available")
	}
	writeFile(t, filepath.Join(dir, "a.txt"), "x")
	r := New(dir)
	out, err := run(t, r, "git", map[string]any{"action": "status"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.txt") && !strings.Contains(out, "##") {
		t.Fatalf("status = %q", out)
	}
	if _, err := run(t, r, "git", map[string]any{"action": "commit"}); err == nil {
		t.Fatal("expected invalid action error")
	}
}

func TestReadBinary(t *testing.T) {
	dir := t.TempDir()
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	writeFile(t, filepath.Join(dir, "x.png"), string(png))
	r := New(dir)
	out, err := run(t, r, "read", map[string]any{"path": "x.png"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "image") || !strings.Contains(out, "content not shown") {
		t.Fatalf("out = %q", out)
	}
}

func TestRegistryAdd(t *testing.T) {
	r := New(t.TempDir())
	r.Add(&stubTool{name: "custom", readOnly: true})
	if _, ok := r.Get("custom"); !ok {
		t.Fatal("custom not added")
	}
	ro := r.ReadOnly()
	if _, ok := ro.Get("custom"); !ok {
		t.Fatal("custom should be in read-only set")
	}
}

type stubTool struct {
	name     string
	readOnly bool
}

func (s *stubTool) Spec() llm.Tool {
	return llm.Tool{Name: s.name, Description: "stub", Parameters: []byte(`{"type":"object","properties":{}}`)}
}
func (s *stubTool) Run(ctx context.Context, args json.RawMessage) (string, error) { return "ok", nil }
func (s *stubTool) ReadOnly() bool                                                { return s.readOnly }
