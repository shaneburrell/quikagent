package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"quikagent/internal/llm"
	"quikagent/internal/text"
)

const maxAdvertisedSkills = 40

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
	avail := formatAvailableSkills(listSkillNames(t.workdir, t.home))
	desc := "Load a named SKILL.md from .quikagent/skills/ or ~/.quikagent/skills/. Available skills: " + avail + "."
	if avail == "none" {
		desc += " Do not call this tool and do not invent names."
	} else {
		desc += " Call only with one of these names."
	}
	return llm.Tool{
		Name:        "skill",
		Description: desc,
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
	if !validSkillName(name) {
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
	return "", fmt.Errorf("skill %q not found. Available: %s", name, formatAvailableSkills(listSkillNames(t.workdir, t.home)))
}

func validSkillName(name string) bool {
	return name != "" && !strings.Contains(name, "..") && !strings.ContainsAny(name, `/\`)
}

func formatAvailableSkills(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	if len(names) > maxAdvertisedSkills {
		names = names[:maxAdvertisedSkills]
	}
	return strings.Join(names, ", ")
}

// listSkillNames returns project skills first (on collision), then home,
// then a stable sort. Invalid names are skipped.
func listSkillNames(workdir, home string) []string {
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		if !validSkillName(name) || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	if workdir != "" {
		scanSkillsDir(workdir, add)
	}
	if home != "" {
		scanSkillsDir(home, add)
	}
	sort.Strings(names)
	return names
}

func scanSkillsDir(root string, add func(string)) {
	dir := filepath.Join(root, ".quikagent", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
				add(e.Name())
			}
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		base := strings.TrimSuffix(name, ".md")
		if strings.EqualFold(base, "SKILL") {
			continue
		}
		add(base)
	}
}
