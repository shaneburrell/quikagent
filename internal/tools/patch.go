package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"quikagent/internal/llm"
)

type applyPatchTool struct{ workdir string }

func newApplyPatch(workdir string) *applyPatchTool { return &applyPatchTool{workdir: workdir} }

func (t *applyPatchTool) ReadOnly() bool { return false }

func (t *applyPatchTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "apply_patch",
		Description: "Apply a multi-file patch. Envelope: *** Begin Patch ... *** End Patch. Sections: *** Add File: path, *** Delete File: path, *** Update File: path (optional *** Move to: newpath) followed by @@ hunks with ' ','-','+' lines.",
		Parameters:  []byte(`{"type":"object","properties":{"patch":{"type":"string","description":"Full apply_patch text including Begin/End markers"}},"required":["patch"]}`),
	}
}

func (t *applyPatchTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	ops, err := parseApplyPatch(a.Patch)
	if err != nil {
		return "", err
	}
	type planned struct {
		op      patchOp
		abs     string
		moveAbs string
		content []byte
		delete  bool
	}
	var plan []planned
	for _, op := range ops {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		abs, err := resolve(t.workdir, op.path)
		if err != nil {
			return "", err
		}
		p := planned{op: op, abs: abs}
		switch op.kind {
		case patchAdd:
			if _, err := os.Stat(abs); err == nil {
				return "", errInvalidArg("add: file already exists: " + op.path)
			}
			p.content = []byte(op.addBody)
		case patchDelete:
			if _, err := os.Stat(abs); err != nil {
				return "", errInvalidArg("delete: missing file: " + op.path)
			}
			p.delete = true
		case patchUpdate:
			data, err := os.ReadFile(abs)
			if err != nil {
				return "", errInvalidArg("update: " + err.Error())
			}
			next, err := applyHunks(string(data), op.hunks)
			if err != nil {
				return "", err
			}
			p.content = []byte(next)
			if op.moveTo != "" {
				mv, err := resolve(t.workdir, op.moveTo)
				if err != nil {
					return "", err
				}
				if _, err := os.Stat(mv); err == nil {
					return "", errInvalidArg("move: target exists: " + op.moveTo)
				}
				p.moveAbs = mv
			}
		}
		plan = append(plan, p)
	}

	var snaps []fileSnap
	for _, p := range plan {
		snaps = append(snaps, snapshotPath(p.abs))
		if p.moveAbs != "" {
			snaps = append(snaps, snapshotPath(p.moveAbs))
		}
	}

	added, updated, deleted := 0, 0, 0
	var applyErr error
	for _, p := range plan {
		if ctx.Err() != nil {
			applyErr = ctx.Err()
			break
		}
		switch p.op.kind {
		case patchAdd:
			if err := os.MkdirAll(filepath.Dir(p.abs), 0o755); err != nil {
				applyErr = err
			} else if err := os.WriteFile(p.abs, p.content, 0o644); err != nil {
				applyErr = err
			} else {
				added++
			}
		case patchDelete:
			if err := os.Remove(p.abs); err != nil {
				applyErr = err
			} else {
				deleted++
			}
		case patchUpdate:
			if p.moveAbs != "" {
				applyErr = applyMove(p.abs, p.moveAbs, p.content)
			} else if err := os.WriteFile(p.abs, p.content, 0o644); err != nil {
				applyErr = err
			}
			if applyErr == nil {
				updated++
			}
		}
		if applyErr != nil {
			break
		}
	}
	if applyErr != nil {
		restoreSnaps(snaps)
		return "", applyErr
	}
	summary := fmt.Sprintf("Applied patch: %d added, %d updated, %d deleted", added, updated, deleted)
	return truncate(summary), nil
}

type fileSnap struct {
	path    string
	existed bool
	data    []byte
	mode    os.FileMode
}

func snapshotPath(path string) fileSnap {
	info, err := os.Stat(path)
	if err != nil {
		return fileSnap{path: path}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileSnap{path: path, existed: true, mode: info.Mode().Perm()}
	}
	return fileSnap{path: path, existed: true, data: data, mode: info.Mode().Perm()}
}

func applyMove(src, dest string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}

