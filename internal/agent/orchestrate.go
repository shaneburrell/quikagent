package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"quikagent/internal/llm"
	"quikagent/internal/session"
	"quikagent/internal/tools"
)

const maxParallelWorkers = 3

func (a *Agent) shouldOrchestrate(userText string) bool {
	if a.ModelPinned() {
		return false
	}
	if !isHandoffText(userText) {
		return false
	}
	a.mu.RLock()
	has := a.plan.HasDispatchableWork()
	a.mu.RUnlock()
	return has
}

func (a *Agent) runOrchestrated(ctx context.Context, ev chan<- Event, steps *int, turnErr *error) {
	emitStatus(ev, "dispatch")
	var log strings.Builder
	for {
		if ctx.Err() != nil {
			*turnErr = ctx.Err()
			ev <- Event{Type: EventError, Err: ctx.Err()}
			return
		}
		batch := a.nextWave()
		if len(batch) == 0 {
			break
		}
		type result struct {
			id    string
			out   string
			err   error
			route string
			model string
		}
		results := make([]result, len(batch))
		var wg sync.WaitGroup
		for i, step := range batch {
			wg.Add(1)
			go func() {
				defer wg.Done()
				route, model := a.routeStep(ctx, ev, step)
				a.setStepStatus(step.ID, "in_progress", ev)
				prompt := workerPrompt(step)
				out, err := a.spawnSubagent(ctx, subagentReq{
					ID: "general", Prompt: prompt, Model: model, StepID: step.ID,
					Files: step.Files, Events: ev,
				})
				results[i] = result{id: step.ID, out: out, err: err, route: route, model: model}
			}()
		}
		wg.Wait()
		*steps += len(batch)

		var okIdx []int
		for i, res := range results {
			step := batch[i]
			if res.err != nil {
				a.setStepStatus(res.id, "failed", ev)
				fmt.Fprintf(&log, "- %s (%s): failed: %v\n", step.Title, res.route, res.err)
				continue
			}
			okIdx = append(okIdx, i)
		}
		if len(okIdx) > 0 {
			okSteps := make([]tools.PlanStep, len(okIdx))
			okOuts := make([]string, len(okIdx))
			for j, i := range okIdx {
				okSteps[j] = batch[i]
				okOuts[j] = results[i].out
			}
			revOut, revErr := a.reviewWave(ctx, ev, okSteps, okOuts)
			if revErr != nil || reviewFailed(revOut) {
				reason := "review FAIL"
				if revErr != nil {
					reason = "review error: " + revErr.Error()
				} else {
					reason += ": " + clip(revOut, 200)
				}
				for _, i := range okIdx {
					step := batch[i]
					a.setStepStatus(step.ID, "failed", ev)
					fmt.Fprintf(&log, "- %s (%s): %s\n", step.Title, results[i].route, reason)
				}
			} else {
				for _, i := range okIdx {
					step := batch[i]
					res := results[i]
					a.setStepStatus(step.ID, "done", ev)
					fmt.Fprintf(&log, "- %s (%s / %s): ok\n", step.Title, res.route, res.model)
					fmt.Fprintf(&log, "  %s\n", clip(res.out, 400))
				}
			}
		}
	}
	a.noteConfirmPending(&log)

	summary := a.summarizeDispatch(ctx, ev, log.String())
	if summary != "" {
		a.mu.Lock()
		a.messages = append(a.messages, llm.Message{Role: llm.RoleAssistant, Content: summary})
		a.mu.Unlock()
	}
	ev <- Event{Type: EventTurnDone}
}

func (a *Agent) noteConfirmPending(log *strings.Builder) {
	a.mu.RLock()
	p := a.plan
	a.mu.RUnlock()
	for _, s := range p.Steps {
		if s.Confirm && (s.Status == "pending" || s.Status == "failed") {
			fmt.Fprintf(log, "- %s: left pending (needs confirmation)\n", s.Title)
		}
	}
}

