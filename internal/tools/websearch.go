package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"quikagent/internal/llm"
)

const (
	websearchTimeout      = 15 * time.Second
	websearchMaxBytes     = 2 * 1024 * 1024
	websearchDefaultLimit = 5
	websearchMaxLimit     = 10
)

type webSearchTool struct {
	endpoint string
	apiKey   string
	http     *http.Client
}

// NewWebSearch builds the websearch tool backed by a user-configured,
// SearXNG-compatible search endpoint. Callers should skip registration
// when the endpoint is unset; if the tool is constructed anyway with an
// empty endpoint, Run returns a "websearch not configured" error.
//
// Unlike fetch, the endpoint here is operator-configured (not
// model-supplied), so fetch's private-IP/loopback SSRF blocking is
// deliberately not applied: a local SearXNG instance on localhost must
// work.
func NewWebSearch(endpoint, apiKey string) Tool {
	return &webSearchTool{
		endpoint: endpoint,
		apiKey:   apiKey,
		http:     &http.Client{Timeout: websearchTimeout},
	}
}

func (w *webSearchTool) ReadOnly() bool { return true }

func (w *webSearchTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "websearch",
		Description: "Search the web via the configured search endpoint (SearXNG-compatible). Returns titles, URLs, and snippets for the top results.",
		Parameters:  []byte(`{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"limit":{"type":"integer","description":"Max results to return (default 5, max 10)"}},"required":["query"]}`),
	}
}

func (w *webSearchTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", errInvalidArg("query is required")
	}
	if w.endpoint == "" {
		return "", fmt.Errorf("websearch not configured: set a search endpoint in the config to enable this tool")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = websearchDefaultLimit
	}
	if limit > websearchMaxLimit {
		limit = websearchMaxLimit
	}

	sep := "?"
	if strings.Contains(w.endpoint, "?") {
		sep = "&"
	}
	reqURL := w.endpoint + sep + "q=" + url.QueryEscape(a.Query) + "&format=json"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", errInvalidArg("bad endpoint: " + err.Error())
	}
	req.Header.Set("User-Agent", "quikagent/0.1")
	req.Header.Set("Accept", "application/json")
	if w.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+w.apiKey)
	}

	res, err := w.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("websearch request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("websearch: endpoint returned HTTP %d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, websearchMaxBytes))
	if err != nil {
		return "", fmt.Errorf("websearch read: %w", err)
	}

	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("websearch: response was not recognized JSON (expected SearXNG-style {\"results\":[...]}): %w", err)
	}
	if parsed.Results == nil {
		return "", fmt.Errorf("websearch: missing results")
	}
	if len(parsed.Results) == 0 {
		return fmt.Sprintf("No results for %q.", a.Query), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Search results for %q:\n", a.Query)
	for i, r := range parsed.Results {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, strings.TrimSpace(r.Title), strings.TrimSpace(r.URL))
		if snippet := strings.TrimSpace(r.Content); snippet != "" {
			fmt.Fprintf(&b, "   %s\n", snippet)
		}
	}
	return truncate(strings.TrimRight(b.String(), "\n")), nil
}
