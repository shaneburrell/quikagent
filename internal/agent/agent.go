package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"quikagent/internal/hooks"
	"quikagent/internal/llm"
	"quikagent/internal/mention"
	"quikagent/internal/text"
	"quikagent/internal/tools"
	"strings"
	"sync"
	"time"
)

const maxSteps = 50

// compactKeepRecent is how many trailing messages to keep when compacting.
const compactKeepRecent = 12

// compactMessageThreshold triggers auto-compaction.
const compactMessageThreshold = 40

// LLM is the subset of the LLM client the agent needs. *llm.Client
// satisfies it; tests use a fake.
type LLM interface {
	Chat(ctx context.Context, messages []llm.Message, toolDefs []llm.Tool, maxTokens int) (<-chan llm.Event, error)
	Model() string
	SetModel(model string)
}

// Summarizer summarizes conversation text for compaction.
type Summarizer interface {
	Summarize(ctx context.Context, text string) (string, error)
}

// ModelRouter selects a chat model for a user turn (Arch-Router).
type ModelRouter interface {
	Select(ctx context.Context, userText string) (route, model string, err error)
}

// AllowFunc decides whether a tool call may run. Return a non-nil error
// to deny (the error text is shown to the model). Nil means allow.
type AllowFunc func(ctx context.Context, name, args string) error

// TodoItem is the agent-facing task list entry (shared with the todo tool).
type TodoItem = tools.TodoItem

// Agent owns the conversation and runs user turns. It is safe for one
// concurrent Run; frontends serialize turns.
type Agent struct {
	llm         LLM
	tools       *tools.Registry
	opts        Options
	mode        Mode
	messages    []llm.Message
	allowTool   AllowFunc
	router      ModelRouter
	routerOn    bool
	modelPinned bool
	lastRoute   string
	planModel   string
	summarizer  Summarizer
	todos       []TodoItem
	onCompact   func([]llm.Message)
	stepLimit   int
	mu          sync.RWMutex
}

// New builds an Agent with an empty conversation.
func New(llm LLM, toolset *tools.Registry, opts Options) *Agent {
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 8192
	}
	if toolset != nil {
		toolset = toolset.Clone()
	}
	a := &Agent{llm: llm, tools: toolset, opts: opts, planModel: opts.PlanModel, mode: Build, stepLimit: maxSteps}
	if toolset != nil {
		if _, ok := toolset.Get("skill"); !ok {
			toolset.Add(tools.NewSkill(opts.Workdir))
		}
		if _, ok := toolset.Get("task"); !ok {
			toolset.Add(tools.NewTask(a.runSubagent))
		}
	}
	return a
}

// Mode returns the current mode.
func (a *Agent) Mode() Mode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mode
}

// SetMode switches between build and plan. Safe to call during Run;
// the next model step picks up the new tool surface.
func (a *Agent) SetMode(m Mode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = m
}

// SetPlanModel sets the optional model used for plan-mode turns.
// Empty means honor Arch-Router (or the current model) as usual.
func (a *Agent) SetPlanModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.planModel = model
}

// SetAllowTool installs an optional approval callback. Nil auto-allows.
func (a *Agent) SetAllowTool(fn AllowFunc) { a.allowTool = fn }

// SetQuestionAsk installs the frontend callback for the question tool.
func (a *Agent) SetQuestionAsk(fn tools.AskFunc) {
	t, ok := a.tools.Get("question")
	if !ok {
		return
	}
	if q, ok := t.(*tools.QuestionTool); ok {
		q.SetAsk(fn)
	}
}

// SetRouter installs an optional Arch-Router. Call SetRouterEnabled(true) to use it.
func (a *Agent) SetRouter(r ModelRouter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.router = r
}

// SetRouterEnabled turns per-turn routing on or off.
func (a *Agent) SetRouterEnabled(on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.routerOn = on
	if on {
		a.modelPinned = false
	}
}

// RouterEnabled reports whether routing is active.
func (a *Agent) RouterEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.routerOn && a.router != nil && !a.modelPinned
}

// LastRoute returns the most recent Arch-Router route name.
func (a *Agent) LastRoute() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastRoute
}

