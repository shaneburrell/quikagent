package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer serves a scripted list of SSE data payloads and records the
// request body for inspection.
func sseServer(t *testing.T, payloads []string, reqBody *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*reqBody = b
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, p := range payloads {
			fmt.Fprintf(w, "data: %s\n\n", p)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func ptr[T any](v T) *T { return &v }

func chunkJSON(t *testing.T, delta map[string]any, finish *string) string {
	t.Helper()
	chunk := map[string]any{
		"object":  "chat.completion.chunk",
		"choices": []map[string]any{{"delta": delta}},
	}
	if finish != nil {
		chunk["choices"].([]map[string]any)[0]["finish_reason"] = *finish
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func collect(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestStreamTextAndReasoning(t *testing.T) {
	var body []byte
	server := sseServer(t, []string{
		chunkJSON(t, map[string]any{"reasoning_content": "hmm "}, nil),
		chunkJSON(t, map[string]any{"reasoning_content": "ok"}, nil),
		chunkJSON(t, map[string]any{"content": "Hello"}, nil),
		chunkJSON(t, map[string]any{"content": " world"}, ptr("stop")),
	}, &body)
	defer server.Close()

	c := New(server.URL, "key", "test-model")
	ch, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, ch)

	var text, reasoning string
	var done bool
	for _, e := range events {
		switch e.Type {
		case EventText:
			text += e.Text
		case EventReasoning:
			reasoning += e.Reasoning
		case EventDone:
			done = true
		case EventError:
			t.Fatalf("unexpected error event: %v", e.Err)
		}
	}
	if !done {
		t.Fatal("no EventDone")
	}
	if text != "Hello world" {
		t.Fatalf("text = %q", text)
	}
	if reasoning != "hmm ok" {
		t.Fatalf("reasoning = %q", reasoning)
	}
	if events[len(events)-1].Message.Content != "Hello world" {
		t.Fatalf("final message content = %q", events[len(events)-1].Message.Content)
	}
}

func TestStreamToolCalls(t *testing.T) {
	finish := "tool_calls"
	var body []byte
	server := sseServer(t, []string{
		chunkJSON(t, map[string]any{"tool_calls": []map[string]any{
			{"index": 0, "id": "call_a", "function": map[string]any{"name": "read"}},
			{"index": 1, "id": "call_b", "function": map[string]any{"name": "bash"}},
		}}, nil),
		chunkJSON(t, map[string]any{"tool_calls": []map[string]any{
			{"index": 0, "function": map[string]any{"arguments": `{"path":"`}},
			{"index": 1, "function": map[string]any{"arguments": `{"command":"ls`}},
		}}, nil),
		chunkJSON(t, map[string]any{"tool_calls": []map[string]any{
			{"index": 0, "function": map[string]any{"arguments": `a.go"}`}},
			{"index": 1, "function": map[string]any{"arguments": ` -la"}`}},
		}}, ptr(finish)),
	}, &body)
	defer server.Close()

	c := New(server.URL, "key", "test-model")
	tools := []Tool{{Name: "read", Description: "d", Parameters: []byte(`{"type":"object"}`)}}
	ch, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "read it"}}, tools, 100)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, ch)

	done := events[len(events)-1]
	if done.Type != EventDone {
		t.Fatalf("last event = %v", done.Type)
	}
	got := done.Message.ToolCalls
	if len(got) != 2 {
		t.Fatalf("tool calls = %d", len(got))
	}
	if got[0].ID != "call_a" || got[0].Name != "read" || got[0].Arguments != `{"path":"a.go"}` {
		t.Fatalf("tool call 0 = %+v", got[0])
	}
	if got[1].ID != "call_b" || got[1].Name != "bash" || got[1].Arguments != `{"command":"ls -la"}` {
		t.Fatalf("tool call 1 = %+v", got[1])
	}

	var req wireRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "test-model" || !req.Stream || len(req.Tools) != 1 || req.Tools[0].Function.Name != "read" {
		t.Fatalf("request = %+v", req)
	}
}

