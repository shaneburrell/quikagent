// Package session persists conversations as JSONL files under
// ~/.quikagent/sessions (one line per message).
package session

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"quikagent/internal/llm"
	"quikagent/internal/text"
)

var sessionIDRe = regexp.MustCompile(`^[0-9]+-[0-9a-f]+$`)

// Session is a persisted conversation.
type Session struct {
	ID             string
	Title          string
	path           string
	messages       []llm.Message
	SkippedCorrupt int // lines skipped while loading (malformed JSON)
}

// Info is a lightweight session listing entry.
type Info struct {
	ID       string
	Path     string
	Modified time.Time
	MsgCount int
	Preview  string
	Title    string
}

// Dir returns the session storage directory, creating it if needed.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".quikagent", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Create starts a new empty session.
func Create() (*Session, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	s := &Session{ID: newID()}
	s.path = filepath.Join(dir, s.ID+".jsonl")
	return s, nil
}

// Load reads a session by ID.
func Load(id string) (*Session, error) {
	if err := validateSessionID(id); err != nil {
		return nil, err
	}
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	s := &Session{ID: id}
	s.path = filepath.Join(dir, id+".jsonl")
	// Ensure resolved path stays under sessions dir.
	clean := filepath.Clean(s.path)
	if !strings.HasPrefix(clean, filepath.Clean(dir)+string(filepath.Separator)) {
		return nil, fmt.Errorf("invalid session id")
	}
	msgs, skipped, err := readAll(s.path)
	if err != nil {
		return nil, fmt.Errorf("load session %s: %w", id, err)
	}
	if skipped > 0 && len(msgs) == 0 {
		return nil, fmt.Errorf("load session %s: all %d lines were corrupt", id, skipped)
	}
	s.messages = msgs
	s.SkippedCorrupt = skipped
	s.Title = readTitle(s.titlePath())
	return s, nil
}

func validateSessionID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("invalid session id")
	}
	if !sessionIDRe.MatchString(id) {
		return fmt.Errorf("invalid session id")
	}
	return nil
}

// List returns all sessions ordered oldest → newest (IDs are timestamp-prefixed).
func List() ([]Info, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".trace.jsonl") {
			continue
		}
		if strings.HasSuffix(name, ".jsonl") {
			ids = append(ids, strings.TrimSuffix(name, ".jsonl"))
		}
	}
	sort.Strings(ids)
	out := make([]Info, 0, len(ids))
	for _, id := range ids {
		path := filepath.Join(dir, id+".jsonl")
		info := Info{ID: id, Path: path}
		if st, err := os.Stat(path); err == nil {
			info.Modified = st.ModTime()
		}
		info.Title = readTitle(strings.TrimSuffix(path, ".jsonl") + ".title")
		msgs, _, err := readAll(path)
		if err == nil {
			info.MsgCount = len(msgs)
			for _, m := range msgs {
				if m.Role == llm.RoleUser && m.Content != "" {
					info.Preview = clipPreview(m.Content)
					break
				}
			}
		}
		if info.Title != "" {
			info.Preview = info.Title
		}
		out = append(out, info)
	}
	return out, nil
}

// Latest loads the most recently created session.
func Latest() (*Session, error) {
	infos, err := List()
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		dir, _ := Dir()
		return nil, fmt.Errorf("no sessions found in %s", dir)
	}
	return Load(infos[len(infos)-1].ID)
}

// Append adds a message and persists immediately.
func (s *Session) Append(m llm.Message) error {
	prev := s.messages
	s.messages = append(s.messages, m)
	if err := s.save(); err != nil {
		s.messages = prev
		return err
	}
	return nil
}

// Replace overwrites the conversation history and persists it.
func (s *Session) Replace(msgs []llm.Message) error {
	prev := s.messages
	s.messages = append([]llm.Message(nil), msgs...)
	if err := s.save(); err != nil {
		s.messages = prev
		return err
	}
	return nil
}

// Messages returns the conversation history.
func (s *Session) Messages() []llm.Message {
	out := make([]llm.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// Path returns the JSONL file path for this session.
func (s *Session) Path() string { return s.path }

func (s *Session) titlePath() string {
	return strings.TrimSuffix(s.path, ".jsonl") + ".title"
}

// SetTitle persists a display title next to the JSONL file.
func (s *Session) SetTitle(title string) error {
	title = clipPreview(strings.TrimSpace(title))
	s.Title = title
	if title == "" {
		_ = os.Remove(s.titlePath())
		return nil
	}
	return os.WriteFile(s.titlePath(), []byte(title+"\n"), 0o600)
}

// EnsureTitle sets Title from the first user line if none is stored yet.
func (s *Session) EnsureTitle() {
	if s.Title != "" {
		return
	}
	for _, m := range s.messages {
		if m.Role == llm.RoleUser && strings.TrimSpace(m.Content) != "" && !strings.HasPrefix(m.Content, "[conversation compacted]") {
			_ = s.SetTitle(TitleFromPrompt(m.Content))
			return
		}
	}
}

// TitleFromPrompt derives a short session title from a user prompt.
func TitleFromPrompt(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return clipPreview(text)
}

func clipPreview(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	return text.ClipRunes(s, 80)
}

func readTitle(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// save rewrites the whole file atomically (temp + rename), mode 0600.
func (s *Session) save() error {
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("save session %s: %w", s.ID, err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	w := bufio.NewWriter(tmp)
	for _, m := range s.messages {
		line, err := json.Marshal(toWire(m))
		if err != nil {
			tmp.Close()
			return fmt.Errorf("marshal session message: %w", err)
		}
		if _, err := w.Write(line); err != nil {
			tmp.Close()
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("save session %s: %w", s.ID, err)
	}
	ok = true
	return nil
}

func readAll(path string) ([]llm.Message, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	var out []llm.Message
	skipped := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var w wireMessage
		if err := json.Unmarshal(line, &w); err != nil {
			skipped++
			continue
		}
		out = append(out, w.message())
	}
	return out, skipped, scanner.Err()
}

func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), fmt.Sprintf("%x", b[:]))
}

// wireMessage is the JSONL representation of llm.Message.
type wireMessage struct {
	Role       string   `json:"role"`
	Content    string   `json:"content,omitempty"`
	Reasoning  string   `json:"reasoning,omitempty"`
	ToolCalls  []wireTC `json:"tool_calls,omitempty"`
	ToolCallID string   `json:"tool_call_id,omitempty"`
	Name       string   `json:"name,omitempty"`
}

type wireTC struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func toWire(m llm.Message) wireMessage {
	w := wireMessage{
		Role:       string(m.Role),
		Content:    m.Content,
		Reasoning:  m.Reasoning,
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	for _, tc := range m.ToolCalls {
		w.ToolCalls = append(w.ToolCalls, wireTC{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
	}
	return w
}

func (w wireMessage) message() llm.Message {
	m := llm.Message{
		Role:       llm.Role(w.Role),
		Content:    w.Content,
		Reasoning:  w.Reasoning,
		ToolCallID: w.ToolCallID,
		Name:       w.Name,
	}
	for _, tc := range w.ToolCalls {
		m.ToolCalls = append(m.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
	}
	return m
}
