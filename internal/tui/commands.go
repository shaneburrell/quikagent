package tui

import (
	"os"
	"path/filepath"
	"strings"
)

type projectCommand struct {
	Name   string
	Prompt string
}

func loadProjectCommands(workdir string) []projectCommand {
	if workdir == "" {
		return nil
	}
	dir := filepath.Join(workdir, ".quikagent", "commands")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []projectCommand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == "" {
			continue
		}
		out = append(out, projectCommand{Name: name, Prompt: strings.TrimSpace(string(data))})
	}
	return out
}

func lookupProjectCommand(workdir, name string) (string, bool) {
	for _, c := range loadProjectCommands(workdir) {
		if strings.EqualFold(c.Name, name) {
			return c.Prompt, true
		}
	}
	return "", false
}

const defaultAgentsMD = `# AGENTS.md

Project guidance for coding agents.

- Prefer small, focused changes.
- Run tests after edits when a check exists.
- Do not commit or push unless asked.
`

func writeAgentsMD(workdir string) (created bool, err error) {
	if workdir == "" {
		return false, os.ErrInvalid
	}
	path := filepath.Join(workdir, "AGENTS.md")
	if _, err := os.Lstat(path); err == nil {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(defaultAgentsMD), 0o644)
}
