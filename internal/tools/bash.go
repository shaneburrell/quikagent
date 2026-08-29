package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"quikagent/internal/llm"
)

const (
	bashDefaultTimeout = 2 * time.Minute
	bashMaxTimeout     = 10 * time.Minute
)

type bashTool struct{ workdir string }

func newBash(workdir string) *bashTool { return &bashTool{workdir: workdir} }

func (b *bashTool) ReadOnly() bool { return false }

func (b *bashTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "bash",
		Description: "Run a shell command in the workspace directory. Returns combined stdout and stderr. Commands run unattended; be precise.",
		Parameters:  []byte(`{"type":"object","properties":{"command":{"type":"string","description":"The command to execute"},"timeout":{"type":"integer","description":"Optional timeout in seconds (default 120, max 600)"}},"required":["command"]}`),
	}
}

func (b *bashTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	if a.Command == "" {
		return "", errInvalidArg("command is required")
	}

	timeout := bashDefaultTimeout
	if a.Timeout > 0 {
		timeout = time.Duration(a.Timeout) * time.Second
		if timeout > bashMaxTimeout {
			timeout = bashMaxTimeout
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-c", a.Command)
	cmd.Dir = b.workdir
	cmd.Env = scrubBashEnv()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if ctx.Err() != nil {
		return "Command canceled.\n" + truncate(out.String()), nil
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Command timed out after %s.\n%s", timeout, truncate(out.String())), nil
	}

	result := out.String()
	if err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			return fmt.Sprintf("Exit code %d\n%s", exitErr.ExitCode(), truncate(result)), nil
		}
		return "", fmt.Errorf("run command: %w", err)
	}
	return truncate(result), nil
}

// BashLooksMutating heuristically detects bash commands that change the workspace.
func BashLooksMutating(argsJSON string) bool {
	var a struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(argsJSON), &a) != nil {
		return true
	}
	cmd := strings.ToLower(a.Command)
	for _, tok := range []string{
		"rm ", "rm\t", "rm\n", "rm-", "unlink ", "mv ", "cp ", "chmod ", "chown ",
		"mkdir ", "touch ", ">", ">>", "tee ", "dd ", "truncate ",
		"git commit", "git push", "git add", "git reset", "git checkout",
		"git merge", "git rebase", "npm install", "npm i ", "pip install",
		"go get ", "cargo install", "make install", "sudo ",
		"sed -i", "find -delete", "perl -pi", "perl -i",
	} {
		if strings.Contains(cmd, tok) {
			return true
		}
	}
	if strings.Contains(cmd, ">") {
		return true
	}
	if gitLooksMutating(cmd) {
		return true
	}
	if inPlaceEditorLooksMutating(cmd) {
		return true
	}
	return false
}

// gitLooksMutating detects git writes even when flags sit between the
// binary and the subcommand (e.g. `git -C dir commit`).
func gitLooksMutating(cmd string) bool {
	fields := strings.Fields(cmd)
	mutating := map[string]bool{
		"commit": true, "push": true, "add": true, "reset": true,
		"checkout": true, "merge": true, "rebase": true, "rm": true,
		"mv": true, "restore": true, "stash": true, "cherry-pick": true,
		"revert": true, "am": true, "apply": true,
	}
	for i := range fields {
		if fields[i] != "git" && !strings.HasSuffix(fields[i], "/git") {
			continue
		}
		j := i + 1
		for j < len(fields) {
			f := fields[j]
			switch {
			case f == "-c" || f == "-C":
				j += 2
			case f == "--git-dir" || f == "--work-tree":
				j += 2
			case strings.HasPrefix(f, "--git-dir=") || strings.HasPrefix(f, "--work-tree="):
				j++
			case strings.HasPrefix(f, "-"):
				j++
			default:
				return mutating[f]
			}
		}
	}
	return false
}

func inPlaceEditorLooksMutating(cmd string) bool {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		switch f {
		case "sed":
			for _, arg := range fields[i+1:] {
				if arg == "-i" || strings.HasPrefix(arg, "-i") {
					return true
				}
			}
		case "perl":
			for _, arg := range fields[i+1:] {
				if arg == "-pi" || strings.HasPrefix(arg, "-pi") || arg == "-i" || strings.HasPrefix(arg, "-i") {
					return true
				}
			}
		case "find":
			for _, arg := range fields[i+1:] {
				if arg == "-delete" || arg == "-exec" {
					return true
				}
			}
		}
	}
	return false
}

// scrubBashEnv keeps a minimal environment and strips secrets.
func scrubBashEnv() []string {
	allow := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TERM": true,
		"TMPDIR": true, "TMP": true, "TEMP": true, "PWD": true,
		"GOPATH": true, "GOROOT": true, "GOTOOLCHAIN": true,
	}
	var out []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		uk := strings.ToUpper(k)
		if strings.HasPrefix(uk, "QUIKAGENT_") {
			continue
		}
		if strings.Contains(uk, "API_KEY") || strings.Contains(uk, "SECRET") || strings.Contains(uk, "TOKEN") || strings.Contains(uk, "PASSWORD") {
			continue
		}
		if allow[k] || strings.HasPrefix(k, "LC_") {
			out = append(out, e)
		}
	}
	return out
}