// SetModel updates the model used for subsequent turns. When pin is true,
// Arch-Router is disabled until SetRouterEnabled(true) or SetModelAuto.
func (a *Agent) SetModel(model string) {
	a.setModel(model, true)
}

// SetSummarizer installs an optional LLM-based summarizer for compaction.
func (a *Agent) SetSummarizer(s Summarizer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.summarizer = s
}

// SetOnCompact installs an optional callback invoked with the new message
// list whenever Compact() rewrites history (manual or automatic).
// Frontends use it to persist the compacted conversation (session.Replace).
func (a *Agent) SetOnCompact(fn func([]llm.Message)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onCompact = fn
}

// SetModelAuto clears a manual pin and sets the model without disabling routing.
func (a *Agent) SetModelAuto(model string) {
	a.mu.Lock()
	a.modelPinned = false
	a.mu.Unlock()
	a.setModel(model, false)
}

func (a *Agent) setModel(model string, pin bool) {
	if model == "" {
		return
	}
	a.mu.Lock()
	a.opts.Model = model
	if pin {
		a.modelPinned = true
	}
	a.mu.Unlock()
	a.llm.SetModel(model)
}

// Model returns the configured model name.
func (a *Agent) Model() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.opts.Model
}

// ModelPinned reports whether the user pinned a model with /model.
func (a *Agent) ModelPinned() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.modelPinned
}

// Todos returns a copy of the current task list.
func (a *Agent) Todos() []TodoItem {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]TodoItem, len(a.todos))
	copy(out, a.todos)
	return out
}

// Messages returns the conversation history (system prompt excluded).
func (a *Agent) Messages() []llm.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]llm.Message, len(a.messages))
	copy(out, a.messages)
	return out
}

// LoadHistory replaces the conversation (used to resume a session).
func (a *Agent) LoadHistory(messages []llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append([]llm.Message(nil), messages...)
}

// ResetTodos clears the in-memory task list and the todo tool.
func (a *Agent) ResetTodos() {
	a.mu.Lock()
	a.todos = nil
	a.mu.Unlock()
	if a.tools == nil {
		return
	}
	if t, ok := a.tools.Get("todo"); ok {
		if td, ok := t.(*tools.TodoTool); ok {
			td.Reset()
		}
	}
}

// Compact summarizes older turns into a single note and keeps the recent
// tail. Returns true if history was changed. Manual compact (TUI/CLI)
// uses a background context; Run uses CompactCtx with the turn context.
func (a *Agent) Compact() bool {
	return a.CompactCtx(context.Background())
}

// CompactCtx is Compact using ctx for the optional LLM summarizer.
func (a *Agent) CompactCtx(ctx context.Context) bool {
	a.mu.Lock()
	if len(a.messages) <= compactKeepRecent {
		a.mu.Unlock()
		return false
	}
	cut := len(a.messages) - compactKeepRecent
	// Never split an assistant tool_calls / tool-result chain.
	for cut < len(a.messages) {
		m := a.messages[cut]
		if m.Role == llm.RoleTool {
			cut++
			continue
		}
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			cut++
			continue
		}
		break
	}
	if cut >= len(a.messages) || cut == 0 {
		a.mu.Unlock()
		return false
	}
	old := append([]llm.Message(nil), a.messages[:cut]...)
	recent := append([]llm.Message(nil), a.messages[cut:]...)
	summarizer := a.summarizer
	a.mu.Unlock()

	var summary string
	if summarizer != nil {
		sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		summary = summarizeMessagesLLM(sctx, summarizer, old)
	}
	if summary == "" {
		summary = summarizeMessages(old)
	}

	a.mu.Lock()
	a.messages = append([]llm.Message{{
		Role:    llm.RoleUser,
		Content: "[conversation compacted]\n" + summary,
	}}, recent...)
	onCompact := a.onCompact
	a.mu.Unlock()
	if onCompact != nil {
		onCompact(a.Messages())
	}
	return true
}

