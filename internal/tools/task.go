package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"quikagent/internal/llm"
)

// TaskFunc runs a named subagent with an isolated conversation.
type TaskFunc func(ctx context.Context, agentID, prompt string) (string, error)

// TaskTool lets the model spawn a child agent.
type TaskTool struct {
	run TaskFunc
}

func NewTask(run TaskFunc) *TaskTool { return &TaskTool{run: run} }

func (t *TaskTool) ReadOnly() bool { return false }

func (t *TaskTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "task",
		Description: "Spawn a child agent for a focused subtask. agent is \"explore\" (read-only search) or \"general\" (can edit), or a custom name from .quikagent/agents/*.md. Returns the child's final answer.",
		Parameters:  []byte(`{"type":"object","properties":{"agent":{"type":"string","description":"Subagent id: explore, general, or custom"},"prompt":{"type":"string","description":"Task for the child agent"}},"required":["prompt"]}`),
	}
}

func (t *TaskTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Agent  string `json:"agent"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	a.Prompt = strings.TrimSpace(a.Prompt)
	if a.Prompt == "" {
		return "", errInvalidArg("prompt is required")
	}
	if a.Agent == "" {
		a.Agent = "general"
	}
	if t.run == nil {
		return "", fmt.Errorf("task: no subagent runner")
	}
	out, err := t.run(ctx, a.Agent, a.Prompt)
	if err != nil {
		return "", err
	}
	return truncate(out), nil
}
