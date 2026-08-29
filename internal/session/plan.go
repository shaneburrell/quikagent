package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"quikagent/internal/tools"
)

// PlanPath returns the structured-plan sidecar path for this session.
func (s *Session) PlanPath() string {
	if s == nil || s.path == "" {
		return ""
	}
	return strings.TrimSuffix(s.path, ".jsonl") + ".plan.json"
}

// SavePlan writes the plan sidecar (mode 0600). A nil or empty plan removes the file.
func (s *Session) SavePlan(p tools.Plan) error {
	if s == nil || s.path == "" {
		return fmt.Errorf("session has no path")
	}
	path := s.PlanPath()
	if len(p.Steps) == 0 && strings.TrimSpace(p.Title) == "" {
		_ = os.Remove(path)
		return nil
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}
	data = append(data, '\n')
	return writeSidecar0600(path, data)
}

func writeSidecar0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".plan-*.tmp")
	if err != nil {
		return fmt.Errorf("save plan: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("save plan: %w", err)
	}
	ok = true
	return nil
}

// LoadPlan reads the sidecar. Missing file returns a zero plan and nil error.
func (s *Session) LoadPlan() (tools.Plan, error) {
	if s == nil || s.path == "" {
		return tools.Plan{}, nil
	}
	data, err := os.ReadFile(s.PlanPath())
	if err != nil {
		if os.IsNotExist(err) {
			return tools.Plan{}, nil
		}
		return tools.Plan{}, fmt.Errorf("load plan: %w", err)
	}
	var p tools.Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return tools.Plan{}, fmt.Errorf("load plan: %w", err)
	}
	return p, nil
}
