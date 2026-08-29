package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// TraceVersion is the sidecar schema version.
const TraceVersion = 1

// TraceRecord is one append-only line in <id>.trace.jsonl.
// It is never sent back to the model.
type TraceRecord struct {
	V            int    `json:"v"`
	TS           string `json:"ts"`
	Type         string `json:"type"`
	Turn         string `json:"turn,omitempty"`
	Frontend     string `json:"frontend,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Model        string `json:"model,omitempty"`
	Route        string `json:"route,omitempty"`
	Step         int    `json:"step,omitempty"`
	MS           int64  `json:"ms,omitempty"`
	PromptTokens int    `json:"prompt_tokens,omitempty"`
	ComplTokens  int    `json:"completion_tokens,omitempty"`
	ToolCalls    int    `json:"tool_calls,omitempty"`
	Name         string `json:"name,omitempty"`
	ToolCallID   string `json:"tool_call_id,omitempty"`
	OK           *bool  `json:"ok,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
	Err          string `json:"err,omitempty"`
	Before       int    `json:"before,omitempty"`
	After        int    `json:"after,omitempty"`
	Steps        int    `json:"steps,omitempty"`
	StepID       string `json:"step_id,omitempty"`
}

// BoolPtr is a helper for the optional ok field.
func BoolPtr(v bool) *bool { return &v }

// TracePath returns the sidecar path for this session.
func (s *Session) TracePath() string {
	return strings.TrimSuffix(s.path, ".jsonl") + ".trace.jsonl"
}

// AppendTrace writes one record (mode 0600). Failures are returned;
// callers typically ignore them so a full disk does not abort a turn.
func (s *Session) AppendTrace(r TraceRecord) error {
	if s == nil || s.path == "" {
		return fmt.Errorf("session has no path")
	}
	if r.V == 0 {
		r.V = TraceVersion
	}
	if r.TS == "" {
		r.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal trace: %w", err)
	}
	f, err := os.OpenFile(s.TracePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	_, werr := f.Write(append(line, '\n'))
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// ReadTraces loads the sidecar, skipping corrupt lines.
func (s *Session) ReadTraces() (recs []TraceRecord, skipped int, err error) {
	f, err := os.Open(s.TracePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r TraceRecord
		if err := json.Unmarshal(line, &r); err != nil {
			skipped++
			continue
		}
		recs = append(recs, r)
	}
	return recs, skipped, scanner.Err()
}

// FormatTraces renders sidecar records as a compact markdown section.
func FormatTraces(recs []TraceRecord) string {
	if len(recs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Trace\n\n")
	for _, r := range recs {
		switch r.Type {
		case "turn_start":
			fmt.Fprintf(&b, "- turn %s start mode=%s model=%s", r.Turn, r.Mode, r.Model)
			if r.Frontend != "" {
				fmt.Fprintf(&b, " frontend=%s", r.Frontend)
			}
			b.WriteByte('\n')
		case "route":
			fmt.Fprintf(&b, "- route %s → %s", r.Route, r.Model)
			if r.Err != "" {
				fmt.Fprintf(&b, " err=%s", r.Err)
			}
			b.WriteByte('\n')
		case "compact":
			fmt.Fprintf(&b, "- compact %d → %d messages\n", r.Before, r.After)
		case "llm":
			fmt.Fprintf(&b, "- llm step=%d %dms prompt=%d completion=%d tools=%d\n",
				r.Step, r.MS, r.PromptTokens, r.ComplTokens, r.ToolCalls)
		case "tool":
			fmt.Fprintf(&b, "- tool %s %dms %s", r.Name, r.MS, r.Outcome)
			if r.Err != "" {
				fmt.Fprintf(&b, " err=%s", clipPreview(r.Err))
			}
			b.WriteByte('\n')
		case "turn_end":
			ok := "fail"
			if r.OK != nil && *r.OK {
				ok = "ok"
			}
			fmt.Fprintf(&b, "- turn %s end %dms steps=%d %s", r.Turn, r.MS, r.Steps, ok)
			if r.Err != "" {
				fmt.Fprintf(&b, " err=%s", r.Err)
			}
			b.WriteByte('\n')
		default:
			fmt.Fprintf(&b, "- %s\n", r.Type)
		}
	}
	b.WriteByte('\n')
	return b.String()
}
