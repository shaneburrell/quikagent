package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ChatOnce performs a non-streaming chat completion and returns the
// assistant message content. Used by the Arch-Router (no SSE).
func (c *Client) ChatOnce(ctx context.Context, model string, messages []Message, maxTokens int) (string, error) {
	if model == "" {
		model = c.Model()
	}
	if maxTokens <= 0 {
		maxTokens = 64
	}
	body, err := json.Marshal(wireRequest{
		Model:     model,
		Stream:    false,
		MaxTokens: maxTokens,
		Messages:  toWireMessages(messages),
	})
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	var lastErr error
	var lastStatus int
	for attempt := 0; attempt <= chatMaxRetries; attempt++ {
		if err := waitRetry(ctx, attempt, lastStatus); err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "quikagent-arch-router/1.0")

		res, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			lastStatus = 0
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			continue
		}
		b, readErr := io.ReadAll(io.LimitReader(res.Body, 256*1024))
		res.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if res.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("provider (HTTP %s): %s", res.Status, strings.TrimSpace(string(b)))
			lastStatus = res.StatusCode
			if !retryableStatus(res.StatusCode) {
				return "", lastErr
			}
			continue
		}
		var wrap struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(b, &wrap); err != nil {
			return "", fmt.Errorf("decode response: %w", err)
		}
		if wrap.Error != nil {
			return "", fmt.Errorf("provider error: %s", wrap.Error.Message)
		}
		if len(wrap.Choices) == 0 {
			return "", fmt.Errorf("empty completion")
		}
		return wrap.Choices[0].Message.Content, nil
	}
	return "", exhaustedProviderError(lastStatus, chatMaxRetries+1, lastErr)
}
