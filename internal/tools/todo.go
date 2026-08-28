package tools

import (
	"context"
	"encoding/json"

	"quikagent/internal/llm"
)

// TodoTool is a tool for managing task lists.
type TodoTool struct {
	todos []TodoItem
}

// TodoItem represents a single todo item.
type TodoItem struct {
	Content  string `json:"content"`
	Status   string `json:"status"`   // pending, in_progress, completed, cancelled
	Priority string `json:"priority"` // high, medium, low
}

// NewTodo creates a new TodoTool with an empty list.
func NewTodo() *TodoTool {
	return &TodoTool{}
}

func (t *TodoTool) ReadOnly() bool { return false }

func (t *TodoTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "todo",
		Description: "Manage a task list. Add, update, or view todos. Use with a JSON object containing a 'todos' array of todo items.",
		Parameters:  []byte(`{"type":"object","properties":{"todos":{"type":"array","items":{"type":"object","properties":{"content":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed","cancelled"]},"priority":{"type":"string","enum":["high","medium","low"]}},"required":["content"]}}},"required":["todos"]}`),
	}
}

func (t *TodoTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var payload struct {
		Todos []TodoItem `json:"todos"`
	}

	if err := json.Unmarshal(args, &payload); err != nil {
		return "", errInvalidArg(err.Error())
	}

	t.todos = payload.Todos
	return "Todos updated.", nil
}

// Todos returns a copy of the current todo list.
func (t *TodoTool) Todos() []TodoItem {
	out := make([]TodoItem, len(t.todos))
	copy(out, t.todos)
	return out
}

// Reset clears the in-memory list (session switch / new conversation).
func (t *TodoTool) Reset() {
	t.todos = nil
}
