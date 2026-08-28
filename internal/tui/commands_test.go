package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectCommands(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".quikagent", "commands"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".quikagent", "commands", "review.md"), []byte("Review the diff."), 0o644)
	cmds := loadProjectCommands(dir)
	if len(cmds) != 1 || cmds[0].Name != "review" || cmds[0].Prompt != "Review the diff." {
		t.Fatalf("%+v", cmds)
	}
	got, ok := lookupProjectCommand(dir, "REVIEW")
	if !ok || got != "Review the diff." {
		t.Fatalf("lookup = %q %v", got, ok)
	}
}

func TestWriteAgentsMD(t *testing.T) {
	dir := t.TempDir()
	created, err := writeAgentsMD(dir)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	created, err = writeAgentsMD(dir)
	if err != nil || created {
		t.Fatalf("second created=%v err=%v", created, err)
	}
}
