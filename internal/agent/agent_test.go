package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"quikagent/internal/llm"
	"quikagent/internal/session"
	"quikagent/internal/tools"
)

// fakeLLM replays scripted responses and records each Chat call.
type fakeLLM struct {
	scripts  []script
	defs     [][]llm.Tool
	requests [][]llm.Message
	model    string
	models   []string // model at each Chat
}

type script struct {
	text      string
	reasoning string
	toolCalls []llm.ToolCall
	err       error
	streamErr error
	nilErr    bool
}

func (f *fakeLLM) Model() string {
	if f.model == "" {
		return "fake"
	}
	return f.model
}

func (f *fakeLLM) SetModel(model string) { f.model = model }

func (f *fakeLLM) Chat(ctx context.Context, messages []llm.Message, toolDefs []llm.Tool, maxTokens int) (<-chan llm.Event, error) {
	f.requests = append(f.requests, append([]llm.Message(nil), messages...))
	f.defs = append(f.defs, append([]llm.Tool(nil), toolDefs...))
	f.models = append(f.models, f.Model())
	if len(f.scripts) == 0 {
		return nil, fmt.Errorf("fakeLLM exhausted")
	}
	s := f.scripts[0]
	f.scripts = f.scripts[1:]

	ch := make(chan llm.Event, 16)
	go func() {
		defer close(ch)
		if s.err != nil {
			ch <- llm.Event{Type: llm.EventError, Err: s.err}
			return
		}
		if s.reasoning != "" {
			ch <- llm.Event{Type: llm.EventReasoning, Reasoning: s.reasoning}
		}
		if s.text != "" {
			for _, r := range s.text {
				ch <- llm.Event{Type: llm.EventText, Text: string(r)}
			}
		}
		if s.nilErr {
			ch <- llm.Event{Type: llm.EventError}
			return
		}
		if s.streamErr != nil {
			ch <- llm.Event{Type: llm.EventError, Err: s.streamErr}
			return
		}
		ch <- llm.Event{
			Type: llm.EventDone,
			Message: &llm.Message{
				Role:      llm.RoleAssistant,
				Content:   s.text,
				Reasoning: s.reasoning,
				ToolCalls: s.toolCalls,
			},
			Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 5},
		}
	}()
	return ch, nil
}

func collect(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func newTestAgent(dir string, fake *fakeLLM) *Agent {
	return New(fake, tools.New(dir), Options{Workdir: dir, Model: "fake", MaxTokens: 100})
}

func TestSimpleTurn(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "All done."}}}
	a := newTestAgent(t.TempDir(), fake)

	events := collect(t, run(a, "fix the bug"))
	var text string
	terminal := Event{}
	for _, e := range events {
		switch e.Type {
		case EventText:
			text += e.Text
		case EventTurnDone:
			terminal = e
		case EventError:
			t.Fatalf("unexpected error: %v", e.Err)
		}
	}
	if text != "All done." {
		t.Fatalf("text = %q", text)
	}
	if terminal.Type != EventTurnDone {
		t.Fatalf("terminal = %+v", terminal)
	}
	if terminal.Usage == nil || terminal.Usage.PromptTokens != 10 || terminal.Usage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v", terminal.Usage)
	}

	hist := a.Messages()
	if len(hist) != 2 || hist[0].Role != llm.RoleUser || hist[1].Role != llm.RoleAssistant {
		t.Fatalf("history = %+v", hist)
	}
	if hist[1].Content != "All done." {
		t.Fatalf("assistant content = %q", hist[1].Content)
	}
	// System prompt is prepended at call time, not stored.
	if fake.requests[0][0].Role != llm.RoleSystem || !strings.Contains(fake.requests[0][0].Content, "quikagent") {
		t.Fatalf("system prompt = %+v", fake.requests[0][0])
	}
}

