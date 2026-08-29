package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"quikagent/internal/llm"
)

// PlanStep is one unit of work in a structured plan.
type PlanStep struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	Files  []string `json:"files,omitempty"`
	Deps   []string `json:"deps,omitempty"`
	Status string   `json:"status"`
}

// Plan is the session-scoped structured plan (not markdown).
type Plan struct {
	Title string     `json:"title"`
	Steps []PlanStep `json:"steps"`
}

// PlanTool lets the model record a structured plan (plan mode, read-only).
type PlanTool struct {
	plan Plan
}

// NewPlan creates an empty PlanTool.
func NewPlan() *PlanTool { return &PlanTool{} }

func (t *PlanTool) ReadOnly() bool { return true }

func (t *PlanTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "plan",
		Description: "Record a structured implementation plan. Call once after a short investigate, with concrete steps (id, title, detail, optional files and deps). Then write the human-readable plan and stop.",
		Parameters:  []byte(`{"type":"object","properties":{"title":{"type":"string"},"steps":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},"detail":{"type":"string"},"files":{"type":"array","items":{"type":"string"}},"deps":{"type":"array","items":{"type":"string"}},"status":{"type":"string","enum":["pending","in_progress","done","failed"]}},"required":["id","title"]}}},"required":["steps"]}`),
	}
}

func (t *PlanTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var payload Plan
	if err := json.Unmarshal(args, &payload); err != nil {
		return "", errInvalidArg(err.Error())
	}
	if len(payload.Steps) == 0 {
		return "", errInvalidArg("steps is required")
	}
	seen := map[string]bool{}
	for i := range payload.Steps {
		s := &payload.Steps[i]
		s.ID = strings.TrimSpace(s.ID)
		s.Title = strings.TrimSpace(s.Title)
		if s.ID == "" {
			return "", errInvalidArg("step id is required")
		}
		if s.Title == "" {
			return "", errInvalidArg("step title is required")
		}
		if seen[s.ID] {
			return "", errInvalidArg("duplicate step id " + s.ID)
		}
		seen[s.ID] = true
		if s.Status == "" {
			s.Status = "pending"
		}
		switch s.Status {
		case "pending", "in_progress", "done", "failed":
		default:
			return "", errInvalidArg("invalid step status " + s.Status)
		}
	}
	t.plan = payload
	return fmt.Sprintf("Plan recorded: %d step(s).", len(payload.Steps)), nil
}

// Plan returns a copy of the recorded plan.
func (t *PlanTool) Plan() Plan {
	out := Plan{Title: t.plan.Title, Steps: make([]PlanStep, len(t.plan.Steps))}
	copy(out.Steps, t.plan.Steps)
	return out
}

// Reset clears the recorded plan.
func (t *PlanTool) Reset() {
	t.plan = Plan{}
}

// HasWork reports whether any step is pending or failed.
func (p Plan) HasWork() bool {
	for _, s := range p.Steps {
		if s.Status == "pending" || s.Status == "failed" {
			return true
		}
	}
	return false
}

// Todos maps plan steps onto the sidebar todo list.
func (p Plan) Todos() []TodoItem {
	out := make([]TodoItem, 0, len(p.Steps))
	for _, s := range p.Steps {
		pri := "high"
		if s.Status == "done" {
			pri = "low"
		}
		out = append(out, TodoItem{Content: s.Title, Status: s.Status, Priority: pri})
	}
	return out
}
