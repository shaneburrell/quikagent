package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"quikagent/internal/llm"
	"quikagent/internal/text"
)

const (
	readMaxLines = 2000
	readMaxLine  = 2000
	readProbe    = 8192
)

type readTool struct{ workdir string }

func newRead(workdir string) *readTool { return &readTool{workdir: workdir} }

func (r *readTool) ReadOnly() bool { return true }

func (r *readTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "read",
		Description: "Read a file (text or directory listing) from the workspace. Output is line-numbered. Use offset/limit for large files. Binary files return metadata only.",
		Parameters:  []byte(`{"type":"object","properties":{"path":{"type":"string","description":"File or directory path"},"offset":{"type":"integer","description":"First line to read (1-indexed, default 1)"},"limit":{"type":"integer","description":"Maximum lines to read (default 2000)"}},"required":["path"]}`),
	}
}

func (r *readTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	abs, err := resolve(r.workdir, a.Path)
	if err != nil {
		return "", err
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", a.Path, err)
	}
	if info.IsDir() {
		return r.listDir(ctx, abs)
	}
	if meta, ok := binaryMeta(abs, info.Size()); ok {
		return meta, nil
	}
	if a.Offset < 1 {
		a.Offset = 1
	}
	if a.Limit < 1 || a.Limit > readMaxLines {
		a.Limit = readMaxLines
	}
	return readLines(ctx, abs, a.Offset, a.Limit)
}

func (r *readTool) listDir(ctx context.Context, abs string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	ign := newIgnoreMatcher(r.workdir)
	var b []string
	for _, e := range entries {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		rel, rerr := relToWorkdir(r.workdir, filepath.Join(abs, e.Name()))
		if rerr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		if skippedDirName(e.Name()) || isSkippedDir(rel) || ign.Ignored(rel, e.IsDir()) {
			continue
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b = append(b, name)
	}
	return truncate(join(b, "\n")), nil
}

// binaryMeta returns a metadata summary for binary/image files.
func binaryMeta(abs string, size int64) (string, bool) {
	f, err := os.Open(abs)
	if err != nil {
		return "", false
	}
	defer f.Close()

	buf := make([]byte, readProbe)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false
	}
	buf = buf[:n]
	if looksBinary(buf) {
		kind := "binary"
		if strings.HasPrefix(httpLikeContentType(buf), "image/") {
			kind = "image"
		}
		ext := filepath.Ext(abs)
		return fmt.Sprintf("(%s file, %d bytes, ext=%q; content not shown)", kind, size, ext), true
	}
	return "", false
}

func looksBinary(buf []byte) bool {
	if len(buf) == 0 {
		return false
	}
	if !utf8.Valid(buf) {
		return true
	}
	nul := 0
	for _, b := range buf {
		if b == 0 {
			nul++
		}
	}
	return nul > 0
}

func httpLikeContentType(buf []byte) string {
	switch {
	case len(buf) >= 8 && buf[0] == 0x89 && string(buf[1:4]) == "PNG":
		return "image/png"
	case len(buf) >= 3 && buf[0] == 0xff && buf[1] == 0xd8 && buf[2] == 0xff:
		return "image/jpeg"
	case len(buf) >= 6 && (string(buf[:6]) == "GIF87a" || string(buf[:6]) == "GIF89a"):
		return "image/gif"
	case len(buf) >= 12 && string(buf[:4]) == "RIFF" && string(buf[8:12]) == "WEBP":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// readLines returns file content with "N: " line-number prefixes.
func readLines(ctx context.Context, abs string, offset, limit int) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var out []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		lineNo++
		if lineNo < offset {
			continue
		}
		if len(out) >= limit {
			break
		}
		line := scanner.Text()
		if len([]rune(line)) > readMaxLine {
			line = text.ClipRunes(line, readMaxLine)
		}
		out = append(out, fmt.Sprintf("%d: %s", lineNo, line))
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if lineNo < offset {
		return "", fmt.Errorf("offset %d beyond end of file (%d lines)", offset, lineNo)
	}
	if len(out) == 0 {
		return "(empty file)", nil
	}
	return truncate(join(out, "\n")), nil
}

func join(lines []string, sep string) string {
	b := make([]byte, 0, len(lines)*16)
	for i, l := range lines {
		if i > 0 {
			b = append(b, sep...)
		}
		b = append(b, l...)
	}
	return string(b)
}