func TestToolResultMessageEncoding(t *testing.T) {
	var body []byte
	server := sseServer(t, []string{chunkJSON(t, map[string]any{"content": "ok"}, ptr("stop"))}, &body)
	defer server.Close()

	c := New(server.URL, "key", "test-model")
	messages := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "do it"},
		{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`}}},
		{Role: RoleTool, ToolCallID: "c1", Name: "bash", Content: "file.txt"},
	}
	ch, err := c.Chat(context.Background(), messages, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)

	var req wireRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 4 {
		t.Fatalf("messages = %d", len(req.Messages))
	}
	toolMsg := req.Messages[3]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "c1" || toolMsg.Content != "file.txt" {
		t.Fatalf("tool message = %+v", toolMsg)
	}
	assistant := req.Messages[2]
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("assistant tool calls = %+v", assistant.ToolCalls)
	}
}

func TestProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Invalid API Key","type":"authentication_error"}}`)
	}))
	defer server.Close()

	c := New(server.URL, "bad", "test-model")
	_, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, 10)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Invalid API Key") {
		t.Fatalf("error = %v", err)
	}
}

func TestUsageCaptured(t *testing.T) {
	payload := `{"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":22}}`
	var body []byte
	server := sseServer(t, []string{payload}, &body)
	defer server.Close()

	c := New(server.URL, "key", "test-model")
	ch, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, ch)
	done := events[len(events)-1]
	if done.Usage == nil || done.Usage.PromptTokens != 11 || done.Usage.CompletionTokens != 22 {
		t.Fatalf("usage = %+v", done.Usage)
	}
}

func TestRetryOn503(t *testing.T) {
	fastHTTPRetry(t)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"message":"busy"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON(t, map[string]any{"content": "ok"}, ptr("stop")))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := New(server.URL, "key", "test-model")
	ch, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, ch)
	if attempts != 3 {
		t.Fatalf("attempts = %d", attempts)
	}
	if events[len(events)-1].Type != EventDone {
		t.Fatalf("last = %+v", events[len(events)-1])
	}
}

func TestRetryExhausted502(t *testing.T) {
	fastHTTPRetry(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"error":{"message":"bad gateway"}}`)
	}))
	defer server.Close()

	c := New(server.URL, "key", "test-model")
	_, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, 10)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "after 4 attempts") || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "is the LLM up") {
		t.Fatalf("err = %v", err)
	}
}

func fastHTTPRetry(t *testing.T) {
	t.Helper()
	old := chatRetryHTTPBase
	chatRetryHTTPBase = time.Millisecond
	t.Cleanup(func() { chatRetryHTTPBase = old })
}

func TestInvalidToolParametersSurfaced(t *testing.T) {
	c := New("http://127.0.0.1:1", "key", "m")
	_, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, []Tool{{
		Name: "bad", Parameters: []byte(`{not-json`),
	}}, 10)
	if err == nil || !strings.Contains(err.Error(), "parameters") {
		t.Fatalf("err = %v, want tool parameters error", err)
	}
}

func TestSetModel(t *testing.T) {
	c := New("http://example", "k", "old")
	c.SetModel("new-model")
	if c.Model() != "new-model" {
		t.Fatalf("model = %q", c.Model())
	}
}

func TestListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"data":[{"id":"z-model"},{"id":"a-model"},{"id":"a-model"}]}`)
	}))
	defer server.Close()
	c := New(server.URL, "key", "x")
	ids, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "a-model" || ids[1] != "z-model" {
		t.Fatalf("%v", ids)
	}
}

func TestContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON(t, map[string]any{"content": "start"}, nil))
		if flusher != nil {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := New(server.URL, "key", "test-model")
	ch, err := c.Chat(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()

	events := collect(t, ch)
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("last event = %+v", last)
	}
	if !errorsIs(last.Err, context.Canceled) {
		t.Fatalf("error = %v", last.Err)
	}
}

func errorsIs(err error, target error) bool {
	return err == target || (err != nil && target != nil && strings.Contains(err.Error(), target.Error()))
}