// summarizeMessagesLLM uses the LLM to create a concise conversation summary.
func summarizeMessagesLLM(ctx context.Context, summarizer Summarizer, msgs []llm.Message) string {
	var b strings.Builder
	b.WriteString("Summarize this conversation for continuity: user's goals, key decisions, files changed, current state, open tasks. Under 300 words.\n\n")
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			fmt.Fprintf(&b, "- user: %s\n", clip(m.Content, 200))
		case llm.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "- assistant: %s\n", clip(m.Content, 200))
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "- tool_call %s(%s)\n", tc.Name, clip(tc.Arguments, 80))
			}
		case llm.RoleTool:
			fmt.Fprintf(&b, "- tool %s → %s\n", m.Name, clip(m.Content, 120))
		}
	}

	summary, err := summarizer.Summarize(ctx, b.String())
	if err != nil {
		return "" // fallback to naive method
	}
	return strings.TrimSpace(summary)
}

// Run executes one user turn: it appends the user message, then loops
// model calls and tool executions until the model replies without
// tool calls. Exactly one terminal event (EventTurnDone or EventError)
// is emitted, after which ev is closed.
func (a *Agent) Run(ctx context.Context, userText string, ev chan<- Event) {
	defer close(ev)
	if a.opts.Workdir != "" {
		if expanded, err := mention.Expand(a.opts.Workdir, userText); err == nil {
			userText = expanded
		}
	}
	a.mu.Lock()
	a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: userText})
	needCompact := len(a.messages) > compactMessageThreshold
	a.mu.Unlock()
	if needCompact {
		emitStatus(ev, "compacting")
		a.CompactCtx(ctx)
	}

	if !a.usePlanModel() && a.RouterEnabled() {
		emitStatus(ev, "routing")
		a.mu.RLock()
		r := a.router
		a.mu.RUnlock()
		route, model, err := r.Select(ctx, userText)
		if model != "" {
			a.setModel(model, false)
		}
		if route == "" {
			route = "qwen"
		}
		a.mu.Lock()
		a.lastRoute = route
		curModel := a.opts.Model
		a.mu.Unlock()
		routeEv := Event{Type: EventRoute, Name: route, Model: curModel}
		if err != nil {
			routeEv.Text = err.Error()
		}
		ev <- routeEv
	}

	turnModel := a.Model()
	planEmitted := false
	defer func() {
		if !a.ModelPinned() && a.Model() != turnModel {
			a.setModel(turnModel, false)
		}
	}()

	usage := &llm.Usage{}
	limit := a.stepLimit
	if limit <= 0 {
		limit = maxSteps
	}
	for step := 0; step < limit; step++ {
		mode := a.Mode()
		defs := a.tools.List()
		if mode == Plan {
			defs = a.tools.ReadOnly().List()
		}
		a.applyStepModel(ev, turnModel, &planEmitted)

		a.mu.RLock()
		wire := append([]llm.Message{{Role: llm.RoleSystem, Content: systemPrompt(a.opts, mode)}}, a.messages...)
		maxTok := a.opts.MaxTokens
		a.mu.RUnlock()
		emitStatus(ev, "waiting")
		ch, err := a.llm.Chat(ctx, wire, defs, maxTok)
		if err != nil {
			ev <- Event{Type: EventError, Err: err}
			return
		}

		assistant, ok := a.consume(ctx, ch, ev, usage)
		if !ok {
			return // consume already emitted EventError
		}
		a.mu.Lock()
		a.messages = append(a.messages, *assistant)
		a.mu.Unlock()
		if len(assistant.ToolCalls) == 0 {
			ev <- Event{Type: EventTurnDone, Usage: usage}
			return
		}

		for _, tc := range assistant.ToolCalls {
			if ctx.Err() != nil {
				ev <- Event{Type: EventError, Err: ctx.Err()}
				return
			}
			ev <- Event{Type: EventToolStart, Name: tc.Name, Args: tc.Arguments}
			output := a.runTool(ctx, tc, ev)
			ev <- Event{Type: EventToolDone, Name: tc.Name, Output: output}
			a.mu.Lock()
			a.messages = append(a.messages, llm.Message{
				Role: llm.RoleTool, ToolCallID: tc.ID, Name: tc.Name, Content: output,
			})
			a.mu.Unlock()
		}
	}
	ev <- Event{Type: EventError, Err: fmt.Errorf("stopped after %d tool steps without a final answer", limit)}
}

func emitStatus(ev chan<- Event, name string) {
	ev <- Event{Type: EventStatus, Name: name, Text: name}
}

