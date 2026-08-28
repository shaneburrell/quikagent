package router

import (
	"context"
	"strings"
	"testing"

	"quikagent/internal/config"
	"quikagent/internal/llm"
)

func TestParseRoute(t *testing.T) {
	routes := config.DefaultRoutes()
	cases := []struct {
		in, want string
	}{
		{`{"route":"nano"}`, "nano"},
		{`{'route': 'qwen'}`, "qwen"},
		{`sure {"route": "other"} ok`, "other"},
		{`route: nano`, "nano"},
		{`garbage`, "other"},
		{`{"route":"nope"}`, "other"},
	}
	for _, c := range cases {
		if got := ParseRoute(c.in, routes); got != c.want {
			t.Fatalf("ParseRoute(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestFormatPromptContainsRoutes(t *testing.T) {
	p, err := FormatPrompt(config.DefaultRoutes(), "write a git commit message")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "<routes>") || !strings.Contains(p, "nano") {
		t.Fatalf("prompt missing routes: %s", p[:min(200, len(p))])
	}
	if !strings.Contains(p, "write a git commit message") {
		t.Fatal("missing user text")
	}
}

type fakeOnce struct {
	content string
	err     error
	model   string
}

func (f *fakeOnce) ChatOnce(ctx context.Context, model string, messages []llm.Message, maxTokens int) (string, error) {
	f.model = model
	return f.content, f.err
}

func TestSelect(t *testing.T) {
	f := &fakeOnce{content: `{"route":"nano"}`}
	r := New(f, config.RouterConfig{Model: "arch-router-1.5b", Routes: config.DefaultRoutes()})
	route, model, err := r.Select(context.Background(), "write a git commit message for staged files")
	if err != nil {
		t.Fatal(err)
	}
	if route != "nano" || model != config.DefaultSmallModel {
		t.Fatalf("route=%s model=%s", route, model)
	}
	if f.model != "arch-router-1.5b" {
		t.Fatalf("called model %q", f.model)
	}
}

func TestSelectFallbackOnError(t *testing.T) {
	f := &fakeOnce{err: context.DeadlineExceeded}
	r := New(f, config.RouterConfig{Routes: config.DefaultRoutes()})
	route, model, err := r.Select(context.Background(), "design a cache")
	if err == nil {
		t.Fatal("expected err")
	}
	if route != "qwen" || model != config.DefaultModel {
		t.Fatalf("route=%s model=%s", route, model)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
