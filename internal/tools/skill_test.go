package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillLoadAndEscape(t *testing.T) {
	dir := t.TempDir()
	sk := filepath.Join(dir, ".quikagent", "skills", "review")
	if err := os.MkdirAll(sk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sk, "SKILL.md"), []byte("# Review\nBe thorough."), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSkill(dir)
	out, err := s.Run(context.Background(), json.RawMessage(`{"name":"review"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Be thorough") {
		t.Fatalf("out = %q", out)
	}
	_, err = s.Run(context.Background(), json.RawMessage(`{"name":"../secret"}`))
	if err == nil {
		t.Fatal("expected invalid name")
	}
	_, err = s.Run(context.Background(), json.RawMessage(`{"name":"missing"}`))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestSkillLoadsFromHome(t *testing.T) {
	workdir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sk := filepath.Join(home, ".quikagent", "skills", "home-review")
	if err := os.MkdirAll(sk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sk, "SKILL.md"), []byte("# Home\nLoaded from ~/.quikagent/skills."), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSkill(workdir)
	out, err := s.Run(context.Background(), json.RawMessage(`{"name":"home-review"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Loaded from ~/.quikagent/skills") {
		t.Fatalf("out = %q", out)
	}
}
