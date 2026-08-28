package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"quikagent/internal/llm"
)

const subagentMaxSteps = 20

type customAgent struct {
	ID       string
	Prompt   string
	ReadOnly bool
}

func (a *Agent) runSubagent(ctx context.Context, agentID, prompt string) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(agentID))
	if kind == "" {
		kind = "general"
	}

	childTools := a.tools
	mode := Build
	prefix := ""
	switch kind {
	case "explore":
		childTools = a.tools.ReadOnly()
		mode = Plan
		prefix = "You are an explore subagent. Only read and search; do not modify files.\n\n"
	case "general":
		prefix = "You are a general-purpose subagent. Complete the assigned task and return a concise result.\n\n"
	default:
		def, ok := loadCustomAgent(a.opts.Workdir, kind)
		if !ok {
			return "", fmt.Errorf("unknown subagent %q (try explore or general)", kind)
		}
		prefix = def.Prompt + "\n\n"
		if def.ReadOnly {
			childTools = a.tools.ReadOnly()
			mode = Plan
		}
	}

	// Drop task from the child so it cannot recurse.
	childTools = childTools.Without("task")

	child := New(a.llm, childTools, a.opts)
	// New() re-adds task if missing; drop it again so children cannot recurse.
	if child.tools != nil {
		child.tools = child.tools.Without("task")
	}
	child.stepLimit = subagentMaxSteps
	child.SetMode(mode)
	child.SetAllowTool(a.allowTool)
	child.SetSummarizer(a.summarizer)

	ev := make(chan Event, 64)
	go child.Run(ctx, prefix+prompt, ev)
	var last string
	for e := range ev {
		switch e.Type {
		case EventText:
			last += e.Text
		case EventError:
			if e.Err != nil {
				return "", e.Err
			}
			return "", fmt.Errorf("subagent error")
		}
	}
	if strings.TrimSpace(last) == "" {
		// fall back to last assistant message
		for _, m := range child.Messages() {
			if m.Role == llm.RoleAssistant && m.Content != "" {
				last = m.Content
			}
		}
	}
	if strings.TrimSpace(last) == "" {
		return "(subagent finished with no text)", nil
	}
	return last, nil
}

func loadCustomAgent(workdir, id string) (customAgent, bool) {
	if workdir == "" {
		return customAgent{}, false
	}
	dir := filepath.Join(workdir, ".quikagent", "agents")
	matches := []string{
		filepath.Join(dir, id+".md"),
		filepath.Join(dir, id),
	}
	var data []byte
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err == nil {
			data = b
			break
		}
	}
	if data == nil {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return customAgent{}, false
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			def := parseAgentMD(e.Name(), string(b))
			if strings.EqualFold(def.ID, id) {
				return def, true
			}
		}
		return customAgent{}, false
	}
	return parseAgentMD(id+".md", string(data)), true
}

func parseAgentMD(filename, body string) customAgent {
	id := strings.TrimSuffix(filename, ".md")
	def := customAgent{ID: id, Prompt: strings.TrimSpace(body)}
	if !strings.HasPrefix(body, "---") {
		return def
	}
	rest := strings.TrimPrefix(body, "---")
	sc := bufio.NewScanner(strings.NewReader(rest))
	var fm []string
	closed := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		fm = append(fm, line)
	}
	if !closed {
		return def
	}
	var leftover strings.Builder
	for sc.Scan() {
		leftover.WriteString(sc.Text())
		leftover.WriteByte('\n')
	}
	for _, line := range fm {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch k {
		case "name", "id":
			if v != "" {
				def.ID = v
			}
		case "readonly", "read_only":
			def.ReadOnly = v == "true" || v == "1" || v == "yes"
		}
	}
	if p := strings.TrimSpace(leftover.String()); p != "" {
		def.Prompt = p
	}
	return def
}
