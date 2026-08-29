package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"quikagent/internal/llm"
	"quikagent/internal/session"
	"quikagent/internal/tools"
)

// fakeLLM replays scripted responses and records each Chat call.
type fakeLLM struct {
	mu       sync.Mutex
	scripts  []script
	defs     [][]llm.Tool
	requests [][]llm.Message
	model    string
	models   []string // model at each Chat
	delay    time.Duration
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.model == "" {
		return "fake"
	}
	return f.model
}

func (f *fakeLLM) SetModel(model string) {
	f.mu.Lock()
	f.model = model
	f.mu.Unlock()
}

func (f *fakeLLM) Chat(ctx context.Context, messages []llm.Message, toolDefs []llm.Tool, maxTokens int) (<-chan llm.Event, error) {
	return f.ChatAs(ctx, f.Model(), messages, toolDefs, maxTokens)
}

func (f *fakeLLM) ChatAs(ctx context.Context, model string, messages []llm.Message, toolDefs []llm.Tool, maxTokens int) (<-chan llm.Event, error) {
	f.mu.Lock()
	if model == "" {
		if f.model == "" {
			model = "fake"
		} else {
			model = f.model
		}
	}
	f.requests = append(f.requests, append([]llm.Message(nil), messages...))
	f.defs = append(f.defs, append([]llm.Tool(nil), toolDefs...))
	f.models = append(f.models, model)
	if len(f.scripts) == 0 {
		f.mu.Unlock()
		return nil, fmt.Errorf("fakeLLM exhausted")
	}
	s := f.scripts[0]
	f.scripts = f.scripts[1:]
	delay := f.delay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}

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
	want := map[string]bool{"read": true, "glob": true, "grep": true, "list": true, "fetch": true, "git": true, "question": true, "skill": true, "plan": true}
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
	if !strings.Contains(sys, "write the plan") {
		t.Fatal("plan prompt should tell the model to write the plan and stop")
	}
	if !strings.Contains(sys, "two question") {
		t.Fatal("plan prompt should mention the question limit")
	}
}

func TestPlanModeCapsQuestions(t *testing.T) {
	ask := func(context.Context, tools.Question) (string, error) {
		return "yes", nil
	}
	q := func(id string) llm.ToolCall {
		return llm.ToolCall{ID: id, Name: "question", Arguments: `{"question":"ok?"}`}
	}
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{q("q1")}},
		{toolCalls: []llm.ToolCall{q("q2")}},
		{toolCalls: []llm.ToolCall{q("q3")}},
		{text: "here is a plan"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetQuestionAsk(ask)
	a.SetMode(Plan)
	events := collect(t, run(a, "plan a tool"))
	var outs []string
	for _, e := range events {
		if e.Type == EventToolDone {
			outs = append(outs, e.Output)
		}
	}
	if len(outs) != 3 {
		t.Fatalf("tool dones = %d %v", len(outs), outs)
	}
	if !strings.Contains(outs[0], "User answered") || !strings.Contains(outs[1], "User answered") {
		t.Fatalf("first two should succeed: %v", outs)
	}
	if !strings.Contains(outs[2], "at most 2 questions") || !strings.Contains(outs[2], "write the plan") {
		t.Fatalf("third should hit the cap: %q", outs[2])
	}

	fake2 := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{q("b1")}},
		{toolCalls: []llm.ToolCall{q("b2")}},
		{toolCalls: []llm.ToolCall{q("b3")}},
		{text: "ok"},
	}}
	b := newTestAgent(t.TempDir(), fake2)
	b.SetQuestionAsk(ask)
	events = collect(t, run(b, "ask thrice"))
	var n int
	for _, e := range events {
		if e.Type == EventToolDone && strings.Contains(e.Output, "User answered") {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("build mode question successes = %d, want 3", n)
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
	mu                  sync.Mutex
	route, model, coder string
	err                 error
	calls               int
	lastMode            string
	lastText            string
	texts               []string
	queue               []struct{ route, model string }
}

func (f *fakeRouter) Select(ctx context.Context, userText, mode string) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastMode = mode
	f.lastText = userText
	f.texts = append(f.texts, userText)
	if len(f.queue) > 0 {
		q := f.queue[0]
		f.queue = f.queue[1:]
		return q.route, q.model, "", f.err
	}
	return f.route, f.model, "", f.err
}

