package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemPromptIncludesAgentsMD(t *testing.T) {
	dir := t.TempDir()
	const marker = "QUIKAGENT_AGENTS_MARKER_42"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Agents\n"+marker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := systemPrompt(Options{Workdir: dir, Model: "test"}, Build)
	if !strings.Contains(got, marker) {
		t.Fatalf("system prompt missing AGENTS.md content:\n%s", got)
	}
	if !strings.Contains(got, "# Project AGENTS.md") {
		t.Fatal("missing AGENTS.md section header")
	}
}

func TestSystemPromptWithoutAgentsMD(t *testing.T) {
	dir := t.TempDir()
	got := systemPrompt(Options{Workdir: dir, Model: "test"}, Build)
	if strings.Contains(got, "# Project AGENTS.md") {
		t.Fatal("unexpected AGENTS.md section when file missing")
	}
	if !strings.Contains(got, "do not keep re-checking the same evidence") {
		t.Fatal("build prompt missing no-ruminate guideline")
	}
}

func TestLoadAgentsMDTruncates(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", maxAgentsMDBytes+4096)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadAgentsMD(dir)
	if len(got) != maxAgentsMDBytes {
		t.Fatalf("len=%d want %d", len(got), maxAgentsMDBytes)
	}
}
