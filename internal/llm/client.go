package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	chatMaxRetries = 3
	chatRetryBase  = 200 * time.Millisecond
)

// Client talks to an OpenAI-compatible /chat/completions endpoint.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
	mu      sync.RWMutex
}

// New builds a Client. baseURL should include the API prefix, e.g.
// "https://llm.example.com/v1".
func New(baseURL, apiKey, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 30 * time.Minute},
	}
}

// Model returns the configured model name.
func (c *Client) Model() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.model
}

// SetModel updates the model used for subsequent Chat calls.
func (c *Client) SetModel(model string) {
	if model == "" {
		return
	}
	c.mu.Lock()
	c.model = model
	c.mu.Unlock()
}

// SetAPIKey updates the bearer token used for subsequent Chat calls.
func (c *Client) SetAPIKey(key string) { c.apiKey = key }

// SetBaseURL updates the API base URL (trailing slash stripped).
func (c *Client) SetBaseURL(baseURL string) {
	if baseURL != "" {
		c.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// BaseURL returns the configured API base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// ListModels fetches model IDs from GET {baseURL}/models (OpenAI-compatible).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse models: %w", err)
	}
	seen := map[string]bool{}
	var ids []string
	for _, m := range parsed.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// Chat starts a streamed completion. The returned channel yields deltas and
// ends with a single EventDone or EventError, then closes. The returned
// error covers request setup only. Transient failures (429, 5xx, transport)
// are retried before any stream tokens are emitted.
func (c *Client) Chat(ctx context.Context, messages []Message, tools []Tool, maxTokens int) (<-chan Event, error) {
	return c.ChatAs(ctx, c.Model(), messages, tools, maxTokens)
}

// ChatAs is Chat with an explicit model (empty uses the client default).
func (c *Client) ChatAs(ctx context.Context, model string, messages []Message, tools []Tool, maxTokens int) (<-chan Event, error) {
	if model == "" {
		model = c.Model()
	}
	toolDefs, err := toWireToolDefs(tools)
	if err != nil {
		return nil, fmt.Errorf("encode tools: %w", err)
	}
	body, err := json.Marshal(wireRequest{
		Model:     model,
		Stream:    true,
		MaxTokens: maxTokens,
		Messages:  toWireMessages(messages),
		Tools:     toolDefs,
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var res *http.Response
	var lastErr error
	for attempt := 0; attempt <= chatMaxRetries; attempt++ {
		if attempt > 0 {
			delay := chatRetryBase << (attempt - 1)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")

		res, err = c.http.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if res.StatusCode == http.StatusOK {
			ch := make(chan Event, 64)
			go c.stream(ctx, res.Body, ch)
			return ch, nil
		}
		err = apiError(res)
		res.Body.Close()
		lastErr = err
		if !retryableStatus(res.StatusCode) {
			return nil, err
		}
	}
	return nil, lastErr
}

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// stream reads the SSE body and translates chunks into Events. It always
// emits exactly one terminating event (EventDone or EventError).
func (c *Client) stream(ctx context.Context, body io.ReadCloser, ch chan<- Event) {
	defer close(ch)
	defer body.Close()

	var (
		text      strings.Builder
		reasoning strings.Builder
		toolCalls = map[int]*wireToolCall{}
		order     []int
		usage     *Usage
		finished  bool
	)

	accToolCalls := func() []ToolCall {
		out := make([]ToolCall, 0, len(order))
		for _, i := range order {
			tc := toolCalls[i]
			out = append(out, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}
		return out
	}

	complete := func() {
		if finished {
			return
		}
		finished = true
		ch <- Event{
			Type: EventDone,
			Message: &Message{
				Role:      RoleAssistant,
				Content:   text.String(),
				Reasoning: reasoning.String(),
				ToolCalls: accToolCalls(),
			},
			Usage: usage,
		}
	}

	fail := func(err error) {
		if finished {
			return
		}
		finished = true
		ch <- Event{Type: EventError, Err: err}
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			fail(ctx.Err())
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var chunk wireChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			fail(fmt.Errorf("decode chunk: %w", err))
			return
		}
		if chunk.Error != nil {
			fail(fmt.Errorf("provider error: %s", chunk.Error.Message))
			return
		}
		if chunk.Usage != nil {
			usage = &Usage{PromptTokens: chunk.Usage.PromptTokens, CompletionTokens: chunk.Usage.CompletionTokens}
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta
			if delta.Content != "" {
				text.WriteString(delta.Content)
				ch <- Event{Type: EventText, Text: delta.Content}
			}
			if delta.ReasoningContent != "" {
				reasoning.WriteString(delta.ReasoningContent)
				ch <- Event{Type: EventReasoning, Reasoning: delta.ReasoningContent}
			}
			for _, tc := range delta.ToolCalls {
				acc, ok := toolCalls[tc.Index]
				if !ok {
					acc = &wireToolCall{Index: tc.Index}
					toolCalls[tc.Index] = acc
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					acc.ID = tc.ID
				}
				if tc.Function.Name != "" {
					acc.Function.Name += tc.Function.Name
				}
				acc.Function.Arguments += tc.Function.Arguments
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				complete()
			}
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		fail(fmt.Errorf("read stream: %w", err))
		return
	}
	if ctx.Err() != nil {
		fail(ctx.Err())
		return
	}
	complete()
}

// apiError extracts a readable error from a non-200 response.
func apiError(res *http.Response) error {
	b, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return fmt.Errorf("HTTP %s", res.Status)
	}
	var wrap struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &wrap) == nil && wrap.Error.Message != "" {
		return fmt.Errorf("provider (HTTP %s): %s", res.Status, wrap.Error.Message)
	}
	return fmt.Errorf("provider (HTTP %s): %s", res.Status, strings.TrimSpace(string(b)))
}

// ---- wire format ----

type wireRequest struct {
	Model     string        `json:"model"`
	Stream    bool          `json:"stream"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Messages  []wireMessage `json:"messages"`
	Tools     []wireToolDef `json:"tools,omitempty"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type wireToolDef struct {
	Type     string          `json:"type"`
	Function wireFunctionDef `json:"function"`
}

type wireFunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// wireToolCall is used both in requests (Index omitted) and in stream
// deltas, where Index distinguishes concurrent calls.
type wireToolCall struct {
	Index    int              `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function wireFunctionCall `json:"function"`
}

type wireFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Content          string         `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			ToolCalls        []wireToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// toWireMessages converts domain messages to the wire format. Assistant
// reasoning is intentionally dropped: providers do not take it back.
func toWireMessages(messages []Message) []wireMessage {
	out := make([]wireMessage, 0, len(messages))
	for _, m := range messages {
		w := wireMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID, Name: m.Name}
		for _, tc := range m.ToolCalls {
			w.ToolCalls = append(w.ToolCalls, wireToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: wireFunctionCall{Name: tc.Name, Arguments: tc.Arguments},
			})
		}
		out = append(out, w)
	}
	return out
}

func toWireToolDefs(tools []Tool) ([]wireToolDef, error) {
	out := make([]wireToolDef, 0, len(tools))
	for _, t := range tools {
		var params any
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &params); err != nil {
				return nil, fmt.Errorf("tool %s parameters: %w", t.Name, err)
			}
		} else {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, wireToolDef{
			Type:     "function",
			Function: wireFunctionDef{Name: t.Name, Description: t.Description, Parameters: params},
		})
	}
	return out, nil
}