func restoreSnaps(snaps []fileSnap) {
	for i := len(snaps) - 1; i >= 0; i-- {
		s := snaps[i]
		if !s.existed {
			_ = os.Remove(s.path)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
		_ = os.WriteFile(s.path, s.data, s.mode)
	}
}

type patchKind int

const (
	patchAdd patchKind = iota
	patchDelete
	patchUpdate
)

type patchHunk struct {
	lines []string // include leading ' ', '-', '+'
}

type patchOp struct {
	kind    patchKind
	path    string
	moveTo  string
	addBody string
	hunks   []patchHunk
}

func parseApplyPatch(text string) ([]patchOp, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	// trim leading/trailing blanks
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, errInvalidArg("malformed patch: missing *** Begin Patch / *** End Patch")
	}
	body := lines[1 : len(lines)-1]
	var ops []patchOp
	var cur *patchOp
	var hunk *patchHunk
	flushHunk := func() {
		if hunk != nil && len(hunk.lines) > 0 && cur != nil {
			cur.hunks = append(cur.hunks, *hunk)
		}
		hunk = nil
	}
	flushOp := func() {
		flushHunk()
		if cur != nil {
			ops = append(ops, *cur)
			cur = nil
		}
	}
	for _, line := range body {
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			flushOp()
			p := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
			if err := checkPatchPath(p); err != nil {
				return nil, err
			}
			cur = &patchOp{kind: patchAdd, path: p}
		case strings.HasPrefix(line, "*** Delete File: "):
			flushOp()
			p := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			if err := checkPatchPath(p); err != nil {
				return nil, err
			}
			cur = &patchOp{kind: patchDelete, path: p}
		case strings.HasPrefix(line, "*** Update File: "):
			flushOp()
			p := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			if err := checkPatchPath(p); err != nil {
				return nil, err
			}
			cur = &patchOp{kind: patchUpdate, path: p}
		case strings.HasPrefix(line, "*** Move to: "):
			if cur == nil || cur.kind != patchUpdate {
				return nil, errInvalidArg("Move to without Update File")
			}
			p := strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: "))
			if err := checkPatchPath(p); err != nil {
				return nil, err
			}
			cur.moveTo = p
		case strings.HasPrefix(line, "@@"):
			if cur == nil || cur.kind != patchUpdate {
				return nil, errInvalidArg("hunk without Update File")
			}
			flushHunk()
			hunk = &patchHunk{}
		case cur != nil && cur.kind == patchAdd && strings.HasPrefix(line, "+"):
			if cur.addBody != "" {
				cur.addBody += "\n"
			}
			cur.addBody += strings.TrimPrefix(line, "+")
		case cur != nil && cur.kind == patchUpdate && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")):
			if hunk == nil {
				hunk = &patchHunk{}
			}
			hunk.lines = append(hunk.lines, line)
		case strings.TrimSpace(line) == "":
			if cur != nil && cur.kind == patchAdd {
				if cur.addBody != "" {
					cur.addBody += "\n"
				}
			}
		default:
			return nil, errInvalidArg("unrecognized patch line: " + line)
		}
	}
	flushOp()
	if len(ops) == 0 {
		return nil, errInvalidArg("empty patch")
	}
	return ops, nil
}

func checkPatchPath(p string) error {
	if p == "" || filepath.IsAbs(p) || strings.Contains(p, "..") {
		return errInvalidArg("path escapes the workspace: " + p)
	}
	return nil
}

var (
	errHunkNotFound  = errors.New("hunk not found")
	errHunkAmbiguous = errors.New("hunk matches multiple locations")
)

func applyHunks(src string, hunks []patchHunk) (string, error) {
	text := src
	for i, h := range hunks {
		next, err := applyOneHunk(text, h, false)
		if errors.Is(err, errHunkNotFound) {
			next, err = applyOneHunk(text, h, true)
		}
		if err != nil {
			if errors.Is(err, errHunkAmbiguous) {
				return "", errInvalidArg(fmt.Sprintf("hunk %d matches multiple locations; add more context", i+1))
			}
			return "", errInvalidArg(fmt.Sprintf("hunk %d not found", i+1))
		}
		text = next
	}
	return text, nil
}

func applyOneHunk(src string, h patchHunk, trimSpace bool) (string, error) {
	var oldLines, newLines []string
	for _, line := range h.lines {
		if line == "" {
			continue
		}
		op, rest := line[0], line[1:]
		switch op {
		case ' ':
			oldLines = append(oldLines, rest)
			newLines = append(newLines, rest)
		case '-':
			oldLines = append(oldLines, rest)
		case '+':
			newLines = append(newLines, rest)
		}
	}
	newBlock := strings.Join(newLines, "\n")
	if len(oldLines) == 0 {
		// Insert-only hunk: write empty files, append to non-empty ones.
		if strings.TrimSpace(src) == "" {
			return strings.Join(newLines, "\n") + trailingNL(src, newLines), nil
		}
		out := src
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += newBlock
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return out, nil
	}
	if trimSpace {
		idx, n := findRelaxed(src, oldLines)
		if n == 0 {
			return "", errHunkNotFound
		}
		if n > 1 {
			return "", errHunkAmbiguous
		}
		all := splitKeep(src)
		end := idx + len(oldLines)
		if end > len(all) {
			return "", errHunkNotFound
		}
		orig := strings.Join(all[idx:end], "\n")
		return strings.Replace(src, orig, newBlock, 1), nil
	}
	oldBlock := strings.Join(oldLines, "\n")
	n := strings.Count(src, oldBlock)
	if n == 0 {
		return "", errHunkNotFound
	}
	if n > 1 {
		return "", errHunkAmbiguous
	}
	return strings.Replace(src, oldBlock, newBlock, 1), nil
}

func trailingNL(src string, newLines []string) string {
	if strings.HasSuffix(src, "\n") || len(newLines) == 0 {
		return "\n"
	}
	return ""
}

func splitKeep(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

func findRelaxed(src string, oldLines []string) (first int, count int) {
	all := splitKeep(src)
	first = -1
	for i := 0; i+len(oldLines) <= len(all); i++ {
		ok := true
		for j, want := range oldLines {
			if strings.TrimRight(all[i+j], " \t") != strings.TrimRight(want, " \t") {
				ok = false
				break
			}
		}
		if ok {
			count++
			if first < 0 {
				first = i
			}
		}
	}
	return first, count
}
