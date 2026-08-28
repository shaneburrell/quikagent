package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"quikagent/internal/llm"
)

type writeTool struct{ workdir string }

func newWrite(workdir string) *writeTool { return &writeTool{workdir: workdir} }

func (w *writeTool) ReadOnly() bool { return false }

func (w *writeTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "write",
		Description: "Create or overwrite a file with the given content. Parent directories are created as needed.",
		Parameters:  []byte(`{"type":"object","properties":{"path":{"type":"string","description":"File path to write"},"content":{"type":"string","description":"Full file content"}},"required":["path","content"]}`),
	}
}

func (w *writeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	if a.Path == "" {
		return "", errInvalidArg("path is required")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	abs, err := resolve(w.workdir, a.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err := writePreservingMode(abs, []byte(a.Content)); err != nil {
		return "", err
	}
	rel, err := relToWorkdir(w.workdir, abs)
	if err != nil {
		rel = a.Path
	}
	return "Wrote " + rel, nil
}

// writePreservingMode writes data, keeping an existing file's mode and
// defaulting to 0o600 only when creating a new file.
func writePreservingMode(path string, data []byte) error {
	perm := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	return os.WriteFile(path, data, perm)
}
