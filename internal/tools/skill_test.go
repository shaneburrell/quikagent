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
	if err == nil || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "Available:") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "review") {
		t.Fatalf("miss should list review: %v", err)
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

func TestListSkillNamesEmptyAndSpec(t *testing.T) {
	s := NewSkill(t.TempDir())
	s.home = t.TempDir()
	if names := listSkillNames(s.workdir, s.home); len(names) != 0 {
		t.Fatalf("names = %v", names)
	}
	if !strings.Contains(s.Spec().Description, "none") {
		t.Fatalf("spec = %q", s.Spec().Description)
	}
	_, err := s.Run(context.Background(), json.RawMessage(`{"name":"missing"}`))
	if err == nil || !strings.Contains(err.Error(), "Available: none") {
		t.Fatalf("err = %v", err)
	}
}

func TestListSkillNamesProjectHomeMerge(t *testing.T) {
	workdir := t.TempDir()
	home := t.TempDir()
	writeSkill := func(root, name, body string) {
		t.Helper()
		dir := filepath.Join(root, ".quikagent", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill(workdir, "review", "project")
	writeSkill(home, "review", "home")
	writeSkill(home, "home-only", "home")
	flat := filepath.Join(workdir, ".quikagent", "skills")
	if err := os.WriteFile(filepath.Join(flat, "release.md"), []byte("flat"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Invalid names must be skipped.
	if err := os.MkdirAll(filepath.Join(flat, "..secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flat, "..secret", "SKILL.md"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	names := listSkillNames(workdir, home)
	got := strings.Join(names, ",")
	if got != "home-only,release,review" {
		t.Fatalf("names = %v", names)
	}
	s := &skillTool{workdir: workdir, home: home}
	if !strings.Contains(s.Spec().Description, "review") || !strings.Contains(s.Spec().Description, "release") {
		t.Fatalf("spec = %q", s.Spec().Description)
	}
	out, err := s.Run(context.Background(), json.RawMessage(`{"name":"review"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "project") {
		t.Fatalf("project should win collision: %q", out)
	}
}