// usePlanModel reports whether this turn should use plan_model and skip the router.
func (a *Agent) usePlanModel() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mode == Plan && a.planModel != "" && !a.modelPinned
}

// applyStepModel applies plan_model (or restores the turn model) when mode changes mid-turn.
func (a *Agent) applyStepModel(ev chan<- Event, turnModel string, planEmitted *bool) {
	if a.usePlanModel() {
		a.mu.RLock()
		planModel := a.planModel
		a.mu.RUnlock()
		a.setModel(planModel, false)
		if !*planEmitted {
			*planEmitted = true
			a.mu.Lock()
			a.lastRoute = "plan"
			a.mu.Unlock()
			ev <- Event{Type: EventRoute, Name: "plan", Model: planModel}
		}
		return
	}
	if *planEmitted {
		a.setModel(turnModel, false)
		*planEmitted = false
	}
}

// consume drains one LLM stream, forwarding deltas to ev. It returns
// the assembled assistant message, or nil if the stream failed.
func (a *Agent) consume(ctx context.Context, ch <-chan llm.Event, ev chan<- Event, usage *llm.Usage) (*llm.Message, bool) {
	for e := range ch {
		switch e.Type {
		case llm.EventText:
			ev <- Event{Type: EventText, Text: e.Text}
		case llm.EventReasoning:
			ev <- Event{Type: EventThinking, Text: e.Reasoning}
		case llm.EventDone:
			if e.Usage != nil {
				usage.PromptTokens += e.Usage.PromptTokens
				usage.CompletionTokens += e.Usage.CompletionTokens
			}
			return e.Message, true
		case llm.EventError:
			ev <- Event{Type: EventError, Err: e.Err}
			return nil, false
		}
	}
	ev <- Event{Type: EventError, Err: fmt.Errorf("llm stream closed unexpectedly")}
	return nil, false
}

// runTool executes a single tool call and returns model-facing output.
// Hard failures are reported as text so the model can react. Side-channel
// events (e.g. EventTodos) are emitted on ev.
func (a *Agent) runTool(ctx context.Context, tc llm.ToolCall, ev chan<- Event) string {
	tool, ok := a.tools.Get(tc.Name)
	if !ok {
		return fmt.Sprintf("Error: unknown tool %q", tc.Name)
	}
	if a.Mode() == Plan && !tool.ReadOnly() {
		return fmt.Sprintf("Error: tool %q is not available in plan mode", tc.Name)
	}
	if a.allowTool != nil {
		if err := a.allowTool(ctx, tc.Name, tc.Arguments); err != nil {
			return "Error: tool denied: " + err.Error()
		}
	}
	if err := hooks.Pre(ctx, a.opts.Workdir, tc.Name, tc.Arguments); err != nil {
		if hooks.IsDenied(err) {
			return "Error: tool denied: " + err.Error()
		}
		return "Error: hook failed: " + err.Error()
	}
	out, err := tool.Run(ctx, json.RawMessage(tc.Arguments))
	if err != nil {
		out = "Error: " + err.Error()
	}
	hooks.Post(ctx, a.opts.Workdir, tc.Name, tc.Arguments, out)
	if err != nil {
		return out
	}
	if td, isTodo := tool.(*tools.TodoTool); isTodo {
		todos := td.Todos()
		a.mu.Lock()
		a.todos = todos
		a.mu.Unlock()
		ev <- Event{Type: EventTodos, Todos: todos}
	}
	return out
}

func summarizeMessages(msgs []llm.Message) string {
	var b strings.Builder
	b.WriteString("Earlier conversation summary:\n")
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			fmt.Fprintf(&b, "- user: %s\n", clip(m.Content, 200))
		case llm.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "- assistant: %s\n", clip(m.Content, 200))
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "- tool_call %s(%s)\n", tc.Name, clip(tc.Arguments, 80))
			}
		case llm.RoleTool:
			fmt.Fprintf(&b, "- tool %s → %s\n", m.Name, clip(m.Content, 120))
		}
	}
	return strings.TrimSpace(b.String())
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if n <= 0 {
		return "…"
	}
	return text.ClipRunes(s, n)
}
