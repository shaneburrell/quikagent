package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"quikagent/internal/llm"
	"quikagent/internal/text"
)

const (
	grepMaxMatches   = 200
	grepMaxLineBytes = 500
)

// skippedDirs are pruned from both glob and grep walks.
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, "__pycache__": true, ".venv": true,
	".idea": true, ".vscode": true, ".next": true, "coverage": true,
}

func (g *grepTool) ReadOnly() bool { return true }

func isSkippedDir(rel string) bool {
	first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	return skippedDirs[first]
}

type grepTool struct{ workdir string }

func newGrep(workdir string) *grepTool { return &grepTool{workdir: workdir} }

func (g *grepTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "grep",
		Description: "Search file contents with a regular expression. Returns \"path:line: content\" matches. Use include to filter files (e.g. \"*.go\").",
		Parameters:  []byte(`{"type":"object","properties":{"pattern":{"type":"string","description":"Regular expression to search for"},"path":{"type":"string","description":"Directory to search in (default: workspace root)"},"include":{"type":"string","description":"File glob to restrict matches (e.g. \"*.ts\")"}},"required":["pattern"]}`),
	}
}

func (g *grepTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	if a.Pattern == "" {
		return "", errInvalidArg("pattern is required")
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", errInvalidArg("bad pattern: " + err.Error())
	}

	root := g.workdir
	if a.Path != "" {
		abs, err := resolve(g.workdir, a.Path)
		if err != nil {
			return "", err
		}
		root = abs
	}

	ign := newIgnoreMatcher(g.workdir)
	var (
		matches []string
		errors  []string
		files   int
		stopped bool
	)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if stopped || ctx.Err() != nil {
			return fs.SkipAll
		}
		rel, rerr := filepath.Rel(g.workdir, path)
		if err != nil {
			if rerr == nil {
				errors = append(errors, rel+": "+err.Error())
			} else {
				errors = append(errors, path+": "+err.Error())
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && skippedDirs[d.Name()] {
				return fs.SkipDir
			}
			if path != root {
				if rerr == nil && ign.Ignored(rel, true) {
					return fs.SkipDir
				}
			}
			return nil
		}
		if rerr != nil {
			return nil
		}
		if ign.Ignored(rel, false) {
			return nil
		}
		if a.Include != "" {
			ok, matchErr := filepath.Match(a.Include, d.Name())
			if matchErr != nil || !ok {
				return nil
			}
		}
		fileMatches, n, scanErr := scanFile(path, re)
		if scanErr != nil {
			errors = append(errors, rel+": "+scanErr.Error())
		}
		if n == 0 {
			return nil
		}
		files++
		for _, m := range fileMatches {
			matches = append(matches, rel+":"+m)
			if len(matches) >= grepMaxMatches {
				stopped = true
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		return "", err
	}
	if len(matches) == 0 && len(errors) == 0 {
		return "No matches found.", nil
	}
	var b strings.Builder
	if len(matches) > 0 {
		fmt.Fprintf(&b, "%d matches in %d file(s):\n", len(matches), files)
		if len(matches) == grepMaxMatches {
			b.WriteString("... [capped at " + fmt.Sprint(grepMaxMatches) + " matches]\n")
		}
		b.WriteString(strings.Join(matches, "\n"))
	}
	if len(errors) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("Search errors:\n")
		b.WriteString(strings.Join(errors, "\n"))
	}
	return truncate(b.String()), nil
}

// scanFile returns per-line matches as "line: content" strings.
func scanFile(path string, re *regexp.Regexp) ([]string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	var out []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !re.MatchString(line) {
			continue
		}
		if len([]rune(line)) > grepMaxLineBytes {
			line = text.ClipRunes(line, grepMaxLineBytes)
		}
		out = append(out, fmt.Sprintf("%d: %s", lineNo, line))
	}
	return out, len(out), scanner.Err()
}