func (f *fakeRouter) RouteModel(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name == "coder" {
		if f.coder != "" {
			return f.coder
		}
		return "coder-model"
	}
	return ""
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
	if r.lastMode != "plan" {
		t.Fatalf("router mode = %q, want plan", r.lastMode)
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

type probeTool struct {
	name     string
	ro       bool
	sleep    time.Duration
	inFlight *int32
	max      *int32
}

func (p *probeTool) Spec() llm.Tool {
	return llm.Tool{Name: p.name, Description: "probe", Parameters: []byte(`{"type":"object","properties":{}}`)}
}
func (p *probeTool) ReadOnly() bool { return p.ro }
func (p *probeTool) Run(context.Context, json.RawMessage) (string, error) {
	if p.inFlight != nil {
		n := atomic.AddInt32(p.inFlight, 1)
		for {
			old := atomic.LoadInt32(p.max)
			if n <= old || atomic.CompareAndSwapInt32(p.max, old, n) {
				break
			}
		}
		defer atomic.AddInt32(p.inFlight, -1)
	}
	if p.sleep > 0 {
		time.Sleep(p.sleep)
	}
	return p.name + "-ok", nil
}

func TestParallelReadOnlyToolBatch(t *testing.T) {
	var inFlight, max int32
	reg := tools.New(t.TempDir())
	for _, name := range []string{"probe_a", "probe_b", "probe_c"} {
		reg.Add(&probeTool{name: name, ro: true, sleep: 80 * time.Millisecond, inFlight: &inFlight, max: &max})
	}
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{
			{ID: "a", Name: "probe_a", Arguments: `{}`},
			{ID: "b", Name: "probe_b", Arguments: `{}`},
			{ID: "c", Name: "probe_c", Arguments: `{}`},
		}},
		{text: "done"},
	}}
	a := New(fake, reg, Options{Workdir: t.TempDir(), Model: "fake", MaxTokens: 100})
	start := time.Now()
	events := collect(t, run(a, "go"))
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("elapsed %s; expected parallel (~80ms)", elapsed)
	}
	if atomic.LoadInt32(&max) < 2 {
		t.Fatalf("max in-flight = %d, want >= 2", max)
	}
	var dones []Event
	for _, e := range events {
		if e.Type == EventToolDone {
			dones = append(dones, e)
		}
	}
	if len(dones) != 3 || dones[0].ToolCallID != "a" || dones[1].ToolCallID != "b" || dones[2].ToolCallID != "c" {
		t.Fatalf("dones = %+v", dones)
	}
	hist := a.Messages()
	if len(hist) < 5 {
		t.Fatalf("history len = %d", len(hist))
	}
	if hist[2].Content != "probe_a-ok" || hist[3].Content != "probe_b-ok" || hist[4].Content != "probe_c-ok" {
		t.Fatalf("tool order = %q %q %q", hist[2].Content, hist[3].Content, hist[4].Content)
	}
}

func TestMixedToolsStaySequential(t *testing.T) {
	var inFlight, max int32
	reg := tools.New(t.TempDir())
	reg.Add(&probeTool{name: "probe_ro", ro: true, sleep: 60 * time.Millisecond, inFlight: &inFlight, max: &max})
	reg.Add(&probeTool{name: "probe_w", ro: false, sleep: 60 * time.Millisecond, inFlight: &inFlight, max: &max})
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{
			{ID: "r", Name: "probe_ro", Arguments: `{}`},
			{ID: "w", Name: "probe_w", Arguments: `{}`},
		}},
		{text: "done"},
	}}
	a := New(fake, reg, Options{Workdir: t.TempDir(), Model: "fake", MaxTokens: 100})
	_ = collect(t, run(a, "go"))
	if atomic.LoadInt32(&max) != 1 {
		t.Fatalf("max in-flight = %d, want 1", max)
	}
}