func TestToolLoop(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir, "notes.txt", "hello\n"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"notes.txt"}`}}},
		{text: "The file says hello."},
	}}
	a := newTestAgent(dir, fake)

	events := collect(t, run(a, "what does notes.txt say?"))
	var (
		started, done    bool
		toolOutput       string
		turns, toolCalls int
	)
	for _, e := range events {
		switch e.Type {
		case EventToolStart:
			started = e.Name == "read" && e.Args == `{"path":"notes.txt"}`
			toolCalls++
		case EventToolDone:
			done = true
			toolOutput = e.Output
		case EventTurnDone:
			turns++
		case EventError:
			t.Fatalf("unexpected error: %v", e.Err)
		}
	}
	if !started || !done || turns != 1 || toolCalls != 1 {
		t.Fatalf("started=%v done=%v turns=%d toolCalls=%d", started, done, turns, toolCalls)
	}
	if !strings.Contains(toolOutput, "hello") {
		t.Fatalf("tool output = %q", toolOutput)
	}

	// Two LLM calls; the second saw the tool result in context.
	if len(fake.requests) != 2 {
		t.Fatalf("llm calls = %d", len(fake.requests))
	}
	last := fake.requests[1]
	toolMsg := last[len(last)-1]
	if toolMsg.Role != llm.RoleTool || toolMsg.ToolCallID != "c1" || toolMsg.Name != "read" {
		t.Fatalf("tool result msg = %+v", toolMsg)
	}
	assistantBefore := last[len(last)-2]
	if len(assistantBefore.ToolCalls) != 1 || assistantBefore.ToolCalls[0].ID != "c1" {
		t.Fatalf("assistant msg = %+v", assistantBefore)
	}

	// Usage accumulates across calls.
	var usage *llm.Usage
	for _, e := range events {
		if e.Type == EventTurnDone {
			usage = e.Usage
		}
	}
	if usage == nil || usage.PromptTokens != 20 || usage.CompletionTokens != 10 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestUnknownToolFeedsModelError(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "nope", Arguments: `{}`}}},
		{text: "recovered"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	events := collect(t, run(a, "do it"))

	var output string
	for _, e := range events {
		if e.Type == EventToolDone {
			output = e.Output
		}
	}
	if !strings.Contains(output, "unknown tool") {
		t.Fatalf("output = %q", output)
	}
	// Model still completes the turn.
	var finished bool
	for _, e := range events {
		if e.Type == EventTurnDone {
			finished = true
		}
	}
	if !finished {
		t.Fatal("turn did not finish")
	}
}

func TestPlanModeLimitsTools(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "here is a plan"}}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetMode(Plan)
	collect(t, run(a, "plan a refactor"))

	defs := fake.defs[0]
	names := []string{}
	for _, d := range defs {
		names = append(names, d.Name)
	}
	want := map[string]bool{"read": true, "glob": true, "grep": true, "list": true, "fetch": true, "git": true, "question": true, "skill": true}
	for _, n := range names {
		if !want[n] {
			t.Fatalf("unexpected plan tool %q in %v", n, names)
		}
		delete(want, n)
	}
	if len(want) != 0 {
		t.Fatalf("missing plan tools %v (got %v)", want, names)
	}
	sys := fake.requests[0][0].Content
	if !strings.Contains(sys, "Plan mode") {
		t.Fatal("system prompt lacks plan mode notice")
	}
	if !strings.Contains(sys, "/build") {
		t.Fatal("plan prompt should tell the user how to leave plan mode")
	}
	if strings.Contains(sys, "Use bash") {
		t.Fatal("plan prompt should not tell the model to use bash")
	}
	if !strings.Contains(sys, "skill") {
		t.Fatal("plan prompt should mention skill")
	}
}

func TestThinkingForwarded(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "answer", reasoning: "let me think"}}}
	a := newTestAgent(t.TempDir(), fake)
	events := collect(t, run(a, "hi"))

	var thinking, text string
	for _, e := range events {
		switch e.Type {
		case EventThinking:
			thinking += e.Text
		case EventText:
			text += e.Text
		}
	}
	if thinking != "let me think" || text != "answer" {
		t.Fatalf("thinking=%q text=%q", thinking, text)
	}
	// Reasoning is kept in history for redisplay on resume; the llm
	// layer drops it before sending to the API.
	hist := a.Messages()
	if hist[1].Reasoning != "let me think" {
		t.Fatalf("reasoning = %q", hist[1].Reasoning)
	}
}

func TestChatSetupError(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{err: fmt.Errorf("boom")}}}
	a := newTestAgent(t.TempDir(), fake)
	events := collect(t, run(a, "hi"))
	if events[len(events)-1].Type != EventError {
		t.Fatalf("last = %+v", events[len(events)-1])
	}
}

func TestStreamErrorForwardedOnce(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{streamErr: fmt.Errorf("stream broke")}}}
	a := newTestAgent(t.TempDir(), fake)
	events := collect(t, run(a, "hi"))

	var errCount int
	for _, e := range events {
		if e.Type == EventError {
			errCount++
			if !strings.Contains(e.Err.Error(), "stream broke") {
				t.Fatalf("err = %v", e.Err)
			}
		}
	}
	if errCount != 1 {
		t.Fatalf("error events = %d", errCount)
	}
}

func TestMaxStepsGuard(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir, "f.txt", "x\n"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{}
	for i := range maxSteps + 5 {
		fake.scripts = append(fake.scripts, script{
			toolCalls: []llm.ToolCall{{ID: fmt.Sprintf("c%d", i), Name: "read", Arguments: `{"path":"f.txt"}`}},
		})
	}
	a := newTestAgent(dir, fake)
	events := collect(t, run(a, "loop forever"))

	var last Event
	for _, e := range events {
		last = e
	}
	if last.Type != EventError || !strings.Contains(last.Err.Error(), "tool steps") {
		t.Fatalf("last = %+v", last)
	}
}

func TestPlanModeRejectsMutatingTool(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeLLM{scripts: []script{{
		toolCalls: []llm.ToolCall{{ID: "1", Name: "bash", Arguments: `{"command":"echo hi"}`}},
	}, {text: "blocked"}}}
	a := newTestAgent(dir, fake)
	a.SetMode(Plan)
	events := collect(t, run(a, "run echo"))
	var toolOut string
	for _, e := range events {
		if e.Type == EventToolDone {
			toolOut = e.Output
		}
	}
	if !strings.Contains(toolOut, "not available in plan mode") {
		t.Fatalf("toolOut = %q events=%+v", toolOut, events)
	}
}

func TestSetModeDuringRunChangesNextStepTools(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir, "f.txt", "x\n"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "1", Name: "read", Arguments: `{"path":"f.txt"}`}}},
		{text: "ok"},
	}}
	a := newTestAgent(dir, fake)
	a.SetMode(Plan)
	a.SetAllowTool(func(ctx context.Context, name, args string) error {
		a.SetMode(Build)
		return nil
	})
	collect(t, run(a, "go"))
	if len(fake.defs) < 2 {
		t.Fatalf("defs=%d", len(fake.defs))
	}
	for _, d := range fake.defs[0] {
		if d.Name == "write" {
			t.Fatal("first step should still be plan tools")
		}
	}
	hasWrite := false
	for _, d := range fake.defs[1] {
		if d.Name == "write" {
			hasWrite = true
		}
	}
	if !hasWrite {
		t.Fatal("second step should include write after SetMode(Build)")
	}
}

func TestRouterEmitsEvent(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "done"}}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetRouter(&fakeRouter{route: "nano", model: "nemotron-nano-q4"})
	a.SetRouterEnabled(true)
	events := collect(t, run(a, "commit these files"))
	var routed bool
	for _, e := range events {
		if e.Type == EventRoute && e.Name == "nano" && e.Model == "nemotron-nano-q4" {
			routed = true
		}
	}
	if !routed {
		t.Fatalf("events = %+v", events)
	}
	if a.Model() != "nemotron-nano-q4" {
		t.Fatalf("model = %s", a.Model())
	}
}

func TestRouterFallbackSurfacesError(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "done"}}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetRouter(&fakeRouter{route: "qwen", model: "qwen-fallback", err: fmt.Errorf("router down")})
	a.SetRouterEnabled(true)
	events := collect(t, run(a, "hello"))
	var found bool
	for _, e := range events {
		if e.Type == EventRoute && e.Name == "qwen" && strings.Contains(e.Text, "router down") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected route fallback text, events=%+v", events)
	}
}

type fakeRouter struct {
	route, model string
	err          error
	calls        int
}

func (f *fakeRouter) Select(ctx context.Context, userText string) (string, string, error) {
	f.calls++
	return f.route, f.model, f.err
}

func TestNewDoesNotMutateCallerRegistry(t *testing.T) {
	dir := t.TempDir()
	reg := tools.New(dir)
	if _, ok := reg.Get("task"); ok {
		t.Fatal("default registry should not include task")
	}
	_ = New(&fakeLLM{}, reg, Options{Workdir: dir, Model: "fake", MaxTokens: 100})
	if _, ok := reg.Get("task"); ok {
		t.Fatal("New must clone the registry before adding task")
	}
	if _, ok := reg.Get("skill"); ok {
		t.Fatal("New must clone the registry before adding skill")
	}
}

func TestLoadHistory(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "again"}}}
	a := newTestAgent(t.TempDir(), fake)
	a.LoadHistory([]llm.Message{
		{Role: llm.RoleUser, Content: "earlier"},
		{Role: llm.RoleAssistant, Content: "earlier reply"},
	})
	collect(t, run(a, "again"))

	req := fake.requests[0]
	// system + 2 history + new user
	if len(req) != 4 || req[1].Content != "earlier" || req[3].Content != "again" {
		t.Fatalf("request = %+v", req)
	}
}

func TestAgentHooksPreDeny(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	dir := t.TempDir()
	if err := writeFile(dir, "f.txt", "x"); err != nil {
		t.Fatal(err)
	}
	hookDir := filepath.Join(dir, ".quikagent", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "pre-tool"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"f.txt"}`}}},
		{text: "ok"},
	}}
	a := newTestAgent(dir, fake)
	events := collect(t, run(a, "read it"))
	var denied bool
	for _, e := range events {
		if e.Type == EventToolDone && strings.Contains(e.Output, "denied") {
			denied = true
		}
	}
	if !denied {
		t.Fatal("expected pre-tool hook deny")
	}
}

