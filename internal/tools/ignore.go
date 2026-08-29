package tools

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ignoreMatcher implements a gitignore subset:
//   - blank lines and # comments are skipped
//   - "name" matches that path segment anywhere
//   - "dir/" matches directories
//   - "*.ext" matches a basename glob
//   - "/prefix" is anchored at the workdir root
//   - trailing "/*" matches children of a directory
//   - "!pattern" (typically from .ignore) re-includes a previously ignored path
//
// Unsupported: **, character classes, nested .gitignore files, git's
// full globstar and negation-order edge cases.
type ignoreMatcher struct {
	rules []ignoreRule
}

type ignoreRule struct {
	negate  bool
	dirOnly bool
	anchor  bool
	raw     string
}

func newIgnoreMatcher(workdir string) *ignoreMatcher {
	m := &ignoreMatcher{}
	m.load(filepath.Join(workdir, ".gitignore"))
	m.load(filepath.Join(workdir, ".ignore"))
	return m
}

func (m *ignoreMatcher) load(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		neg := strings.HasPrefix(line, "!")
		if neg {
			line = strings.TrimSpace(line[1:])
			if line == "" {
				continue
			}
		}
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		anchor := strings.HasPrefix(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		m.rules = append(m.rules, ignoreRule{negate: neg, dirOnly: dirOnly, anchor: anchor, raw: line})
	}
}

// Ignored reports whether relPath (slash-separated, relative to workdir)
// should be skipped. Last matching rule wins so .ignore !patterns can
// re-include.
func (m *ignoreMatcher) Ignored(relPath string, isDir bool) bool {
	relPath = filepath.ToSlash(relPath)
	relPath = strings.TrimPrefix(relPath, "./")
	if relPath == "" || relPath == "." {
		return false
	}
	ignored := false
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if matchIgnore(r, relPath, isDir) {
			ignored = !r.negate
		}
	}
	return ignored
}

func matchIgnore(r ignoreRule, rel string, _ bool) bool {
	base := filepath.Base(rel)
	if r.anchor {
		if r.raw == rel || strings.HasPrefix(rel, r.raw+"/") {
			return true
		}
		ok, _ := filepath.Match(r.raw, rel)
		return ok
	}
	if strings.HasSuffix(r.raw, "/*") {
		dir := strings.TrimSuffix(r.raw, "/*")
		return strings.HasPrefix(rel, dir+"/")
	}
	if strings.Contains(r.raw, "*") {
		if ok, _ := filepath.Match(r.raw, base); ok {
			return true
		}
		if ok, _ := filepath.Match(r.raw, rel); ok {
			return true
		}
		return false
	}
	if base == r.raw {
		return true
	}
	for _, part := range strings.Split(rel, "/") {
		if part == r.raw {
			return true
		}
	}
	return strings.HasPrefix(rel, r.raw+"/")
}
