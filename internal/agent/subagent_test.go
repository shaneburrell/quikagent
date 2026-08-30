package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quikagent/internal/llm"
)

func TestExploreSubagentReadOnly(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello\n"), 0o644)
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "write", Arguments: `{"path":"x.txt","content":"no"}`}}},
		{text: "could not write"},
	}}
	a := newTestAgent(dir, fake)
	out, err := a.runSubagent(t.Context(), "explore", "write a file", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); !os.IsNotExist(err) {
		t.Fatal("explore must not write")
	}
	if !strings.Contains(out, "could not write") && out == "" {
		t.Fatalf("out = %q", out)
	}
}

func TestSubagentGeneralRespectsAllowTool(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "write", Arguments: `{"path":"x.txt","content":"no"}`}}},
		{text: "could not write"},
	}}
	a := newTestAgent(dir, fake)
	called := false
	a.SetAllowTool(func(ctx context.Context, name, args string) error {
		called = true
		return fmt.Errorf("parent said no")
	})
	out, err := a.runSubagent(t.Context(), "general", "write a file", "")
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("child did not inherit allowTool")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); !os.IsNotExist(err) {
		t.Fatal("denied write must not create the file")
	}
	if !strings.Contains(out, "could not write") && out == "" {
		t.Fatalf("out = %q", out)
	}
}

func TestSubagentNilErrorIsError(t *testing.T) {
	a := newTestAgent(t.TempDir(), &fakeLLM{scripts: []script{{nilErr: true}}})
	_, err := a.runSubagent(t.Context(), "general", "hi", "")
	if err == nil {
		t.Fatal("expected error for EventError with nil Err")
	}
	if !strings.Contains(err.Error(), "llm") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownSubagent(t *testing.T) {
	a := newTestAgent(t.TempDir(), &fakeLLM{})
	_, err := a.runSubagent(t.Context(), "nope", "hi", "")
	if err == nil || !strings.Contains(err.Error(), "unknown subagent") {
		t.Fatalf("err = %v", err)
	}
}

func TestCustomAgentFrontmatter(t *testing.T) {
	dir := t.TempDir()
	ad := filepath.Join(dir, ".quikagent", "agents")
	_ = os.MkdirAll(ad, 0o755)
	_ = os.WriteFile(filepath.Join(ad, "reviewer.md"), []byte("---\nname: reviewer\nreadonly: true\nmodel: review-model\n---\nOnly review.\n"), 0o644)
	def, ok := loadCustomAgent(dir, "reviewer")
	if !ok || !def.ReadOnly || def.Model != "review-model" || !strings.Contains(def.Prompt, "Only review") {
		t.Fatalf("def = %+v ok=%v", def, ok)
	}
}

func TestParseAgentMD(t *testing.T) {
	def := parseAgentMD("x.md", "just a prompt")
	if def.ID != "x" || def.Prompt != "just a prompt" {
		t.Fatalf("%+v", def)
	}
}
