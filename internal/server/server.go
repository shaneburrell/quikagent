// Package server exposes the agent over HTTP with SSE event streaming.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"quikagent/internal/agent"
	"quikagent/internal/llm"
	"quikagent/internal/session"
	"quikagent/internal/tools"
)

// AgentRunner is the subset of agent.Agent the server needs.
type AgentRunner interface {
	Run(ctx context.Context, userText string, ev chan<- agent.Event)
	Mode() agent.Mode
	SetMode(m agent.Mode)
	SetAllowTool(fn agent.AllowFunc)
	SetOnCompact(fn func([]llm.Message))
	SetQuestionAsk(fn tools.AskFunc)
	Messages() []llm.Message
	LoadHistory(messages []llm.Message)
	ResetTodos()
}

type approvePending struct {
	ID   string
	Name string
	Args string
	ch   chan error
}

type questionPending struct {
	ID string
	Q  tools.Question
	ch chan questionResult
}

type questionResult struct {
	answer string
	err    error
}

// Server hosts a single-session web UI over the agent event stream.
type Server struct {
	agent AgentRunner
	sess  *session.Session
	mux   *http.ServeMux

	mu          sync.Mutex
	busy        bool
	pendingBase int
	lastRoute   string
	turnCancel  context.CancelFunc
	subscribers map[chan []byte]struct{}
	pending     *approvePending
	asking      *questionPending

	permAllow   []string
	permDeny    []string
	alwaysAllow map[string]bool
	todos       []tools.TodoItem

	// Set from the agent goroutine when Compact() rewrote history;
	// syncSession replaces the session instead of appending.
	compacted atomic.Bool
}

// New builds an HTTP handler for the given agent and session.
func New(ag AgentRunner, sess *session.Session) *Server {
	s := &Server{
		agent:       ag,
		sess:        sess,
		mux:         http.NewServeMux(),
		subscribers: map[chan []byte]struct{}{},
		pendingBase: len(sess.Messages()),
		alwaysAllow: map[string]bool{},
	}
	ag.SetAllowTool(s.allowTool)
	ag.SetQuestionAsk(s.askQuestion)
	ag.SetOnCompact(func([]llm.Message) { s.compacted.Store(true) })
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /events", s.handleEvents)
	s.mux.HandleFunc("POST /turn", s.handleTurn)
	s.mux.HandleFunc("POST /cancel", s.handleCancel)
	s.mux.HandleFunc("POST /mode", s.handleMode)
	s.mux.HandleFunc("POST /approve", s.handleApprove)
	s.mux.HandleFunc("POST /answer", s.handleAnswer)
	s.mux.HandleFunc("GET /sessions", s.handleSessions)
	s.mux.HandleFunc("GET /api/state", s.handleState)
	s.mux.HandleFunc("POST /session/resume", s.handleSessionResume)
	s.mux.HandleFunc("POST /session/new", s.handleSessionNew)
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return s
}

// SetPermissions installs config-driven allow/deny rules checked before
// falling back to interactive approval.
func (s *Server) SetPermissions(allow, deny []string) {
	s.mu.Lock()
	s.permAllow = append([]string(nil), allow...)
	s.permDeny = append([]string(nil), deny...)
	s.mu.Unlock()
}

