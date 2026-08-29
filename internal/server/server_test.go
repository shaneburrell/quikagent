package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quikagent/internal/agent"
	"quikagent/internal/llm"
	"quikagent/internal/session"
	"quikagent/internal/tools"
)

type fakeAgent struct {
	mode  agent.Mode
	allow agent.AllowFunc
	run   func(ctx context.Context, text string, ev chan<- agent.Event, allow agent.AllowFunc)
	msgs  []llm.Message
}

func (f *fakeAgent) Run(ctx context.Context, userText string, ev chan<- agent.Event) {
	if f.run != nil {
		f.run(ctx, userText, ev, f.allow)
		return
	}
	defer close(ev)
	f.msgs = append(f.msgs,
		llm.Message{Role: llm.RoleUser, Content: userText},
		llm.Message{Role: llm.RoleAssistant, Content: "hello"},
	)
	ev <- agent.Event{Type: agent.EventText, Text: "hello"}
	ev <- agent.Event{Type: agent.EventTurnDone, Usage: &llm.Usage{PromptTokens: 1, CompletionTokens: 2}}
}
func (f *fakeAgent) Mode() agent.Mode                    { return f.mode }
func (f *fakeAgent) SetMode(m agent.Mode)                { f.mode = m }
func (f *fakeAgent) SetAllowTool(fn agent.AllowFunc)     { f.allow = fn }
func (f *fakeAgent) SetOnCompact(fn func([]llm.Message)) {}
func (f *fakeAgent) SetQuestionAsk(fn tools.AskFunc)     {}
func (f *fakeAgent) Messages() []llm.Message             { return f.msgs }
func (f *fakeAgent) LoadHistory(msgs []llm.Message)      { f.msgs = append([]llm.Message(nil), msgs...) }
func (f *fakeAgent) ResetTodos()                         {}

