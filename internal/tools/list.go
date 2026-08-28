package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"quikagent/internal/llm"
)

const listMaxEntries = 200

type listTool struct{ workdir string }

func newList(workdir string) *listTool { return &listTool{workdir: workdir} }

func (l *listTool) ReadOnly() bool { return true }

func (l *listTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "list",
		Description: "List files and directories in a folder (default: workspace root). Directories end with /. Honors .gitignore and .ignore.",
		Parameters:  []byte(`{"type":"object","properties":{"path":{"type":"string","description":"Directory to list (default: \".\")"}}}`),
	}
}

func (l *listTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", errInvalidArg(err.Error())
		}
	}
	if a.Path == "" {
		a.Path = "."
	}
	abs, err := resolve(l.workdir, a.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", errInvalidArg(err.Error())
	}
	if !info.IsDir() {
		return "", errInvalidArg("not a directory: " + a.Path)
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	ign := newIgnoreMatcher(l.workdir)
	type item struct {
		name  string
		isDir bool
	}
	var items []item
	for _, e := range entries {
		rel, rerr := relToWorkdir(l.workdir, filepath.Join(abs, e.Name()))
		if rerr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		isDir := e.IsDir()
		if skippedDirName(e.Name()) || isSkippedDir(rel) || ign.Ignored(rel, isDir) {
			continue
		}
		items = append(items, item{name: e.Name(), isDir: isDir})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].isDir != items[j].isDir {
			return items[i].isDir
		}
		return items[i].name < items[j].name
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", a.Path)
	n := len(items)
	trunc := false
	if n > listMaxEntries {
		items = items[:listMaxEntries]
		trunc = true
	}
	for _, it := range items {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if it.isDir {
			fmt.Fprintf(&b, "  %s/\n", it.name)
		} else {
			fmt.Fprintf(&b, "  %s\n", it.name)
		}
	}
	if trunc {
		fmt.Fprintf(&b, "... (truncated, showing first %d of %d)\n", listMaxEntries, n)
	}
	return truncate(strings.TrimRight(b.String(), "\n")), nil
}
