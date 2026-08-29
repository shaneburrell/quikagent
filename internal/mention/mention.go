// Package mention expands @mentions in user text: @path becomes file
// contents or a directory listing, and @git becomes a git status summary.
package mention

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"quikagent/internal/text"
)

const (
	maxContentBytes = 32 * 1024
	maxDirEntries   = 200
)

// Expand resolves @mentions in text. The token "git" expands to a <git>
// block with git status and diff summary for workdir. Any other token is
// treated as a path relative to workdir and expands to file contents or a
// directory listing. Tokens that fail to resolve, escape workdir (including
// via symlinks), or hit errors are left unchanged.
func Expand(ctx context.Context, workdir, text string) (string, error) {
	var result strings.Builder
	i := 0
	for i < len(text) {
		if text[i] != '@' {
			result.WriteByte(text[i])
			i++
			continue
		}
		start := i + 1
		end := start
		for end < len(text) && !isWhitespace(text[end]) {
			end++
		}
		if end == start {
			// Bare @ - leave as-is.
			result.WriteByte('@')
		} else {
			result.WriteString(expandToken(ctx, workdir, text[start:end], text[i:end]))
		}
		i = end
	}
	return result.String(), nil
}

// expandToken returns the replacement for one @token, or raw (the original
// "@token" text) if the token cannot be expanded.
func expandToken(ctx context.Context, workdir, token, raw string) string {
	// Special-case @git before path resolution so a real .git directory
	// never shadows it.
	if token == "git" {
		if out, ok := expandGit(ctx, workdir); ok {
			return out
		}
		return raw
	}

	path := filepath.Join(workdir, token)
	rel, err := filepath.Rel(workdir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return raw
	}
	if !containedIn(workdir, path) {
		return raw
	}

	info, err := os.Stat(path)
	if err != nil {
		return raw
	}
	if info.IsDir() {
		if out, ok := expandDir(token, path); ok {
			return out
		}
		return raw
	}
	if out, ok := expandFile(token, path); ok {
		return out
	}
	return raw
}

// containedIn reports whether path resolves (following symlinks) to a
// location strictly inside workdir.
func containedIn(workdir, path string) bool {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	realWorkdir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return false
	}
	return strings.HasPrefix(realPath, realWorkdir+string(filepath.Separator))
}

// expandGit runs git status and diff summary in workdir and wraps the
// output in a <git> block, capped at maxContentBytes. It reports false if
// git is unavailable or workdir is not a repository.
func expandGit(ctx context.Context, workdir string) (string, bool) {
	status, err := gitOutput(ctx, workdir, "status", "--porcelain")
	if err != nil {
		return "", false
	}
	diff, err := gitOutput(ctx, workdir, "diff", "--stat")
	if err != nil {
		return "", false
	}
	body := truncate(status + diff)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return fmt.Sprintf("<git>\n%s</git>\n", body), true
}

func gitOutput(ctx context.Context, workdir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// expandDir lists directory entries, capped at maxDirEntries.
func expandDir(token, path string) (string, bool) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<dir path=%q>\n", token)
	for i, entry := range entries {
		if i >= maxDirEntries {
			b.WriteString("... (truncated)\n")
			break
		}
		fmt.Fprintf(&b, "  %s\n", entry.Name())
	}
	b.WriteString("</dir>\n")
	return b.String(), true
}

// expandFile reads file contents, capped at maxContentBytes.
func expandFile(token, path string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("<file path=%q>\n%s</file>\n", token, truncate(string(content))), true
}

func truncate(s string) string {
	out := text.ClipRunes(s, maxContentBytes)
	if out == s {
		return s
	}
	return out + fmt.Sprintf("\n... [truncated, showing first %d characters] ...", maxContentBytes)
}

func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
