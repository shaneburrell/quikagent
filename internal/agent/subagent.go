package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"quikagent/internal/llm"
	"quikagent/internal/session"
)

const subagentMaxSteps = 20

const (
	reviewerPrefix = "You are a reviewer. Inspect the workspace (git diff if useful) and decide whether the assigned step was completed. Reply with PASS or FAIL and a short reason. Do not modify files.\n\n"
	explorePrefix  = "You are an explore subagent. Only read and search; do not modify files.\n\n"
	generalPrefix  = "You are a general-purpose subagent. Complete the assigned task and return a concise result.\n\n"
)

type customAgent struct {
	ID       string
	Prompt   string
	ReadOnly bool
	Model    string
}

type subagentReq struct {
	ID     string
	Prompt string
	Model  string
	StepID string
	Events chan<- Event
}

func (a *Agent) runSubagent(ctx context.Context, agentID, prompt, model string) (string, error) {
	if model == "" && a.RouterEnabled() {
		a.mu.RLock()
		r := a.router
		a.mu.RUnlock()
		route, m, _, err := r.Select(ctx, prompt, "build")
		if err == nil && route != "other" && m != "" {
			model = m
		} else {
			model = a.coderModel()
		}
	}
	return a.spawnSubagent(ctx, subagentReq{ID: agentID, Prompt: prompt, Model: model})
}

func (a *Agent) spawnSubagent(ctx context.Context, req subagentReq) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(req.ID))
	if kind == "" {
		kind = "general"
	}

	childTools := a.tools
	mode := Build
	var prefix string
	switch kind {
	case "explore":
		childTools = a.tools.ReadOnly()
		mode = Plan
		prefix = explorePrefix
	case "reviewer":
		childTools = a.tools.ReadOnly()
		mode = Plan
		prefix = reviewerPrefix
	case "general":
		prefix = generalPrefix
	default:
		def, ok := loadCustomAgent(a.opts.Workdir, kind)
		if !ok {
			return "", fmt.Errorf("unknown subagent %q (try explore, general, or reviewer)", kind)
		}
		prefix = def.Prompt + "\n\n"
		if def.ReadOnly {
			childTools = a.tools.ReadOnly()
			mode = Plan
		}
		if req.Model == "" && def.Model != "" {
			req.Model = def.Model
		}
	}

	childTools = childTools.Without("task", "plan")

	parentModel := a.Model()
	child := New(a.llm, childTools, a.opts)
	if child.tools != nil {
		child.tools = child.tools.Without("task", "plan")
	}
	child.stepLimit = subagentMaxSteps
	child.SetMode(mode)
	child.SetAllowTool(a.allowTool)
	child.SetSummarizer(a.summarizer)
	a.mu.RLock()
	fn, fe := a.trace, a.traceFrontend
	a.mu.RUnlock()
	child.SetTrace(fn)
	child.SetTraceFrontend(fe)
	if req.Model != "" {
		child.setModel(req.Model, false)
	}

	ev := make(chan Event, 64)
	go child.Run(ctx, prefix+req.Prompt, ev)
	var last string
	for e := range ev {
		switch e.Type {
		case EventText:
			last += e.Text
			if req.Events != nil {
				req.Events <- e
			}
		case EventToolStart, EventToolDone, EventRoute, EventThinking, EventStatus:
			if req.Events != nil {
				req.Events <- e
			}
		case EventError:
			a.setModel(parentModel, false)
			if e.Err != nil {
				return "", e.Err
			}
			return "", fmt.Errorf("subagent error")
		}
	}
	a.setModel(parentModel, false)
	if req.StepID != "" {
		a.emitTrace(session.TraceRecord{Type: "dispatch", Name: kind, Model: req.Model, StepID: req.StepID})
	}
	if strings.TrimSpace(last) == "" {
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
		case "model":
			def.Model = v
		}
	}
	if p := strings.TrimSpace(leftover.String()); p != "" {
		def.Prompt = p
	}
	return def
}
