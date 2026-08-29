// Package agent is the frontend-agnostic core: it drives the
// model -> tool -> model loop and reports progress as typed events.
// Frontends (TUI today, web later) consume the event stream only.
package agent

import "quikagent/internal/llm"

// EventType classifies an agent event.
type EventType int

const (
	EventThinking  EventType = iota // reasoning delta
	EventText                       // assistant text delta
	EventToolStart                  // a tool is about to run
	EventToolDone                   // a tool finished; Output is set
	EventTurnDone                   // the user turn finished; Usage is set
	EventError                      // turn failed; Err is set
	EventRoute                      // Arch-Router selected a model for this turn
	EventTodos                      // todo list updated
	EventStatus                     // pre-stream phase (waiting, routing, compacting)
)

// Event is one item in the agent's output stream.
type Event struct {
	Type       EventType
	Text       string     // EventText, EventThinking, EventStatus (label)
	Output     string     // EventToolDone
	Name       string     // EventToolStart, EventToolDone, EventRoute (route name), EventStatus (phase)
	Args       string     // EventToolStart (raw JSON)
	Model      string     // EventRoute (selected chat model)
	Usage      *llm.Usage // EventTurnDone
	Err        error      // EventError
	Todos      []TodoItem // EventTodos
	ToolCallID string     // EventToolStart, EventToolDone
	StepID     string     // orchestrated worker/reviewer (dispatch tidy)
}

// Usage is cumulative token accounting for a turn.
type Usage struct {
	Prompt     int
	Completion int
}
