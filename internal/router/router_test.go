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
		{`garbage`, ""},
		{`{"route":"nope"}`, ""},
	}
	for _, c := range cases {
		if got := ParseRoute(c.in, routes); got != c.want {
			t.Fatalf("ParseRoute(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestFormatPromptContainsRoutes(t *testing.T) {
	p, err := FormatPrompt(config.DefaultRoutes(), "write a git commit message", "build")
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
	route, model, raw, err := r.Select(context.Background(), "write a git commit message for staged files", "build")
	if err != nil {
		t.Fatal(err)
	}
	if route != "nano" || model != config.DefaultSmallModel {
		t.Fatalf("route=%s model=%s", route, model)
	}
	if !strings.Contains(raw, "nano") {
		t.Fatalf("raw = %q", raw)
	}
	if f.model != "arch-router-1.5b" {
		t.Fatalf("called model %q", f.model)
	}
}

func TestSelectFallbackOnError(t *testing.T) {
	f := &fakeOnce{err: context.DeadlineExceeded}
	r := New(f, config.RouterConfig{Routes: config.DefaultRoutes()})
	route, model, _, err := r.Select(context.Background(), "design a cache", "build")
	if err == nil {
		t.Fatal("expected err")
	}
	if route != "qwen" || model != config.DefaultModel {
		t.Fatalf("route=%s model=%s", route, model)
	}
}

func TestFormatPromptPlanPrefix(t *testing.T) {
	plan, err := FormatPrompt(config.DefaultRoutes(), "lets plan a tool", "plan")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "plan mode") || !strings.Contains(plan, "lets plan a tool") {
		t.Fatalf("plan prompt = %s", plan[:min(300, len(plan))])
	}
	build, err := FormatPrompt(config.DefaultRoutes(), "lets plan a tool", "build")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(build, "The user is in plan mode") {
		t.Fatal("build prompt should not include the plan-mode prefix")
	}
}

func TestSelectGarbageFallsBackToQwen(t *testing.T) {
	f := &fakeOnce{content: `not json at all`}
	r := New(f, config.RouterConfig{Routes: config.DefaultRoutes()})
	route, model, raw, err := r.Select(context.Background(), "hello", "build")
	if err != nil {
		t.Fatal(err)
	}
	if route != "qwen" || model != config.DefaultModel {
		t.Fatalf("route=%s model=%s", route, model)
	}
	if raw != "not json at all" {
		t.Fatalf("raw=%q", raw)
	}
}

func TestSelectOtherDoesNotSwitch(t *testing.T) {
	f := &fakeOnce{content: `{"route":"other"}`}
	r := New(f, config.RouterConfig{Routes: config.DefaultRoutes()})
	route, model, _, err := r.Select(context.Background(), "hello", "build")
	if err != nil {
		t.Fatal(err)
	}
	if route != "other" || model != "" {
		t.Fatalf("route=%s model=%q (want empty model)", route, model)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
