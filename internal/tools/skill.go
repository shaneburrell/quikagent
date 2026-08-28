package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"quikagent/internal/llm"
	"quikagent/internal/text"
)

type skillTool struct {
	workdir string
	home    string
}

// NewSkill loads SKILL.md files from .quikagent/skills and ~/.quikagent/skills.
func NewSkill(workdir string) *skillTool {
	home, _ := os.UserHomeDir()
	return &skillTool{workdir: workdir, home: home}
}

func (t *skillTool) ReadOnly() bool { return true }

func (t *skillTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "skill",
		Description: "Load a skill (SKILL.md) by name from .quikagent/skills/<name>/ or ~/.quikagent/skills/<name>/. Returns the skill body.",
		Parameters:  []byte(`{"type":"object","properties":{"name":{"type":"string","description":"Skill name (directory name)"}},"required":["name"]}`),
	}
}

func (t *skillTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	name := strings.TrimSpace(a.Name)
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return "", errInvalidArg("invalid skill name")
	}
	candidates := []string{
		filepath.Join(t.workdir, ".quikagent", "skills", name, "SKILL.md"),
		filepath.Join(t.workdir, ".quikagent", "skills", name+".md"),
	}
	if t.home != "" {
		candidates = append(candidates,
			filepath.Join(t.home, ".quikagent", "skills", name, "SKILL.md"),
			filepath.Join(t.home, ".quikagent", "skills", name+".md"),
		)
	}
	for _, p := range candidates {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		return text.ClipRunes(string(data), MaxOutput), nil
	}
	return "", fmt.Errorf("skill %q not found", name)
}
