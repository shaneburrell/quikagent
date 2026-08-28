package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"quikagent/internal/llm"
)

type editTool struct{ workdir string }

func newEdit(workdir string) *editTool { return &editTool{workdir: workdir} }

func (e *editTool) ReadOnly() bool { return false }

func (e *editTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "edit",
		Description: "Perform an exact string replacement in a file. Fails if old_string is missing or ambiguous (unless replace_all is true). Always read the file before editing it.",
		Parameters:  []byte(`{"type":"object","properties":{"path":{"type":"string","description":"File path to modify"},"old_string":{"type":"string","description":"Exact text to find"},"new_string":{"type":"string","description":"Replacement text (must differ from old_string)"},"replace_all":{"type":"boolean","description":"Replace every occurrence (default false)"}},"required":["path","old_string","new_string"]}`),
	}
}

func (e *editTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	if a.OldString == "" {
		return "", errInvalidArg("old_string is required")
	}
	if a.OldString == a.NewString {
		return "", errInvalidArg("new_string must differ from old_string")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	abs, err := resolve(e.workdir, a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}

	content := string(data)
	count := strings.Count(content, a.OldString)
	switch {
	case count == 0:
		return "", fmt.Errorf("old_string not found in %s", a.Path)
	case count > 1 && !a.ReplaceAll:
		return "", fmt.Errorf("old_string found %d times in %s; provide more context or set replace_all", count, a.Path)
	}

	var updated string
	if a.ReplaceAll {
		updated = strings.ReplaceAll(content, a.OldString, a.NewString)
	} else {
		updated = strings.Replace(content, a.OldString, a.NewString, 1)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err := writePreservingMode(abs, []byte(updated)); err != nil {
		return "", err
	}
	rel, err := relToWorkdir(e.workdir, abs)
	if err != nil {
		rel = a.Path
	}
	if a.ReplaceAll {
		return fmt.Sprintf("Replaced %d occurrences in %s", count, rel), nil
	}
	return "Edited " + rel, nil
}
