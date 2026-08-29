package session

import (
	"os"
	"strings"
	"testing"
	"time"

	"quikagent/internal/llm"
)

// pointDirAt redirects session storage for tests.
func pointDirAt(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestCreateAppendAndReload(t *testing.T) {
	pointDirAt(t)
	s, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	assistant := llm.Message{
		Role:      llm.RoleAssistant,
		Content:   "",
		Reasoning: "thinking",
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`}},
	}
	if err := s.Append(assistant); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Name: "bash", Content: "out"}); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	msgs := loaded.Messages()
	if len(msgs) != 3 {
		t.Fatalf("messages = %d", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Fatalf("m0 = %+v", msgs[0])
	}
	if msgs[1].Reasoning != "thinking" || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].Arguments != `{"command":"ls"}` {
		t.Fatalf("m1 = %+v", msgs[1])
	}
	if msgs[2].Role != llm.RoleTool || msgs[2].ToolCallID != "c1" {
		t.Fatalf("m2 = %+v", msgs[2])
	}
}

func TestAppendOverwritesNotAppendsLines(t *testing.T) {
	pointDirAt(t)
	s, _ := Create()
	_ = s.Append(llm.Message{Role: llm.RoleUser, Content: "one"})
	_ = s.Append(llm.Message{Role: llm.RoleUser, Content: "two"})

	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := countLines(string(data)); got != 2 {
		t.Fatalf("lines = %d", got)
	}
}

func TestReplace(t *testing.T) {
	pointDirAt(t)
	s, _ := Create()
	_ = s.Append(llm.Message{Role: llm.RoleUser, Content: "old1"})
	_ = s.Append(llm.Message{Role: llm.RoleUser, Content: "old2"})
	if err := s.Replace([]llm.Message{{Role: llm.RoleUser, Content: "compacted"}}); err != nil {
		t.Fatal(err)
	}
	if len(s.Messages()) != 1 || s.Messages()[0].Content != "compacted" {
		t.Fatalf("messages = %+v", s.Messages())
	}
	loaded, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages()) != 1 || loaded.Messages()[0].Content != "compacted" {
		t.Fatalf("reloaded = %+v", loaded.Messages())
	}
}

func TestList(t *testing.T) {
	pointDirAt(t)
	s1, _ := Create()
	_ = s1.Append(llm.Message{Role: llm.RoleUser, Content: "first prompt"})
	time.Sleep(11 * time.Millisecond)
	s2, _ := Create()
	_ = s2.Append(llm.Message{Role: llm.RoleUser, Content: "second prompt"})

	infos, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("list len = %d", len(infos))
	}
	if infos[0].ID != s1.ID || infos[1].ID != s2.ID {
		t.Fatalf("order = %v want %s,%s", infos, s1.ID, s2.ID)
	}
	if infos[0].Preview != "first prompt" || infos[0].MsgCount != 1 {
		t.Fatalf("info0 = %+v", infos[0])
	}
}

func TestLatestReturnsNewest(t *testing.T) {
	pointDirAt(t)
	s1, _ := Create()
	_ = s1.Append(llm.Message{Role: llm.RoleUser, Content: "old"})
	time.Sleep(11 * time.Millisecond)
	s2, _ := Create()
	_ = s2.Append(llm.Message{Role: llm.RoleUser, Content: "new"})

	latest, err := Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != s2.ID {
		t.Fatalf("latest = %s want %s", latest.ID, s2.ID)
	}
	if latest.Messages()[0].Content != "new" {
		t.Fatalf("content = %q", latest.Messages()[0].Content)
	}
}

func TestLatestNone(t *testing.T) {
	pointDirAt(t)
	if _, err := Latest(); err == nil {
		t.Fatal("expected error with no sessions")
	}
}

func TestLoadMissing(t *testing.T) {
	pointDirAt(t)
	if _, err := Load("does-not-exist"); err == nil {
		t.Fatal("expected error")
	}
}

func countLines(s string) int {
	n := 0
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	return n
}

func TestLoadRejectsPathTraversal(t *testing.T) {
	pointDirAt(t)
	_, err := Load("../evil")
	if err == nil {
		t.Fatal("expected invalid session id")
	}
	_, err = Load("abc")
	if err == nil {
		t.Fatal("expected invalid session id format")
	}
}

func TestLoadSkipsCorruptLines(t *testing.T) {
	pointDirAt(t)
	s, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{Role: llm.RoleUser, Content: "keep-me"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte("{not-json\n"), data...)
	if err := os.WriteFile(s.Path(), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SkippedCorrupt != 1 {
		t.Fatalf("SkippedCorrupt = %d", loaded.SkippedCorrupt)
	}
	msgs := loaded.Messages()
	if len(msgs) != 1 || msgs[0].Content != "keep-me" {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestLoadAllCorruptFails(t *testing.T) {
	pointDirAt(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	id := "1234567890-abcd"
	path := dir + "/" + id + ".jsonl"
	if err := os.WriteFile(path, []byte("{bad\nalso-bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(id)
	if err == nil {
		t.Fatal("expected error for all-corrupt session")
	}
}

func TestTitleRoundTrip(t *testing.T) {
	pointDirAt(t)
	s, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{Role: llm.RoleUser, Content: "long prompt body"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle("fix the sidebar"); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "fix the sidebar" {
		t.Fatalf("title = %q", loaded.Title)
	}
	infos, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Title != "fix the sidebar" || infos[0].Preview != "fix the sidebar" {
		t.Fatalf("infos = %+v", infos)
	}
}

func TestEnsureTitleFromFirstUser(t *testing.T) {
	pointDirAt(t)
	s, _ := Create()
	_ = s.Append(llm.Message{Role: llm.RoleUser, Content: "add undo/redo\nmore detail"})
	s.EnsureTitle()
	if s.Title != "add undo/redo" {
		t.Fatalf("title = %q", s.Title)
	}
	s.EnsureTitle() // no overwrite
	if s.Title != "add undo/redo" {
		t.Fatalf("title changed = %q", s.Title)
	}
}

func TestAppendAndReadTraces(t *testing.T) {
	pointDirAt(t)
	s, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTrace(TraceRecord{Type: "turn_start", Turn: "t1", Mode: "build", Model: "qwen"}); err != nil {
		t.Fatal(err)
	}
	ok := true
	if err := s.AppendTrace(TraceRecord{Type: "turn_end", Turn: "t1", OK: &ok, Steps: 1, MS: 12}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(s.TracePath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o", st.Mode().Perm())
	}
	recs, skipped, err := s.ReadTraces()
	if err != nil || skipped != 0 {
		t.Fatalf("err=%v skipped=%d", err, skipped)
	}
	if len(recs) != 2 || recs[0].Type != "turn_start" || recs[0].V != TraceVersion {
		t.Fatalf("recs = %+v", recs)
	}
	if recs[1].Type != "turn_end" || recs[1].OK == nil || !*recs[1].OK {
		t.Fatalf("end = %+v", recs[1])
	}
}

func TestReadTracesSkipsCorrupt(t *testing.T) {
	pointDirAt(t)
	s, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.TracePath(), []byte("{bad\n{\"v\":1,\"ts\":\"t\",\"type\":\"compact\",\"before\":4,\"after\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, skipped, err := s.ReadTraces()
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(recs) != 1 || recs[0].Type != "compact" {
		t.Fatalf("recs=%+v skipped=%d", recs, skipped)
	}
}

func TestListIgnoresTraceSidecar(t *testing.T) {
	pointDirAt(t)
	s, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	if err := s.AppendTrace(TraceRecord{Type: "turn_start", Turn: "t1"}); err != nil {
		t.Fatal(err)
	}
	infos, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != s.ID {
		t.Fatalf("infos = %+v", infos)
	}
}

func TestFormatTraces(t *testing.T) {
	ok := true
	md := FormatTraces([]TraceRecord{
		{Type: "turn_start", Turn: "1", Mode: "build", Model: "qwen", Frontend: "print"},
		{Type: "tool", Name: "read", MS: 3, Outcome: "ok"},
		{Type: "turn_end", Turn: "1", MS: 10, Steps: 1, OK: &ok},
	})
	if !strings.Contains(md, "## Trace") || !strings.Contains(md, "tool read") || !strings.Contains(md, "frontend=print") {
		t.Fatalf("md = %s", md)
	}
}

func TestTitleFromPrompt(t *testing.T) {
	if got := TitleFromPrompt("  hello\nworld  "); got != "hello" {
		t.Fatalf("%q", got)
	}
	long := strings.Repeat("日", 90)
	got := TitleFromPrompt(long)
	if got != strings.Repeat("日", 80)+"…" {
		t.Fatalf("utf8 clip = %q", got)
	}
}