func (a *Agent) routeStep(ctx context.Context, ev chan<- Event, step tools.PlanStep) (route, model string) {
	text := strings.TrimSpace(step.Title + "\n" + step.Detail)
	a.mu.RLock()
	r := a.router
	a.mu.RUnlock()
	if r != nil {
		rt, m, _, err := r.Select(ctx, text, "build")
		_ = err
		if rt == "other" || m == "" {
			rt = "coder"
			m = a.coderModel()
		}
		route, model = rt, m
	} else {
		route, model = "coder", a.coderModel()
	}
	label := fmt.Sprintf("dispatch %s → %s", step.ID, route)
	if ev != nil {
		ev <- Event{Type: EventRoute, Name: route, Model: model, Text: label, StepID: step.ID}
	}
	a.emitTrace(session.TraceRecord{Type: "dispatch", Route: route, Model: model, StepID: step.ID, Name: step.Title})
	return route, model
}

func (a *Agent) reviewerModel() string {
	a.mu.RLock()
	small := a.opts.SmallModel
	a.mu.RUnlock()
	if small != "" {
		return small
	}
	if rm, ok := a.router.(interface{ RouteModel(string) string }); ok {
		if m := rm.RouteModel("nano"); m != "" {
			return m
		}
	}
	return a.coderModel()
}

func (a *Agent) reviewWave(ctx context.Context, ev chan<- Event, batch []tools.PlanStep, outs []string) (string, error) {
	var b strings.Builder
	b.WriteString("Reply with PASS or FAIL on the first line (nothing before it). Do not call question.\n\n")
	ids := make([]string, 0, len(batch))
	for i, step := range batch {
		ids = append(ids, step.ID)
		fmt.Fprintf(&b, "Step %s (%s):\n%s\n\nWorker result:\n%s\n\n", step.ID, step.Title, step.Detail, clip(outs[i], 1500))
	}
	model := a.reviewerModel()
	sid := strings.Join(ids, ",") + "/review"
	if ev != nil {
		ev <- Event{Type: EventRoute, Name: "reviewer", Model: model, Text: "dispatch " + sid + " → " + model, StepID: sid}
	}
	return a.spawnSubagent(ctx, subagentReq{
		ID: "reviewer", Prompt: b.String(), Model: model, StepID: sid, Events: ev,
	})
}

func (a *Agent) summarizeDispatch(ctx context.Context, ev chan<- Event, log string) string {
	model := a.plannerModel()
	prompt := "Summarize what the workers implemented. Do not call tools.\n\n" + log
	userMsg := llm.Message{Role: llm.RoleUser, Content: prompt}
	a.mu.RLock()
	wire := append([]llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt(a.opts, Build)},
	}, a.messages...)
	wire = append(wire, userMsg)
	maxTok := a.opts.MaxTokens
	a.mu.RUnlock()
	ch, err := a.chat(ctx, model, wire, nil, maxTok)
	if err != nil {
		return strings.TrimSpace(log)
	}
	usage := &llm.Usage{}
	assistant, ok := a.consume(ctx, ch, ev, usage)
	if !ok || assistant == nil || strings.TrimSpace(assistant.Content) == "" {
		return strings.TrimSpace(log)
	}
	a.mu.Lock()
	a.messages = append(a.messages, userMsg)
	a.mu.Unlock()
	return assistant.Content
}

func (a *Agent) nextWave() []tools.PlanStep {
	a.mu.RLock()
	p := a.plan
	a.mu.RUnlock()
	done := map[string]bool{}
	for _, s := range p.Steps {
		if s.Status == "done" {
			done[s.ID] = true
		}
	}
	var ready []tools.PlanStep
	for _, s := range p.Steps {
		if s.Confirm {
			continue
		}
		if s.Status != "pending" {
			continue
		}
		ok := true
		for _, d := range s.Deps {
			if !done[d] {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, s)
		}
	}
	return pickNonOverlapping(ready, maxParallelWorkers)
}