func (s *Server) allowTool(ctx context.Context, name, args string) error {
	s.mu.Lock()
	allow, deny := s.permAllow, s.permDeny
	s.mu.Unlock()
	switch tools.CheckPermission(allow, deny, name, args) {
	case tools.MatchDeny:
		return fmt.Errorf("denied by permissions")
	case tools.MatchAllow:
		return nil
	}

	if !tools.NeedsInteractiveApproval(name, args) {
		return nil
	}
	s.mu.Lock()
	if s.alwaysAllow[name] {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	ch := make(chan error, 1)
	s.mu.Lock()
	s.pending = &approvePending{ID: id, Name: name, Args: args, ch: ch}
	s.mu.Unlock()
	payload, _ := json.Marshal(map[string]string{
		"type": "approve", "id": id, "name": name, "args": args,
	})
	s.broadcast(payload, true)
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string `json:"id"`
		Allow  bool   `json:"allow"`
		Always bool   `json:"always"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	p := s.pending
	if p == nil || body.ID == "" || p.ID != body.ID {
		s.mu.Unlock()
		http.Error(w, "no pending approval", http.StatusConflict)
		return
	}
	s.pending = nil
	if body.Allow && body.Always {
		if s.alwaysAllow == nil {
			s.alwaysAllow = map[string]bool{}
		}
		s.alwaysAllow[p.Name] = true
	}
	s.mu.Unlock()
	if body.Allow {
		p.ch <- nil
	} else {
		p.ch <- fmt.Errorf("user denied %s", p.Name)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) askQuestion(ctx context.Context, q tools.Question) (string, error) {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	ch := make(chan questionResult, 1)
	s.mu.Lock()
	s.asking = &questionPending{ID: id, Q: q, ch: ch}
	s.mu.Unlock()
	payload, _ := json.Marshal(map[string]any{
		"type": "question", "id": id, "header": q.Header, "question": q.Prompt, "options": q.Options,
	})
	s.broadcast(payload, true)
	select {
	case r := <-ch:
		return r.answer, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string `json:"id"`
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	p := s.asking
	if p == nil || body.ID == "" || p.ID != body.ID {
		s.mu.Unlock()
		http.Error(w, "no pending question", http.StatusConflict)
		return
	}
	s.asking = nil
	s.mu.Unlock()
	ans := strings.TrimSpace(body.Answer)
	if ans == "" {
		p.ch <- questionResult{err: fmt.Errorf("user skipped question")}
	} else {
		p.ch <- questionResult{answer: ans}
	}
	w.WriteHeader(http.StatusNoContent)
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) handleTurn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
		http.Error(w, "prompt required", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		http.Error(w, "turn already in progress", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.busy = true
	s.turnCancel = cancel
	s.pendingBase = len(s.agent.Messages())
	ev := make(chan agent.Event, 64)
	turnSess := s.sess
	sessID := ""
	if turnSess != nil {
		sessID = turnSess.ID
	}
	go s.agent.Run(ctx, body.Prompt, ev)
	go s.forward(ev, turnSess)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "started", "session": sessID})
}

func (s *Server) forward(ev <-chan agent.Event, turnSess *session.Session) {
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.turnCancel = nil
		if s.pending != nil {
			s.pending.ch <- fmt.Errorf("turn ended")
			s.pending = nil
		}
		if s.asking != nil {
			s.asking.ch <- questionResult{err: fmt.Errorf("turn ended")}
			s.asking = nil
		}
		s.syncSession(turnSess)
		s.mu.Unlock()
	}()
	for e := range ev {
		if e.Type == agent.EventRoute {
			s.mu.Lock()
			s.lastRoute = e.Name
			s.mu.Unlock()
		}
		if e.Type == agent.EventTodos {
			s.mu.Lock()
			s.todos = append([]tools.TodoItem(nil), e.Todos...)
			s.mu.Unlock()
		}
		payload, err := encodeEvent(e)
		if err != nil {
			log.Printf("quikagent: encode event: %v", err)
			continue
		}
		s.broadcast(payload, e.Type == agent.EventTurnDone || e.Type == agent.EventError)
	}
}

func (s *Server) syncSession(turnSess *session.Session) {
	if turnSess == nil {
		return
	}
	if s.sess != turnSess {
		// Session was switched mid-turn; do not persist into the new file.
		return
	}
	msgs := s.agent.Messages()
	if s.compacted.Swap(false) {
		_ = turnSess.Replace(msgs)
		s.pendingBase = len(turnSess.Messages())
		return
	}
	if len(msgs) <= s.pendingBase {
		return
	}
	for _, m := range msgs[s.pendingBase:] {
		_ = turnSess.Append(m)
	}
	s.pendingBase = len(turnSess.Messages())
	turnSess.EnsureTitle()
}

func (s *Server) handleSessionResume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		http.Error(w, "turn already in progress", http.StatusConflict)
		return
	}
	s.busy = true
	s.mu.Unlock()
	loaded, err := session.Load(body.ID)
	s.mu.Lock()
	s.busy = false
	if err != nil {
		s.mu.Unlock()
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if s.turnCancel != nil {
		// A turn started while we loaded from disk.
		s.mu.Unlock()
		http.Error(w, "turn already in progress", http.StatusConflict)
		return
	}
	s.applySessionLocked(loaded)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "session": loaded.ID})
}

func (s *Server) handleSessionNew(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		http.Error(w, "turn already in progress", http.StatusConflict)
		return
	}
	s.busy = true
	s.mu.Unlock()
	created, err := session.Create()
	s.mu.Lock()
	s.busy = false
	if err != nil {
		s.mu.Unlock()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.turnCancel != nil {
		s.mu.Unlock()
		http.Error(w, "turn already in progress", http.StatusConflict)
		return
	}
	s.applySessionLocked(created)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "session": created.ID})
}

func (s *Server) applySessionLocked(sess *session.Session) {
	s.sess = sess
	s.pendingBase = 0
	if sess != nil {
		s.pendingBase = len(sess.Messages())
	}
	s.todos = nil
	s.lastRoute = ""
	s.alwaysAllow = map[string]bool{}
	s.compacted.Store(false)
	if s.agent != nil {
		var msgs []llm.Message
		if sess != nil {
			msgs = sess.Messages()
		}
		s.agent.LoadHistory(msgs)
		s.agent.ResetTodos()
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, _ *http.Request) {
	infos, err := session.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type row struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Preview string `json:"preview"`
		Msgs    int    `json:"messages"`
	}
	out := make([]row, 0, len(infos))
	for _, i := range infos {
		out = append(out, row{ID: i.ID, Title: i.Title, Preview: i.Preview, Msgs: i.MsgCount})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleCancel(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	cancel := s.turnCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	switch body.Mode {
	case "plan":
		s.agent.SetMode(agent.Plan)
	case "build":
		s.agent.SetMode(agent.Build)
	default:
		http.Error(w, "mode must be plan or build", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"mode": body.Mode})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ch := make(chan []byte, 32)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
		close(ch)
	}()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (s *Server) broadcast(payload []byte, reliable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- payload:
		default:
			if !reliable {
				continue
			}
			// Drop the oldest queued event so turn_done/error still lands.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- payload:
			default:
			}
		}
	}
}

type wireEvent struct {
	Type   string     `json:"type"`
	Text   string     `json:"text,omitempty"`
	Output string     `json:"output,omitempty"`
	Name   string     `json:"name,omitempty"`
	Args   string     `json:"args,omitempty"`
	Model  string     `json:"model,omitempty"`
	Error  string     `json:"error,omitempty"`
	Prompt int        `json:"prompt_tokens,omitempty"`
	Compl  int        `json:"completion_tokens,omitempty"`
	Todos  []wireTodo `json:"todos,omitempty"`
}

type wireTodo struct {
	Content  string `json:"content"`
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
}

type wireToolCall struct {
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

type wireMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Name      string         `json:"name,omitempty"`
	ToolCalls []wireToolCall `json:"tool_calls,omitempty"`
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	busy := s.busy
	route := s.lastRoute
	todos := append([]tools.TodoItem(nil), s.todos...)
	id := ""
	if s.sess != nil {
		id = s.sess.ID
	}
	s.mu.Unlock()
	mode := "build"
	if s.agent != nil && s.agent.Mode() == agent.Plan {
		mode = "plan"
	}
	var msgs []wireMessage
	var live []llm.Message
	if s.agent != nil {
		live = s.agent.Messages()
	}
	if len(live) == 0 && s.sess != nil {
		live = s.sess.Messages()
	}
	for _, m := range live {
		wm := wireMessage{Role: string(m.Role), Content: m.Content, Name: m.Name}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{Name: tc.Name, Args: tc.Arguments})
		}
		msgs = append(msgs, wm)
	}
	model := ""
	if s.agent != nil {
		if m, ok := s.agent.(interface{ Model() string }); ok {
			model = m.Model()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"mode": mode, "busy": busy, "session_id": id,
		"route": route, "model": model, "todos": todos, "messages": msgs,
	})
}

// EncodeEvent maps an agent event to SSE JSON (exported for tests).
func EncodeEvent(e agent.Event) ([]byte, error) { return encodeEvent(e) }

func encodeEvent(e agent.Event) ([]byte, error) {
	w := wireEvent{}
	switch e.Type {
	case agent.EventThinking:
		w.Type = "thinking"
		w.Text = e.Text
	case agent.EventText:
		w.Type = "text"
		w.Text = e.Text
	case agent.EventToolStart:
		w.Type = "tool_start"
		w.Name = e.Name
		w.Args = e.Args
	case agent.EventToolDone:
		w.Type = "tool_done"
		w.Name = e.Name
		w.Output = e.Output
	case agent.EventRoute:
		w.Type = "route"
		w.Name = e.Name
		w.Model = e.Model
		w.Text = e.Text // router fallback error, if any
	case agent.EventTurnDone:
		w.Type = "turn_done"
		if e.Usage != nil {
			w.Prompt = e.Usage.PromptTokens
			w.Compl = e.Usage.CompletionTokens
		}
	case agent.EventError:
		w.Type = "error"
		if e.Err != nil {
			w.Error = e.Err.Error()
		}
	case agent.EventTodos:
		w.Type = "todos"
		for _, td := range e.Todos {
			w.Todos = append(w.Todos, wireTodo{Content: td.Content, Status: td.Status, Priority: td.Priority})
		}
	case agent.EventStatus:
		w.Type = "status"
		w.Name = e.Name
		w.Text = e.Text
	default:
		w.Type = "unknown"
	}
	return json.Marshal(w)
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	id := ""
	route := ""
	if s.sess != nil {
		id = s.sess.ID
	}
	s.mu.Lock()
	route = s.lastRoute
	s.mu.Unlock()
	page := strings.ReplaceAll(indexHTML, "{{SESSION}}", html.EscapeString(id))
	page = strings.ReplaceAll(page, "{{ROUTE}}", html.EscapeString(route))
	_, _ = w.Write([]byte(page))
}

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>quikagent</title>
<style>
  :root { color-scheme: dark; font-family: ui-sans-serif, system-ui, sans-serif; }
  body { margin: 0; background: #0f1115; color: #e6e8ee; display: flex; flex-direction: column; height: 100vh; height: 100dvh; }
  #wrap { display: flex; flex: 1; min-height: 0; }
  #side { display:none; width: 240px; border-left: 1px solid #2a2f3a; padding: 12px; overflow: auto; font-size: 12px; color: #8a93a6; }
  #newSessTop { display:inline-block; }
  @media (min-width: 900px) { #side { display:block; } #newSessTop { display:none; } }
  #side h2 { margin: 12px 0 6px; font-size: 11px; letter-spacing: .08em; color: #9ecbff; }
  #side button { min-height: 32px; padding: 6px 8px; margin: 2px 0; width: 100%; text-align: left; background: #1a2030; }
  header { padding: 12px 16px; border-bottom: 1px solid #2a2f3a; display: flex; flex-wrap: wrap; gap: 12px; align-items: center; }
  #mode { background: #1a2030; color: #9ecbff; border: 1px solid #2a2f3a; border-radius: 6px; padding: 6px 10px; }
  #log { flex: 1; overflow: auto; padding: 16px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; line-height: 1.45; }
  #log > div { white-space: pre-wrap; }
  .user { color: #9ecbff; } .tool { color: #c3a6ff; } .err { color: #ff8a8a; } .think { color: #8a93a6; } .note { color: #8a93a6; }
  .approve-card, .question-card { background:#1a2030; border:1px solid #3b82f6; border-radius:8px; padding:10px; margin:8px 0; }
  .question-card { border-color: #39c5cf; }
  form { display: flex; gap: 8px; padding: 12px 16px; border-top: 1px solid #2a2f3a; }
  input { flex: 1; background: #151922; border: 1px solid #2a2f3a; color: inherit; border-radius: 8px; padding: 10px 12px; }
  button { background: #3b82f6; color: white; border: 0; border-radius: 8px; padding: 10px 14px; cursor: pointer; min-height: 44px; }
  button.secondary { background: #2a2f3a; }
  button:disabled { opacity: 0.5; cursor: default; }
  #banner { display:none; background:#3b1d1d; color:#ff8a8a; padding:8px 16px; font-size:12px; }
  #banner.show { display:block; }
  #send.busy { background:#2a2f3a; }
</style>
</head>
<body>
<header>
  <strong>quikagent</strong>
  <span id="meta" style="color:#8a93a6;font-size:12px">session {{SESSION}} · route {{ROUTE}}</span>
  <button id="mode" type="button" aria-label="Toggle plan or build mode">mode: build</button>
  <button class="secondary" id="newSessTop" type="button">New session</button>
  <button class="secondary" id="cancel" type="button">cancel</button>
</header>
<div id="banner" role="status"></div>
<div id="wrap">
<div id="log" role="log" aria-live="polite"></div>
<aside id="side">
  <button id="newSess" type="button">New session</button>
  <h2>TODOS</h2>
  <div id="todos">none</div>
  <h2>SESSIONS</h2>
  <div id="sessions"></div>
</aside>
</div>
<form id="f">
  <label for="prompt" style="position:absolute;left:-9999px">Prompt</label>
  <input id="prompt" placeholder="Ask quikagent…" autocomplete="off"/>
  <button id="send" type="submit">Send</button>
</form>
<script>
const log = document.getElementById('log');
const sendBtn = document.getElementById('send');
const banner = document.getElementById('banner');
let mode = 'build';
let busy = false;
let curText = null, curThink = null, curStatus = null;
function setBusy(on) {
  busy = on;
  sendBtn.disabled = on;
  sendBtn.classList.toggle('busy', on);
  sendBtn.textContent = on ? 'Working…' : 'Send';
}
function showBanner(t) { banner.textContent = t || ''; banner.classList.toggle('show', !!t); }
function append(cls, text) {
  const d = document.createElement('div');
  if (cls) d.className = cls;
  d.textContent = text;
  log.appendChild(d);
  log.scrollTop = log.scrollHeight;
  return d;
}
function clearStatus() {
  if (curStatus) { curStatus.remove(); curStatus = null; }
}
function appendDelta(kind, text) {
  clearStatus();
  if (kind === 'text') {
    curThink = null;
    if (!curText) curText = append('', '');
    curText.textContent += text || '';
  } else {
    curText = null;
    if (!curThink) curThink = append('think', '');
    curThink.textContent += text || '';
  }
  log.scrollTop = log.scrollHeight;
}
function disableCard(d) {
  d.querySelectorAll('button,input').forEach(el => { el.disabled = true; });
}
function postJSON(url, body) {
  return fetch(url, {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(body)});
}
function argSummary(raw) {
  try {
    const o = JSON.parse(raw || '');
    for (const k of ['command','file_path','path','pattern','query','description']) {
      if (o[k]) return String(o[k]);
    }
    if (o.patch) {
      const m = String(o.patch).match(/\*\*\* (?:Update|Add|Delete) File: (.+)/);
      if (m) return m[1].trim();
    }
  } catch (e) {}
  return raw || '';
}
function renderTodos(list) {
  const el = document.getElementById('todos');
  if (!el) return;
  if (!list || !list.length) { el.textContent = 'none'; return; }
  el.textContent = list.map(t => {
    const mark = t.status === 'completed' ? '[x]' : t.status === 'in_progress' ? '[~]' : '[ ]';
    return mark + ' ' + (t.priority ? t.priority + ' · ' : '') + t.content;
  }).join('\n');
}
function loadSessions() {
  fetch('/sessions').then(r => r.json()).then(rows => {
    const el = document.getElementById('sessions');
    if (!el) return;
    el.innerHTML = '';
    (rows || []).slice(-10).reverse().forEach(s => {
      const b = document.createElement('button');
      b.textContent = (s.title || s.id) + ' (' + s.messages + ')';
      b.onclick = async () => {
        const res = await postJSON('/session/resume', {id:s.id});
        if (!res.ok) { append('err', '✗ resume failed'); return; }
        location.reload();
      };
      el.appendChild(b);
    });
  }).catch(() => {});
}
function questionCard(e) {
  const d = document.createElement('div');
  d.className = 'question-card';
  d.setAttribute('role', 'dialog');
  d.appendChild(document.createTextNode((e.header ? e.header + ': ' : '') + (e.question || '')));
  d.appendChild(document.createElement('br'));
  const send = async (answer) => {
    disableCard(d);
    const res = await postJSON('/answer', {id:e.id, answer:answer});
    if (!res.ok) {
      d.querySelectorAll('button,input').forEach(el => { el.disabled = false; });
      append('err', '✗ answer failed (' + res.status + ')');
    }
  };
  (e.options || []).forEach((opt) => {
    const b = document.createElement('button');
    b.textContent = opt;
    b.style.margin = '4px 4px 0 0';
    b.onclick = () => send(opt);
    d.appendChild(b);
  });
  const skip = document.createElement('button');
  skip.className = 'secondary';
  skip.textContent = 'Skip';
  skip.onclick = () => send('');
  d.appendChild(skip);
  const custom = document.createElement('input');
  custom.placeholder = 'custom answer';
  custom.style.marginTop = '8px';
  custom.onkeydown = (ev) => { if (ev.key === 'Enter') send(custom.value); };
  d.appendChild(document.createElement('br'));
  d.appendChild(custom);
  log.appendChild(d);
  log.scrollTop = log.scrollHeight;
}
function approveCard(e) {
  const d = document.createElement('div');
  d.className = 'approve-card';
  d.setAttribute('role', 'dialog');
  d.appendChild(document.createTextNode('Approve ' + (e.name || 'tool') + '? ' + (e.args || '')));
  d.appendChild(document.createElement('br'));
  const send = async (allow, always) => {
    disableCard(d);
    const res = await postJSON('/approve', {id:e.id, allow:allow, always:!!always});
    if (!res.ok) {
      d.querySelectorAll('button,input').forEach(el => { el.disabled = false; });
      append('err', '✗ approve failed (' + res.status + ')');
    }
  };
  const yes = document.createElement('button');
  yes.textContent = 'Allow';
  yes.onclick = () => send(true, false);
  const always = document.createElement('button');
  always.textContent = 'Always';
  always.onclick = () => send(true, true);
  const no = document.createElement('button');
  no.className = 'secondary';
  no.textContent = 'Deny';
  no.onclick = () => send(false, false);
  d.appendChild(yes); d.appendChild(always); d.appendChild(no);
  log.appendChild(d);
  log.scrollTop = log.scrollHeight;
}
function handleEvent(e) {
  switch (e.type) {
    case 'thinking': appendDelta('think', e.text); break;
    case 'text': appendDelta('text', e.text); break;
    case 'status':
      clearStatus();
      curStatus = append('tool', '… ' + (e.text || e.name || 'waiting'));
      break;
    case 'route':
      append('think', 'route ' + e.name + ' → ' + e.model + (e.text ? ' (' + e.text + ')' : ''));
      document.getElementById('meta').textContent = 'session {{SESSION}} · route ' + e.name;
      break;
    case 'tool_start':
      clearStatus();
      append('tool', '⏺ ' + e.name + ' ' + argSummary(e.args || ''));
      break;
    case 'tool_done': {
      const out = e.output || '';
      const cls = out.indexOf('Error:') === 0 ? 'err' : 'tool';
      append(cls, '  ' + out.slice(0, 400) + (out.length > 400 ? '…' : ''));
      break;
    }
    case 'turn_done':
      clearStatus();
      curText = curThink = null;
      setBusy(false);
      append('note', e.prompt_tokens || e.completion_tokens ? ('— ↑' + (e.prompt_tokens||0) + ' ↓' + (e.completion_tokens||0)) : '—');
      break;
    case 'error':
      clearStatus();
      setBusy(false);
      if ((e.error || '').indexOf('context canceled') >= 0) append('note', 'cancelled');
      else append('err', '✗ ' + e.error);
      break;
    case 'todos': renderTodos(e.todos || []); break;
    case 'approve': approveCard(e); break;
    case 'question': questionCard(e); break;
  }
}
const es = new EventSource('/events');
es.onmessage = (ev) => {
  try { handleEvent(JSON.parse(ev.data)); }
  catch (err) { append('err', '✗ bad event'); }
};
es.onerror = () => showBanner('connection lost — retrying…');
es.onopen = () => showBanner('');
document.getElementById('f').onsubmit = async (ev) => {
  ev.preventDefault();
  const prompt = document.getElementById('prompt').value.trim();
  if (!prompt || busy) return;
  setBusy(true);
  const res = await fetch('/turn', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({prompt}) });
  if (!res.ok) {
    setBusy(false);
    append('err', res.status === 409 ? 'turn already in progress' : '✗ turn rejected (' + res.status + ')');
    return;
  }
  append('user', '› ' + prompt);
  document.getElementById('prompt').value = '';
};
document.getElementById('mode').onclick = async () => {
  const prev = mode;
  const next = mode === 'build' ? 'plan' : 'build';
  document.getElementById('mode').textContent = 'mode: ' + next;
  const res = await fetch('/mode', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({mode: next}) });
  if (!res.ok) {
    mode = prev;
    document.getElementById('mode').textContent = 'mode: ' + prev;
    append('err', res.status === 409 ? 'mode locked during turn' : '✗ mode failed');
    return;
  }
  mode = next;
};
document.getElementById('cancel').onclick = () => fetch('/cancel', { method: 'POST' });
fetch('/api/state').then(r => r.json()).then(st => {
  if (st.mode) { mode = st.mode; document.getElementById('mode').textContent = 'mode: ' + mode; }
  setBusy(!!st.busy);
  const route = st.route || '{{ROUTE}}';
  document.getElementById('meta').textContent = 'session ' + (st.session_id || '{{SESSION}}') + (route ? ' · route ' + route : '');
  (st.messages || []).forEach(m => {
    if (m.role === 'user') append('user', '› ' + (m.content || ''));
    else if (m.role === 'tool') append('tool', '  ' + (m.name || 'tool') + ' ' + (m.content || ''));
    else if (m.role === 'assistant') {
      if (m.content) append('', m.content);
      (m.tool_calls || []).forEach(tc => append('tool', '⏺ ' + (tc.name || 'tool')));
    } else if (m.content) append('', m.content);
  });
  renderTodos(st.todos || []);
}).catch(() => {});
async function newSession() {
  const res = await postJSON('/session/new', {});
  if (!res.ok) { append('err', '✗ new session failed'); return; }
  location.reload();
}
document.getElementById('newSess').onclick = newSession;
document.getElementById('newSessTop').onclick = newSession;
loadSessions();
</script>
</body>
</html>
`
