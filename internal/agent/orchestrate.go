package agent

import (
	"context"
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
	if a.ModelPinned() || a.Mode() != Build {
		return false
	}
	if !a.RouterEnabled() {
		return false
	}
	a.mu.RLock()
	hasWork := a.plan.HasWork()
	a.mu.RUnlock()
	return hasWork && isHandoffText(userText)
}

func (a *Agent) runOrchestrated(ctx context.Context, ev chan<- Event, steps *int, turnErr *error) {
	emitStatus(ev, "dispatch")
	var log strings.Builder
	failed := false
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
					ID: "general", Prompt: prompt, Model: model, StepID: step.ID, Events: ev,
				})
				results[i] = result{id: step.ID, out: out, err: err, route: route, model: model}
			}()
		}
		wg.Wait()
		*steps += len(batch)

		for i, res := range results {
			step := batch[i]
			if res.err != nil {
				a.setStepStatus(res.id, "failed", ev)
				fmt.Fprintf(&log, "- %s (%s): failed: %v\n", step.Title, res.route, res.err)
				failed = true
				continue
			}
			revOut, revErr := a.reviewStep(ctx, ev, step, res.out)
			if revErr != nil {
				a.setStepStatus(res.id, "failed", ev)
				fmt.Fprintf(&log, "- %s (%s): review error: %v\n", step.Title, res.route, revErr)
				failed = true
				continue
			}
			if reviewFailed(revOut) {
				a.setStepStatus(res.id, "failed", ev)
				fmt.Fprintf(&log, "- %s (%s): review FAIL: %s\n", step.Title, res.route, clip(revOut, 200))
				failed = true
				continue
			}
			a.setStepStatus(res.id, "done", ev)
			fmt.Fprintf(&log, "- %s (%s / %s): ok\n", step.Title, res.route, res.model)
			fmt.Fprintf(&log, "  %s\n", clip(res.out, 400))
		}
		if failed {
			break
		}
	}

	summary := a.summarizeDispatch(ctx, ev, log.String())
	if summary != "" {
		a.mu.Lock()
		a.messages = append(a.messages, llm.Message{Role: llm.RoleAssistant, Content: summary})
		a.mu.Unlock()
	}
	ev <- Event{Type: EventTurnDone}
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
	a.mu.Lock()
	a.lastRoute = route
	a.mu.Unlock()
	if ev != nil {
		ev <- Event{Type: EventRoute, Name: route, Model: model, Text: "dispatch " + step.ID}
	}
	a.emitTrace(session.TraceRecord{Type: "dispatch", Route: route, Model: model, StepID: step.ID, Name: step.Title})
	return route, model
}

func (a *Agent) reviewStep(ctx context.Context, ev chan<- Event, step tools.PlanStep, workerOut string) (string, error) {
	prompt := fmt.Sprintf("Step %s (%s):\n%s\n\nWorker result:\n%s\n", step.ID, step.Title, step.Detail, clip(workerOut, 1500))
	return a.spawnSubagent(ctx, subagentReq{
		ID: "reviewer", Prompt: prompt, StepID: step.ID + "/review", Events: ev,
	})
}

func (a *Agent) summarizeDispatch(ctx context.Context, ev chan<- Event, log string) string {
	model := a.plannerModel()
	prompt := "Summarize what the workers implemented. Do not call tools.\n\n" + log
	a.mu.RLock()
	wire := append([]llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt(a.opts, Build)},
	}, a.messages...)
	wire = append(wire, llm.Message{Role: llm.RoleUser, Content: prompt})
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
		if s.Status != "pending" && s.Status != "failed" {
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
		if len(out) >= capN {
			break
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
	b.WriteString("\nDo not redo the whole plan. Complete this step and stop.\n")
	return b.String()
}

func reviewFailed(out string) bool {
	u := strings.ToUpper(out)
	if strings.Contains(u, "PASS") {
		return false
	}
	return strings.Contains(u, "FAIL")
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