func pickNonOverlapping(ready []tools.PlanStep, capN int) []tools.PlanStep {
	if capN <= 0 {
		capN = 1
	}
	var out []tools.PlanStep
	for _, s := range ready {
		if s.Confirm {
			continue
		}
		if len(out) >= capN {
			break
		}
		if len(s.Files) == 0 {
			if len(out) == 0 {
				return []tools.PlanStep{s}
			}
			continue
		}
		conflict := false
		for _, taken := range out {
			if filesOverlap(taken.Files, s.Files) {
				conflict = true
				break
			}
		}
		if !conflict {
			out = append(out, s)
		}
	}
	return out
}

func filesOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			if pathOverlap(x, y) {
				return true
			}
		}
	}
	return false
}

func pathOverlap(a, b string) bool {
	a, b = filepath.ToSlash(strings.TrimSpace(a)), filepath.ToSlash(strings.TrimSpace(b))
	if a == "" || b == "" || a == b {
		return true
	}
	if ok, _ := filepath.Match(a, b); ok {
		return true
	}
	if ok, _ := filepath.Match(b, a); ok {
		return true
	}
	if strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
		return true
	}
	return false
}

func workerPrompt(step tools.PlanStep) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Implement only this plan step (%s): %s\n\n", step.ID, step.Title)
	if step.Detail != "" {
		b.WriteString(step.Detail)
		b.WriteByte('\n')
	}
	if len(step.Files) > 0 {
		fmt.Fprintf(&b, "\nYou may write these paths: %s\n", strings.Join(step.Files, ", "))
	}
	b.WriteString("\nWrite paths relative to the workspace root. Do not nest a second module directory. Do not call question.\n")
	b.WriteString("If you write .go files, create go.mod at the repo root if it is missing (use the write tool, not bash go mod init).\n")
	b.WriteString("Complete this step and stop. Do not redo the whole plan.\n")
	return b.String()
}

func reviewFailed(out string) bool {
	// Explicit FAIL rejects the wave. PASS or no verdict (reviewer
	// wandered / print-mode) accepts the worker result.
	return reviewVerdict(out) == "FAIL"
}

// reviewVerdict returns PASS or FAIL from the first verdict line.
func reviewVerdict(out string) string {
	lines := strings.Split(out, "\n")
	n := len(lines)
	if n > 8 {
		n = 8
	}
	for _, line := range lines[:n] {
		u := strings.ToUpper(strings.TrimSpace(line))
		u = strings.TrimLeft(u, "#*`> ")
		switch {
		case strings.HasPrefix(u, "PASS") && (len(u) == 4 || !isLetter(u[4])):
			return "PASS"
		case strings.HasPrefix(u, "FAIL") && (len(u) == 4 || !isLetter(u[4])):
			return "FAIL"
		}
	}
	return ""
}

func isLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func (a *Agent) setStepStatus(id, status string, ev chan<- Event) {
	a.mu.Lock()
	for i := range a.plan.Steps {
		if a.plan.Steps[i].ID == id {
			a.plan.Steps[i].Status = status
			break
		}
	}
	p := a.plan
	todos := p.Todos()
	a.todos = todos
	onPlan := a.onPlan
	a.mu.Unlock()
	if ev != nil {
		ev <- Event{Type: EventTodos, Todos: todos}
	}
	if onPlan != nil {
		onPlan(p)
	}
}

func dispatchAllow(parent AllowFunc, files []string) AllowFunc {
	return func(ctx context.Context, name, args string) error {
		if scopedFileAllow(name, args, files) {
			return nil
		}
		if parent != nil {
			return parent(ctx, name, args)
		}
		return nil
	}
}

func scopedFileAllow(name, args string, files []string) bool {
	if len(files) == 0 {
		return false
	}
	switch name {
	case "write", "edit", "read":
	default:
		return false
	}
	path := toolArgPath(args)
	if path == "" {
		return false
	}
	base := filepath.Base(filepath.ToSlash(path))
	if base == "go.mod" || base == "go.sum" {
		return true
	}
	for _, f := range files {
		if pathOverlap(path, f) {
			return true
		}
	}
	return false
}

func toolArgPath(args string) string {
	var m struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	return strings.TrimSpace(m.Path)
}
