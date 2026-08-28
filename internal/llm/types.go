// Package llm is a minimal OpenAI-compatible chat client with streaming
// and tool-call support. It deliberately uses only the standard library.
package llm

// Role identifies who produced a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall is a function invocation requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON object, e.g. {"path":"main.go"}
}

// Message is one entry in the conversation.
type Message struct {
	Role    Role
	Content string

	// Reasoning carries the model's thinking output (when the provider
	// emits it). It is display-only and not sent back to the API.
	Reasoning string

	// ToolCalls is set on assistant messages that request tool runs.
	ToolCalls []ToolCall

	// ToolCallID and Name are set on tool-result messages.
	ToolCallID string
	Name       string
}

// Tool describes a function the model may call.
type Tool struct {
	Name        string
	Description string
	Parameters  []byte // JSON Schema object
}

// Usage reports token accounting for a completed exchange.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// EventType classifies a streaming event.
type EventType int

const (
	EventText      EventType = iota // assistant text delta
	EventReasoning                  // thinking delta
	EventDone                       // turn complete; Message is set
	EventError                      // failure; Err is set
)

// Event is a single item from the Chat stream.
type Event struct {
	Type      EventType
	Text      string
	Reasoning string
	Message   *Message
	Usage     *Usage
	Err       error
}
