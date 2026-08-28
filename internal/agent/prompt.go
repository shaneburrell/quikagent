package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Options configures an Agent.
type Options struct {
	Workdir   string
	Model     string
	MaxTokens int
}

// Mode selects the tool surface. Build exposes all tools; Plan is
// read-only.
type Mode int

const (
	Build Mode = iota
	Plan
)

// maxAgentsMDBytes caps AGENTS.md content appended to the system prompt.
const maxAgentsMDBytes = 32 * 1024

func (m Mode) String() string {
	if m == Plan {
		return "plan"
	}
	return "build"
}

// systemPrompt builds the leading system message, mirroring the
// opencode-style environment context.
func systemPrompt(opts Options, mode Mode) string {
	var b strings.Builder
	b.WriteString("You are quikagent, an interactive CLI coding agent. You help users with software engineering tasks in their workspace.\n\n")
	b.WriteString("# Environment\n")
	fmt.Fprintf(&b, "- Working directory: %s (all relative paths resolve against it)\n", opts.Workdir)
	fmt.Fprintf(&b, "- Platform: %s\n", runtime.GOOS)
	fmt.Fprintf(&b, "- Date: %s\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&b, "- Model: %s\n\n", opts.Model)
	if mode == Plan {
		b.WriteString("# Plan mode\nYou are in plan mode. Do NOT modify anything: you only have read-only tools (read, glob, grep, list, fetch, git, question, and websearch if configured). Investigate, then propose a concrete step-by-step plan. Do not write to files.\n\n")
	}
	b.WriteString("# Guidelines\n")
	b.WriteString("- Use the tools to do real work; read files before editing them.\n")
	b.WriteString("- Keep replies concise; this is a terminal, not an essay.\n")
	b.WriteString("- Use bash for builds, tests, and git; verify changes when a check exists.\n")
	b.WriteString("- Never commit or push unless explicitly asked.\n")
	b.WriteString("- If a task is ambiguous, state your assumption briefly and proceed.\n")
	if agents := loadAgentsMD(opts.Workdir); agents != "" {
		b.WriteString("\n# Project AGENTS.md\n")
		b.WriteString(agents)
		if !strings.HasSuffix(agents, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// loadAgentsMD reads Workdir/AGENTS.md if present, truncated to maxAgentsMDBytes.
func loadAgentsMD(workdir string) string {
	if workdir == "" {
		return ""
	}
	path := filepath.Join(workdir, "AGENTS.md")
	fi, err := os.Lstat(path)
	if err != nil {
		return ""
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		// Resolve and require target under workdir.
		if _, err := resolveAgentsPath(workdir, path); err != nil {
			return ""
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > maxAgentsMDBytes {
		data = data[:maxAgentsMDBytes]
	}
	return string(data)
}

func resolveAgentsPath(workdir, path string) (string, error) {
	root, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	target = filepath.Clean(target)
	root = filepath.Clean(root)
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("AGENTS.md symlink escapes workdir")
	}
	return target, nil
}