func TestAgentHooksPostRunsOnToolError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	dir := t.TempDir()
	hookDir := filepath.Join(dir, ".quikagent", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "post-on-error")
	hook := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(filepath.Join(hookDir, "post-tool"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"missing.txt"}`}}},
		{text: "ok"},
	}}
	a := newTestAgent(dir, fake)
	collect(t, run(a, "read missing"))
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("post hook must run when tool.Run returns an error")
	}
}

func TestAgentHooksPreExecFailureNotDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	dir := t.TempDir()
	if err := writeFile(dir, "f.txt", "x"); err != nil {
		t.Fatal(err)
	}
	hookDir := filepath.Join(dir, ".quikagent", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "pre-tool"), []byte("#!/bin/sh\nexit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"f.txt"}`}}},
		{text: "ok"},
	}}
	a := newTestAgent(dir, fake)
	events := collect(t, run(a, "read it"))
	var failed, denied bool
	for _, e := range events {
		if e.Type == EventToolDone {
			if strings.Contains(e.Output, "hook failed") {
				failed = true
			}
			if strings.Contains(e.Output, "tool denied") {
				denied = true
			}
		}
	}
	if !failed || denied {
		t.Fatalf("expected hook failed (not denied), events=%+v", events)
	}
}

func TestAllowToolDenied(t *testing.T) {
	dir := t.TempDir()
	_ = writeFile(dir, "f.txt", "x")
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"rm f.txt"}`}}},
		{text: "ok"},
	}}
	a := newTestAgent(dir, fake)
	a.SetAllowTool(func(ctx context.Context, name, args string) error {
		return fmt.Errorf("nope")
	})
	events := collect(t, run(a, "delete it"))
	var denied bool
	for _, e := range events {
		if e.Type == EventToolDone && strings.Contains(e.Output, "denied") {
			denied = true
		}
	}
	if !denied {
		t.Fatal("expected denied tool output")
	}
}

func TestCompact(t *testing.T) {
	a := newTestAgent(t.TempDir(), &fakeLLM{})
	var msgs []llm.Message
	for i := range 20 {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("msg-%d", i)})
	}
	a.LoadHistory(msgs)
	if !a.Compact() {
		t.Fatal("expected compact")
	}
	got := a.Messages()
	if len(got) != compactKeepRecent+1 {
		t.Fatalf("len = %d", len(got))
	}
	if !strings.Contains(got[0].Content, "compacted") {
		t.Fatalf("summary = %q", got[0].Content)
	}
}

func TestCompactWithSummarizer(t *testing.T) {
	a := newTestAgent(t.TempDir(), &fakeLLM{})
	a.SetSummarizer(&fakeSummarizer{out: "llm summary of the past"})
	var msgs []llm.Message
	for i := range 20 {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("msg-%d", i)})
	}
	a.LoadHistory(msgs)
	if !a.Compact() {
		t.Fatal("expected compact")
	}
	got := a.Messages()
	if !strings.Contains(got[0].Content, "llm summary of the past") {
		t.Fatalf("summary = %q", got[0].Content)
	}
}

func TestCompactSummarizerErrorFallsBack(t *testing.T) {
	a := newTestAgent(t.TempDir(), &fakeLLM{})
	a.SetSummarizer(&fakeSummarizer{err: fmt.Errorf("summarizer down")})
	var msgs []llm.Message
	for i := range 20 {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("msg-%d", i)})
	}
	a.LoadHistory(msgs)
	if !a.Compact() {
		t.Fatal("expected compact")
	}
	got := a.Messages()
	if !strings.Contains(got[0].Content, "msg-0") {
		t.Fatalf("naive fallback summary missing, got %q", got[0].Content)
	}
}

func TestCompactCallsOnCompact(t *testing.T) {
	a := newTestAgent(t.TempDir(), &fakeLLM{})
	var persisted []llm.Message
	a.SetOnCompact(func(msgs []llm.Message) { persisted = msgs })
	var msgs []llm.Message
	for i := range 20 {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("msg-%d", i)})
	}
	a.LoadHistory(msgs)
	if !a.Compact() {
		t.Fatal("expected compact")
	}
	if len(persisted) != compactKeepRecent+1 {
		t.Fatalf("onCompact got %d messages, want %d", len(persisted), compactKeepRecent+1)
	}
	if !strings.Contains(persisted[0].Content, "compacted") {
		t.Fatalf("persisted[0] = %q", persisted[0].Content)
	}
}

func TestClipRuneSafe(t *testing.T) {
	// "é" is 2 bytes; a byte clip would split the rune.
	got := clip("éééé", 2)
	if got != "éé…" {
		t.Fatalf("clip = %q", got)
	}
	if clip("abc", 10) != "abc" {
		t.Fatal("short string should be unchanged")
	}
}

func TestCompactCtxUsesTurnContext(t *testing.T) {
	a := newTestAgent(t.TempDir(), &fakeLLM{})
	sum := &fakeSummarizer{out: "should not be used"}
	a.SetSummarizer(sum)
	var msgs []llm.Message
	for i := range 20 {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("msg-%d", i)})
	}
	a.LoadHistory(msgs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !a.CompactCtx(ctx) {
		t.Fatal("expected compact")
	}
	if sum.saw == nil {
		t.Fatal("summarizer was not called")
	}
	if sum.saw.Err() == nil {
		t.Fatal("summarizer ctx should be canceled")
	}
	got := a.Messages()
	if !strings.Contains(got[0].Content, "msg-0") {
		t.Fatalf("canceled summarizer should fall back, got %q", got[0].Content)
	}
}

func TestCompactNoChangeSkipsOnCompact(t *testing.T) {
	a := newTestAgent(t.TempDir(), &fakeLLM{})
	called := false
	a.SetOnCompact(func([]llm.Message) { called = true })
	if a.Compact() {
		t.Fatal("expected no compact on short history")
	}
	if called {
		t.Fatal("onCompact must not fire when history is unchanged")
	}
}

func TestTodoToolEmitsEvent(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "todo", Arguments: `{"todos":[{"content":"write tests","status":"in_progress"},{"content":"ship","status":"pending"}]}`}}},
		{text: "tracked"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	events := collect(t, run(a, "track this work"))

	var todoEv *Event
	for i := range events {
		if events[i].Type == EventTodos {
			todoEv = &events[i]
		}
	}
	if todoEv == nil {
		t.Fatalf("no EventTodos in %+v", events)
	}
	if len(todoEv.Todos) != 2 || todoEv.Todos[0].Content != "write tests" || todoEv.Todos[0].Status != "in_progress" {
		t.Fatalf("todos = %+v", todoEv.Todos)
	}
	got := a.Todos()
	if len(got) != 2 || got[1].Content != "ship" {
		t.Fatalf("agent todos = %+v", got)
	}
}

func TestRunEmitsWaitingStatus(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "ok"}}}
	a := newTestAgent(t.TempDir(), fake)
	events := collect(t, run(a, "hi"))
	if len(events) < 2 || events[0].Type != EventStatus || events[0].Name != "waiting" {
		t.Fatalf("first event = %+v", events)
	}
	var sawText bool
	for _, e := range events {
		if e.Type == EventText {
			sawText = true
			break
		}
	}
	if !sawText {
		t.Fatalf("missing text after status: %+v", events)
	}
}

func TestRouterEmitsRoutingStatus(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "done"}}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetRouter(&fakeRouter{route: "nano", model: "nemotron-nano-q4"})
	a.SetRouterEnabled(true)
	events := collect(t, run(a, "commit"))
	if len(events) < 1 || events[0].Type != EventStatus || events[0].Name != "routing" {
		t.Fatalf("first event = %+v", events)
	}
}

func TestPlanModeUsesPlanModel(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "here is a plan"}}}
	a := newTestAgent(t.TempDir(), fake)
	r := &fakeRouter{route: "coder", model: "should-not-use"}
	a.SetRouter(r)
	a.SetRouterEnabled(true)
	a.SetPlanModel("qwen-plan")
	a.SetMode(Plan)
	events := collect(t, run(a, "plan a refactor"))
	if r.calls != 0 {
		t.Fatalf("router calls = %d, want 0", r.calls)
	}
	if len(fake.models) == 0 || fake.models[0] != "qwen-plan" {
		t.Fatalf("chat models = %v", fake.models)
	}
	var routed bool
	for _, e := range events {
		if e.Type == EventRoute && e.Name == "plan" && e.Model == "qwen-plan" {
			routed = true
		}
	}
	if !routed {
		t.Fatalf("missing plan route event: %+v", events)
	}
	if a.Model() != "fake" {
		t.Fatalf("model after turn = %s, want restored fake", a.Model())
	}
}

func TestPlanModeWithoutPlanModelStillRoutes(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "plan"}}}
	a := newTestAgent(t.TempDir(), fake)
	r := &fakeRouter{route: "qwen", model: "qwen-routed"}
	a.SetRouter(r)
	a.SetRouterEnabled(true)
	a.SetMode(Plan)
	events := collect(t, run(a, "plan a refactor"))
	if r.calls != 1 {
		t.Fatalf("router calls = %d, want 1", r.calls)
	}
	var routed bool
	for _, e := range events {
		if e.Type == EventRoute && e.Name == "qwen" && e.Model == "qwen-routed" {
			routed = true
		}
	}
	if !routed {
		t.Fatalf("events = %+v", events)
	}
}

func TestPinnedModelOverridesPlanModel(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "ok"}}}
	a := newTestAgent(t.TempDir(), fake)
	r := &fakeRouter{route: "coder", model: "routed"}
	a.SetRouter(r)
	a.SetRouterEnabled(true)
	a.SetModel("pinned-model")
	a.SetPlanModel("qwen-plan")
	a.SetMode(Plan)
	collect(t, run(a, "plan a refactor"))
	if r.calls != 0 {
		t.Fatalf("router calls = %d, want 0", r.calls)
	}
	if len(fake.models) == 0 || fake.models[0] != "pinned-model" {
		t.Fatalf("chat models = %v", fake.models)
	}
	if a.Model() != "pinned-model" {
		t.Fatalf("model = %s", a.Model())
	}
}

func TestMentionExpandedInRun(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir, "notes.txt", "mention payload\n"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{scripts: []script{{text: "saw it"}}}
	a := newTestAgent(dir, fake)
	collect(t, run(a, "summarize @notes.txt please"))

	req := fake.requests[0]
	userMsg := req[len(req)-1]
	if !strings.Contains(userMsg.Content, "mention payload") {
		t.Fatalf("mention not expanded: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, `<file path="notes.txt">`) {
		t.Fatalf("mention wrapper missing: %q", userMsg.Content)
	}
}

type fakeSummarizer struct {
	out string
	err error
	saw context.Context
}

func (f *fakeSummarizer) Summarize(ctx context.Context, text string) (string, error) {
	f.saw = ctx
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return f.out, f.err
}

func TestTraceTurnWithTool(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir, "notes.txt", "hello\n"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"notes.txt"}`}}},
		{text: "done"},
	}}
	a := newTestAgent(dir, fake)
	var recs []session.TraceRecord
	a.SetTrace(func(r session.TraceRecord) { recs = append(recs, r) })
	a.SetTraceFrontend("print")
	_ = collect(t, run(a, "read notes"))

	types := make([]string, 0, len(recs))
	var tool session.TraceRecord
	for _, r := range recs {
		types = append(types, r.Type)
		if r.Type == "tool" {
			tool = r
		}
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "turn_start") || !strings.Contains(joined, "tool") || !strings.Contains(joined, "turn_end") {
		t.Fatalf("types = %s", joined)
	}
	if recs[0].Frontend != "print" || recs[0].Mode != "build" {
		t.Fatalf("start = %+v", recs[0])
	}
	if tool.Name != "read" || tool.ToolCallID != "c1" || tool.Outcome != "ok" || tool.OK == nil || !*tool.OK {
		t.Fatalf("tool = %+v", tool)
	}
	end := recs[len(recs)-1]
	if end.Type != "turn_end" || end.OK == nil || !*end.OK || end.Steps < 1 {
		t.Fatalf("end = %+v", end)
	}
}

func TestTracePlanDeny(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "1", Name: "bash", Arguments: `{"command":"echo hi"}`}}},
		{text: "blocked"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetMode(Plan)
	var recs []session.TraceRecord
	a.SetTrace(func(r session.TraceRecord) { recs = append(recs, r) })
	_ = collect(t, run(a, "run echo"))
	var tool session.TraceRecord
	for _, r := range recs {
		if r.Type == "tool" {
			tool = r
		}
	}
	if tool.Outcome != "denied_plan" || tool.OK == nil || *tool.OK {
		t.Fatalf("tool = %+v", tool)
	}
}

func TestTraceNilSinkIsNoop(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "ok"}}}
	a := newTestAgent(t.TempDir(), fake)
	_ = collect(t, run(a, "hi"))
}

func run(a *Agent, text string) <-chan Event {
	ch := make(chan Event, 64)
	go a.Run(context.Background(), text, ch)
	return ch
}

func writeFile(dir, name, content string) error {
	return os.WriteFile(dir+"/"+name, []byte(content), 0o644)
}
