// Package tools implements the capabilities exposed to the model:
// bash, read, write, edit, glob, grep, list, fetch, git, apply_patch,
// question, skill, task, todo, websearch, lsp, and mcp.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"quikagent/internal/llm"
)

// MaxOutput caps what any single tool returns to the model.
const MaxOutput = 32 * 1024

// Tool is an executable capability exposed to the model.
type Tool interface {
	// Spec returns the tool's name, description, and argument schema.
	Spec() llm.Tool
	// Run executes the tool with raw JSON arguments and returns text
	// for the model. Errors are reserved for hard failures such as
	// bad arguments or sandbox violations.
	Run(ctx context.Context, args json.RawMessage) (string, error)
	// ReadOnly reports whether the tool is safe in plan mode.
	ReadOnly() bool
}

// Registry is a named, ordered collection of tools.
type Registry struct {
	tools   map[string]Tool
	order   []string
	closers []func()
}

// New builds a Registry with the full default toolset, sandboxed to
// workdir.
func New(workdir string) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range []Tool{
		newBash(workdir),
		newRead(workdir),
		newWrite(workdir),
		newEdit(workdir),
		newGlob(workdir),
		newGrep(workdir),
		newList(workdir),
		newFetch(),
		newGit(workdir),
		NewTodo(),
		NewQuestion(nil),
		newApplyPatch(workdir),
	} {
		r.Add(t)
	}
	return r
}

// Clone returns a shallow copy of the registry (tools shared, closers not).
// Agent.New uses this so adding skill/task does not mutate the caller's set.
func (r *Registry) Clone() *Registry {
	if r == nil {
		return nil
	}
	r2 := &Registry{tools: map[string]Tool{}}
	for _, name := range r.order {
		r2.tools[name] = r.tools[name]
		r2.order = append(r2.order, name)
	}
	return r2
}

// Add registers a tool. Later adds with the same name replace the prior
// entry and keep its position in the order list.
func (r *Registry) Add(t Tool) {
	name := t.Spec().Name
	if _, ok := r.tools[name]; !ok {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
}

// ReadOnly returns a copy exposing only non-mutating tools (plan mode).
func (r *Registry) ReadOnly() *Registry {
	r2 := &Registry{tools: map[string]Tool{}}
	for _, name := range r.order {
		t := r.tools[name]
		if t.ReadOnly() {
			r2.tools[name] = t
			r2.order = append(r2.order, name)
		}
	}
	return r2
}

// AddCloser registers a shutdown hook (MCP child processes).
func (r *Registry) AddCloser(fn func()) {
	if fn != nil {
		r.closers = append(r.closers, fn)
	}
}

// Close stops attached MCP clients and other closers.
func (r *Registry) Close() {
	if r == nil {
		return
	}
	for i := len(r.closers) - 1; i >= 0; i-- {
		r.closers[i]()
	}
	r.closers = nil
}

// List returns tool specs in stable order.
func (r *Registry) List() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name].Spec())
	}
	return out
}

// Get looks up a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Without returns a copy of the registry without the named tools.
func (r *Registry) Without(names ...string) *Registry {
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}
	r2 := &Registry{tools: map[string]Tool{}}
	for _, name := range r.order {
		if drop[name] {
			continue
		}
		r2.tools[name] = r.tools[name]
		r2.order = append(r2.order, name)
	}
	return r2
}

// resolve pins a user/model-supplied path inside the sandbox. Relative
// paths are interpreted against workdir. Symlink targets must also stay
// under the workdir (evaluated via EvalSymlinks on the existing prefix).
func resolve(workdir, path string) (string, error) {
	if path == "" {
		return "", errInvalidArg("path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = realRoot
	}
	final, err := resolveExistingPrefix(abs)
	if err != nil {
		return "", err
	}
	if !underRoot(final, root) {
		return "", &sandboxError{path: path}
	}
	return final, nil
}

// resolveExistingPrefix EvalSymlinks the longest existing prefix of abs
// and re-joins any missing trailing components (for create-new-file writes).
func resolveExistingPrefix(abs string) (string, error) {
	abs = filepath.Clean(abs)
	existing := abs
	var missing []string
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			// Broken symlink or permission: still try EvalSymlinks.
			break
		}
		dir := filepath.Dir(existing)
		if dir == existing {
			break
		}
		missing = append([]string{filepath.Base(existing)}, missing...)
		existing = dir
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		// Unresolvable (e.g. dangling symlink): treat as escape.
		if _, lerr := os.Lstat(existing); lerr == nil {
			return "", &sandboxError{path: abs}
		}
		real = existing
	}
	if len(missing) == 0 {
		return real, nil
	}
	return filepath.Join(append([]string{real}, missing...)...), nil
}

func underRoot(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(path, root+sep)
}

// truncate limits output to MaxOutput bytes on UTF-8 rune boundaries.
func truncate(s string) string {
	if len(s) <= MaxOutput {
		return s
	}
	kept := MaxOutput / 2
	head := s[:kept]
	for !utf8.ValidString(head) && len(head) > 0 {
		head = head[:len(head)-1]
	}
	tail := s[len(s)-kept:]
	for !utf8.ValidString(tail) && len(tail) > 0 {
		tail = tail[1:]
	}
	return head + "\n... [truncated, showing first " +
		strconv.Itoa(len(head)) + " of " + strconv.Itoa(len(s)) + " bytes] ...\n" + tail
}

func relToWorkdir(workdir, abs string) (string, error) {
	root, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	path := abs
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		path = real
	}
	return filepath.Rel(root, path)
}

// sortPaths returns paths sorted lexicographically.
func sortPaths(paths []string) []string {
	sort.Strings(paths)
	return paths
}

// fileStat is a thin helper so tools share one existence check.
func fileStat(p string) (os.FileInfo, error) { return os.Stat(p) }

type errInvalidArg string

func (e errInvalidArg) Error() string { return "invalid arguments: " + string(e) }

type sandboxError struct{ path string }

func (e *sandboxError) Error() string {
	return "path escapes the workspace: " + e.path
}
