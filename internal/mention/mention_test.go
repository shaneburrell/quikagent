package mention

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpand(t *testing.T) {
	workdir := t.TempDir()
	outside := t.TempDir()

	writeFile(t, filepath.Join(workdir, "test.txt"), "hello world")
	writeFile(t, filepath.Join(workdir, "big.txt"), strings.Repeat("x", maxContentBytes+100))
	if err := os.Mkdir(filepath.Join(workdir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workdir, "subdir", "a.txt"), "a")
	writeFile(t, filepath.Join(outside, "secret.txt"), "secret")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(workdir, "escape")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		in           string
		want         []string // substrings that must appear
		notWant      []string // substrings that must not appear
		wantSameAsIn bool
	}{
		{
			name: "file expansion",
			in:   "Check @test.txt for details",
			want: []string{`<file path="test.txt">`, "hello world", "</file>"},
		},
		{
			name: "directory expansion",
			in:   "Check @subdir for contents",
			want: []string{`<dir path="subdir">`, "a.txt", "</dir>"},
		},
		{
			name:         "missing path left as-is",
			in:           "Check @nonexistent for details",
			wantSameAsIn: true,
		},
		{
			name:         "path escape left as-is",
			in:           "Check @../secret for details",
			wantSameAsIn: true,
		},
		{
			name:         "symlink escaping workdir left as-is",
			in:           "Check @escape for details",
			notWant:      []string{"secret"},
			wantSameAsIn: true,
		},
		{
			name:         "bare @ left as-is",
			in:           "email me @ home",
			wantSameAsIn: true,
		},
		{
			name: "content cap truncation",
			in:   "Check @big.txt",
			want: []string{`<file path="big.txt">`, "... [truncated, showing first 32768 characters] ..."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(t.Context(), workdir, tt.in)
			if err != nil {
				t.Fatalf("Expand(%q) error: %v", tt.in, err)
			}
			if tt.wantSameAsIn && got != tt.in {
				t.Errorf("Expand(%q) = %q, want unchanged", tt.in, got)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("Expand(%q) missing %q, got:\n%s", tt.in, want, got)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("Expand(%q) unexpectedly contains %q, got:\n%s", tt.in, notWant, got)
				}
			}
		})
	}
}

func TestExpandGitInRepo(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()

	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	writeFile(t, filepath.Join(repo, "tracked.txt"), "one\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	writeFile(t, filepath.Join(repo, "tracked.txt"), "one\ntwo\n")
	writeFile(t, filepath.Join(repo, "untracked.txt"), "new\n")

	got, err := Expand(t.Context(), repo, "state: @git done")
	if err != nil {
		t.Fatalf("Expand error: %v", err)
	}
	for _, want := range []string{"<git>", "</git>", "tracked.txt", "untracked.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in git expansion, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "@git") {
		t.Errorf("@git token was not expanded, got:\n%s", got)
	}
}

func TestExpandGitNonRepo(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()

	in := "state: @git done"
	got, err := Expand(t.Context(), dir, in)
	if err != nil {
		t.Fatalf("Expand error: %v", err)
	}
	if got != in {
		t.Errorf("Expand(%q) = %q, want unchanged in non-repo", in, got)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