func TestQuestionBatchStaysSequential(t *testing.T) {
	var inFlight, max int32
	reg := tools.New(t.TempDir())
	reg.Add(&probeTool{name: "probe_a", ro: true, sleep: 40 * time.Millisecond, inFlight: &inFlight, max: &max})
	reg.Add(&probeTool{name: "probe_b", ro: true, sleep: 40 * time.Millisecond, inFlight: &inFlight, max: &max})
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{
			{ID: "q", Name: "question", Arguments: `{"question":"ok?"}`},
			{ID: "a", Name: "probe_a", Arguments: `{}`},
			{ID: "b", Name: "probe_b", Arguments: `{}`},
		}},
		{text: "done"},
	}}
	a := New(fake, reg, Options{Workdir: t.TempDir(), Model: "fake", MaxTokens: 100})
	a.SetQuestionAsk(func(context.Context, tools.Question) (string, error) {
		time.Sleep(40 * time.Millisecond)
		return "yes", nil
	})
	_ = collect(t, run(a, "go"))
	if atomic.LoadInt32(&max) != 1 {
		t.Fatalf("max in-flight = %d, want 1", max)
	}
}

func TestHandoffGoRoutesToCoder(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "here is a plan"}, {text: "on it"}}}
	a := newTestAgent(t.TempDir(), fake)
	r := &fakeRouter{route: "qwen", model: "qwen-routed", coder: "coder-model"}
	a.SetRouter(r)
	a.SetRouterEnabled(true)
	a.SetMode(Plan)
	collect(t, run(a, "plan a refactor"))
	a.SetMode(Build)
	r.route = "other"
	r.model = ""
	events := collect(t, run(a, "go"))
	if r.lastMode != "handoff" {
		t.Fatalf("router mode = %q, want handoff", r.lastMode)
	}
	var routed bool
	for _, e := range events {
		if e.Type == EventRoute && e.Name == "coder" && e.Model == "coder-model" {
			routed = true
		}
	}
	if !routed {
		t.Fatalf("missing coder handoff route: %+v", events)
	}
}

func TestThanksAfterPlanDoesNotForceCoder(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "here is a plan"}, {text: "bye"}}}
	a := newTestAgent(t.TempDir(), fake)
	r := &fakeRouter{route: "qwen", model: "qwen-routed"}
	a.SetRouter(r)
	a.SetRouterEnabled(true)
	a.SetMode(Plan)
	collect(t, run(a, "plan a refactor"))
	a.SetMode(Build)
	r.route = "other"
	r.model = ""
	events := collect(t, run(a, "thanks, I'm done"))
	if r.lastMode != "build" {
		t.Fatalf("router mode = %q, want build", r.lastMode)
	}
	for _, e := range events {
		if e.Type == EventRoute && e.Name == "coder" {
			t.Fatalf("thanks should not force coder: %+v", events)
		}
	}
}

func TestPinnedModelSkipsOrchestrate(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "one model"}}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetRouter(&fakeRouter{route: "nano", model: "nano"})
	a.SetRouterEnabled(true)
	a.SetModel("pinned")
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{{ID: "a", Title: "scaffold", Status: "pending", Files: []string{"main.go"}}}})
	events := collect(t, run(a, "go"))
	for _, e := range events {
		if e.Type == EventRoute && strings.Contains(e.Text, "dispatch") {
			t.Fatalf("pinned should not dispatch: %+v", events)
		}
	}
	if fake.models[0] != "pinned" {
		t.Fatalf("models = %v", fake.models)
	}
}

func writeFile(dir, name, content string) error {
	return os.WriteFile(dir+"/"+name, []byte(content), 0o644)
}