func TestEncodeEvent(t *testing.T) {
	b, err := EncodeEvent(agent.Event{Type: agent.EventText, Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var w wireEvent
	if err := json.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	if w.Type != "text" || w.Text != "hi" {
		t.Fatalf("%+v", w)
	}
	b, err = EncodeEvent(agent.Event{Type: agent.EventStatus, Name: "waiting", Text: "waiting"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	if w.Type != "status" || w.Name != "waiting" || w.Text != "waiting" {
		t.Fatalf("%+v", w)
	}
}

func TestTurnAndSSE(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	ag := &fakeAgent{
		run: func(ctx context.Context, text string, ev chan<- agent.Event, _ agent.AllowFunc) {
			defer close(ev)
			<-started
			ev <- agent.Event{Type: agent.EventText, Text: "hello"}
			ev <- agent.Event{Type: agent.EventTurnDone}
		},
	}
	srv := New(ag, sess)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	eventsDone := make(chan string, 1)
	go func() {
		res, err := http.Get(ts.URL + "/events")
		if err != nil {
			eventsDone <- "err:" + err.Error()
			return
		}
		defer res.Body.Close()
		close(started)
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		deadline := time.After(3 * time.Second)
		for {
			select {
			case <-deadline:
				eventsDone <- string(buf)
				return
			default:
			}
			n, err := res.Body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				if strings.Contains(string(buf), `"type":"turn_done"`) {
					eventsDone <- string(buf)
					return
				}
			}
			if err == io.EOF {
				eventsDone <- string(buf)
				return
			}
			if err != nil {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	res, err := http.Post(ts.URL+"/turn", "application/json", strings.NewReader(`{"prompt":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	got := <-eventsDone
	if !strings.Contains(got, `"type":"text"`) || !strings.Contains(got, `"type":"turn_done"`) {
		t.Fatalf("events = %q", got)
	}
}

func TestDefaultRunSyncsSessionMessages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	ag := &fakeAgent{}
	s := New(ag, sess)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Post(ts.URL+"/turn", "application/json", strings.NewReader(`{"prompt":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		busy := s.busy
		s.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(ag.msgs) < 2 {
		t.Fatalf("agent msgs = %+v", ag.msgs)
	}
	if len(sess.Messages()) < 2 {
		t.Fatalf("session not synced from agent: %+v", sess.Messages())
	}
}

func TestHealth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	srv := New(&fakeAgent{}, sess)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestApproveRequiresExactID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	ag := &fakeAgent{
		run: func(ctx context.Context, text string, ev chan<- agent.Event, allow agent.AllowFunc) {
			defer close(ev)
			defer close(done)
			_ = allow(ctx, "write", `{"path":"x","content":"y"}`)
			ev <- agent.Event{Type: agent.EventTurnDone}
		},
	}
	srv := New(ag, sess)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	events := make(chan string, 1)
	go func() {
		res, err := http.Get(ts.URL + "/events")
		if err != nil {
			events <- err.Error()
			return
		}
		defer res.Body.Close()
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 512)
		deadline := time.After(3 * time.Second)
		for {
			select {
			case <-deadline:
				events <- string(buf)
				return
			default:
			}
			n, err := res.Body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				if strings.Contains(string(buf), `"type":"approve"`) {
					events <- string(buf)
					return
				}
			}
			if err != nil {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	http.Post(ts.URL+"/turn", "application/json", strings.NewReader(`{"prompt":"w"}`))
	payload := <-events
	id := ""
	for _, line := range strings.Split(payload, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var m map[string]any
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		if m["type"] == "approve" {
			id, _ = m["id"].(string)
			break
		}
	}
	if id == "" {
		t.Fatalf("no approve id in %q", payload)
	}
	res, err := http.Post(ts.URL+"/approve", "application/json", strings.NewReader(`{"allow":true}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("empty id status = %d want 409", res.StatusCode)
	}
	// Unblock the turn.
	ares, err := http.Post(ts.URL+"/approve", "application/json",
		strings.NewReader(fmt.Sprintf(`{"id":%q,"allow":false}`, id)))
	if err != nil {
		t.Fatal(err)
	}
	ares.Body.Close()
	<-done
}

func TestApproveDenyMessageUsesToolName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	denied := make(chan error, 1)
	ag := &fakeAgent{
		run: func(ctx context.Context, text string, ev chan<- agent.Event, allow agent.AllowFunc) {
			defer close(ev)
			if allow == nil {
				t.Error("allow func not installed")
				return
			}
			denied <- allow(ctx, "write", `{"path":"x"}`)
			ev <- agent.Event{Type: agent.EventTurnDone}
		},
	}
	srv := New(ag, sess)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	events := make(chan string, 1)
	go func() {
		res, err := http.Get(ts.URL + "/events")
		if err != nil {
			events <- err.Error()
			return
		}
		defer res.Body.Close()
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 512)
		deadline := time.After(3 * time.Second)
		for {
			select {
			case <-deadline:
				events <- string(buf)
				return
			default:
			}
			n, err := res.Body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				if strings.Contains(string(buf), `"type":"approve"`) {
					events <- string(buf)
					return
				}
			}
			if err != nil {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)

	res, err := http.Post(ts.URL+"/turn", "application/json", strings.NewReader(`{"prompt":"write"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	payload := <-events
	id := ""
	for _, line := range strings.Split(payload, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var m map[string]any
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		if m["type"] == "approve" {
			id, _ = m["id"].(string)
			break
		}
	}
	if id == "" {
		t.Fatalf("no approve id in %q", payload)
	}
	ares, err := http.Post(ts.URL+"/approve", "application/json",
		strings.NewReader(fmt.Sprintf(`{"id":%q,"allow":false}`, id)))
	if err != nil {
		t.Fatal(err)
	}
	ares.Body.Close()
	if ares.StatusCode != http.StatusNoContent {
		t.Fatalf("approve status = %d", ares.StatusCode)
	}
	err = <-denied
	if err == nil || !strings.Contains(err.Error(), "user denied write") {
		t.Fatalf("deny error = %v", err)
	}
	if strings.Contains(err.Error(), "bash command") {
		t.Fatalf("deny still uses bash wording: %v", err)
	}
}

func TestSessionsEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	ag := &fakeAgent{}
	s := New(ag, sess)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
		t.Fatalf("body = %s", body)
	}
}

func TestAPIStatePlanAndHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: llm.RoleUser, Content: "hello history"}); err != nil {
		t.Fatal(err)
	}
	ag := &fakeAgent{mode: agent.Plan}
	s := New(ag, sess)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var st struct {
		Mode     string        `json:"mode"`
		Busy     bool          `json:"busy"`
		Session  string        `json:"session_id"`
		Messages []wireMessage `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.Mode != "plan" {
		t.Fatalf("mode = %q", st.Mode)
	}
	if st.Busy {
		t.Fatal("expected idle")
	}
	if st.Session != sess.ID {
		t.Fatalf("session = %q", st.Session)
	}
	if len(st.Messages) != 1 || st.Messages[0].Content != "hello history" {
		t.Fatalf("messages = %+v", st.Messages)
	}
}

func TestAPIStateIncludesPendingApproval(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	s := New(&fakeAgent{}, sess)
	s.mu.Lock()
	s.busy = true
	s.pending = &approvePending{ID: "appr-1", Name: "write", Args: `{"path":"x"}`}
	s.mu.Unlock()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var st struct {
		Busy            bool              `json:"busy"`
		PendingApproval map[string]string `json:"pending_approval"`
		PendingQuestion map[string]any    `json:"pending_question"`
	}
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if !st.Busy {
		t.Fatal("expected busy")
	}
	if st.PendingApproval["id"] != "appr-1" || st.PendingApproval["name"] != "write" {
		t.Fatalf("pending = %+v", st.PendingApproval)
	}
	if st.PendingQuestion != nil {
		t.Fatalf("question = %+v", st.PendingQuestion)
	}
}

func TestApproveAlwaysSkipsNextPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	s := New(&fakeAgent{}, sess)
	done := make(chan error, 1)
	go func() {
		done <- s.allowTool(context.Background(), "write", `{"path":"x"}`)
	}()
	var id string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		if s.pending != nil {
			id = s.pending.ID
		}
		s.mu.Unlock()
		if id != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("timed out waiting for pending approval")
	}
	req := httptest.NewRequest(http.MethodPost, "/approve",
		strings.NewReader(fmt.Sprintf(`{"id":%q,"allow":true,"always":true}`, id)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleApprove(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("approve status = %d", w.Code)
	}
	if err := <-done; err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if !s.alwaysAllow["write"] {
		t.Fatal("HTTP always did not record tool-name allow")
	}
	if err := s.allowTool(context.Background(), "write", `{"path":"y"}`); err != nil {
		t.Fatalf("always allow should skip prompt: %v", err)
	}
}

func TestServerPermissionsDeny(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	s := New(&fakeAgent{}, sess)
	s.SetPermissions(nil, []string{"write", "bash(rm *)"})

	err = s.allowTool(context.Background(), "write", `{"path":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "denied by permissions") {
		t.Fatalf("write deny = %v", err)
	}
	err = s.allowTool(context.Background(), "bash", `{"command":"rm -rf tmp"}`)
	if err == nil || !strings.Contains(err.Error(), "denied by permissions") {
		t.Fatalf("bash deny = %v", err)
	}
	if err := s.allowTool(context.Background(), "read", `{"path":"x"}`); err != nil {
		t.Fatalf("read should auto-allow when no deny rule: %v", err)
	}
}

func TestAnswerSkip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	s := New(&fakeAgent{}, sess)
	ch := make(chan questionResult, 1)
	s.asking = &questionPending{ID: "q1", Q: tools.Question{Prompt: "x"}, ch: ch}
	req := httptest.NewRequest(http.MethodPost, "/answer", strings.NewReader(`{"id":"q1","answer":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleAnswer(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	got := <-ch
	if got.err == nil || !strings.Contains(got.err.Error(), "skipped") {
		t.Fatalf("%+v", got)
	}
}

func TestSessionNewAndResume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(llm.Message{Role: llm.RoleUser, Content: "old"}); err != nil {
		t.Fatal(err)
	}
	ag := &fakeAgent{}
	s := New(ag, sess)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/session/new", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("new status = %d", res.StatusCode)
	}
	if s.sess.ID == sess.ID {
		t.Fatal("expected a new session id")
	}
	if len(ag.msgs) != 0 {
		t.Fatalf("history = %+v", ag.msgs)
	}

	res, err = http.Post(ts.URL+"/session/resume", "application/json",
		strings.NewReader(fmt.Sprintf(`{"id":%q}`, sess.ID)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("resume status = %d", res.StatusCode)
	}
	if s.sess.ID != sess.ID {
		t.Fatalf("resumed %q want %q", s.sess.ID, sess.ID)
	}
	if len(ag.msgs) != 1 || ag.msgs[0].Content != "old" {
		t.Fatalf("resumed msgs = %+v", ag.msgs)
	}
}

func TestResumeWhileBusy409(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	ag := &fakeAgent{
		run: func(ctx context.Context, text string, ev chan<- agent.Event, _ agent.AllowFunc) {
			defer close(ev)
			<-block
			ev <- agent.Event{Type: agent.EventTurnDone}
		},
		msgs: []llm.Message{{Role: llm.RoleUser, Content: "live"}},
	}
	s := New(ag, sess)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/turn", "application/json", strings.NewReader(`{"prompt":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("turn status = %d", res.StatusCode)
	}

	other, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	res, err = http.Post(ts.URL+"/session/resume", "application/json",
		strings.NewReader(fmt.Sprintf(`{"id":%q}`, other.ID)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("resume status = %d want 409", res.StatusCode)
	}
	if s.sess.ID != sess.ID {
		t.Fatal("busy resume swapped the session")
	}
	if len(ag.msgs) != 1 || ag.msgs[0].Content != "live" {
		t.Fatalf("history swapped: %+v", ag.msgs)
	}
	close(block)
}

func TestSessionSwitchClearsAlwaysAllowAndCompacted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	s := New(&fakeAgent{}, sess)
	s.alwaysAllow["write"] = true
	s.todos = []tools.TodoItem{{Content: "old"}}
	s.compacted.Store(true)

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Post(ts.URL+"/session/new", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if s.alwaysAllow["write"] {
		t.Fatal("alwaysAllow survived new session")
	}
	if len(s.todos) != 0 {
		t.Fatalf("todos = %+v", s.todos)
	}
	if s.compacted.Load() {
		t.Fatal("compacted not cleared")
	}
}

func TestBroadcastReliableTurnDone(t *testing.T) {
	s := &Server{subscribers: map[chan []byte]struct{}{}}
	ch := make(chan []byte, 1)
	s.subscribers[ch] = struct{}{}
	s.broadcast([]byte(`{"type":"text"}`), false)
	s.broadcast([]byte(`{"type":"turn_done"}`), true)
	got := <-ch
	if !strings.Contains(string(got), "turn_done") {
		t.Fatalf("got %s", got)
	}
}

func TestHandleStateUsesLiveAgentMessages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	ag := &fakeAgent{msgs: []llm.Message{
		{Role: llm.RoleUser, Content: "from agent"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{Name: "read", Arguments: `{"path":"x"}`}}},
	}}
	s := New(ag, sess)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var st struct {
		Messages []wireMessage `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if len(st.Messages) != 2 || st.Messages[0].Content != "from agent" {
		t.Fatalf("messages = %+v", st.Messages)
	}
	if len(st.Messages[1].ToolCalls) != 1 || st.Messages[1].ToolCalls[0].Name != "read" {
		t.Fatalf("tool-only turn omitted: %+v", st.Messages[1])
	}
}

func TestModeWhileBusyOK(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	ag := &fakeAgent{
		run: func(ctx context.Context, text string, ev chan<- agent.Event, _ agent.AllowFunc) {
			defer close(ev)
			<-block
			ev <- agent.Event{Type: agent.EventTurnDone}
		},
	}
	s := New(ag, sess)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Post(ts.URL+"/turn", "application/json", strings.NewReader(`{"prompt":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res, err = http.Post(ts.URL+"/mode", "application/json", strings.NewReader(`{"mode":"plan"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("mode status = %d", res.StatusCode)
	}
	if ag.Mode() != agent.Plan {
		t.Fatalf("mode = %s", ag.Mode())
	}
	close(block)
}
