package tools

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"quikagent/internal/llm"
)

const globMaxResults = 1000

type globTool struct{ workdir string }

func newGlob(workdir string) *globTool { return &globTool{workdir: workdir} }

func (g *globTool) ReadOnly() bool { return true }

func (g *globTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "glob",
		Description: "Find files by glob pattern (e.g. \"**/*.go\" or \"src/**/*.ts\"). Returns matching paths sorted, relative to the workspace.",
		Parameters:  []byte(`{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern to match"},"path":{"type":"string","description":"Directory to search in (default: workspace root)"}},"required":["pattern"]}`),
	}
}

func (g *globTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	if a.Pattern == "" {
		return "", errInvalidArg("pattern is required")
	}
	root := g.workdir
	if a.Path != "" {
		abs, err := resolve(g.workdir, a.Path)
		if err != nil {
			return "", err
		}
		root = abs
	}

	matches, err := filepath.Glob(filepath.Join(root, a.Pattern))
	if err != nil {
		return "", errInvalidArg("bad pattern: " + err.Error())
	}
	var walkErrs []string
	// filepath.Glob does not expand "**"; walk and match manually so
	// recursive patterns work like other agents' glob tools.
	if strings.Contains(a.Pattern, "**") {
		matches, walkErrs = g.walkMatch(ctx, root, a.Pattern)
	}

	ign := newIgnoreMatcher(g.workdir)
	rel := make([]string, 0, len(matches))
	for _, m := range matches {
		resolved, err := resolve(g.workdir, m)
		if err != nil {
			continue
		}
		r, err := relToWorkdir(g.workdir, resolved)
		if err != nil || strings.HasPrefix(r, "..") {
			continue
		}
		if ign.Ignored(r, false) {
			continue
		}
		rel = append(rel, r)
	}
	rel = sortPaths(rel)
	if len(rel) > globMaxResults {
		rel = append(rel[:globMaxResults], "... [more than "+strconv.Itoa(globMaxResults)+" matches, showing first]")
	}
	if len(walkErrs) > 0 {
		rel = append(rel, walkErrs...)
	}
	if len(rel) == 0 {
		return "No files found.", nil
	}
	return truncate(join(rel, "\n")), nil
}

// walkMatch expands "**" patterns via a directory walk: "**" matches
// any number of path segments.
func (g *globTool) walkMatch(ctx context.Context, root, pattern string) ([]string, []string) {
	parts := strings.Split(pattern, string(filepath.Separator))
	star := make([]bool, len(parts))
	for i, p := range parts {
		star[i] = p == "**"
	}

	ign := newIgnoreMatcher(g.workdir)
	var out, errs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return fs.SkipAll
		}
		rel, rerr := filepath.Rel(root, path)
		if err != nil {
			label := path
			if rerr == nil {
				label = rel
			}
			errs = append(errs, "error: "+label+": "+err.Error())
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if rerr != nil || rel == "." {
			return nil
		}
		if d.IsDir() && skippedDirName(d.Name()) {
			return fs.SkipDir
		}
		if isSkippedDir(rel) || ign.Ignored(rel, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if matchParts(parts, star, rel) {
			out = append(out, path)
		}
		return nil
	})
	return out, errs
}

func skippedDirName(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "venv", "__pycache__":
		return true
	default:
		return false
	}
}

// matchParts reports whether a relative path matches the pattern parts,
// where star[i] marks "**" segments that consume zero or more parts.
func matchParts(parts []string, star []bool, rel string) bool {
	relParts := strings.Split(rel, string(filepath.Separator))
	return matchPartsRec(parts, star, 0, relParts)
}

func matchPartsRec(parts []string, star []bool, i int, rel []string) bool {
	if i == len(parts) {
		return len(rel) == 0
	}
	if star[i] {
		// "**" matches zero or more remaining path segments.
		for skip := 0; skip <= len(rel); skip++ {
			if matchPartsRec(parts, star, i+1, rel[skip:]) {
				return true
			}
		}
		return false
	}
	if len(rel) == 0 {
		return false
	}
	ok, err := filepath.Match(parts[i], rel[0])
	if err != nil || !ok {
		return false
	}
	return matchPartsRec(parts, star, i+1, rel[1:])
}
