package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"quikagent/internal/llm"
)

const gitTimeout = 30 * time.Second

type gitTool struct{ workdir string }

func newGit(workdir string) *gitTool { return &gitTool{workdir: workdir} }

func (g *gitTool) ReadOnly() bool { return true }

func (g *gitTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "git",
		Description: "Run a read-only git command. Allowed actions: status, diff, log. For commits/pushes use bash (only when the user asked).",
		Parameters:  []byte(`{"type":"object","properties":{"action":{"type":"string","description":"One of: status, diff, log","enum":["status","diff","log"]},"args":{"type":"array","items":{"type":"string"},"description":"Optional extra args (e.g. path for diff, -n 5 for log)"}},"required":["action"]}`),
	}
}

func (g *gitTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string   `json:"action"`
		Args   []string `json:"args"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	action := strings.ToLower(strings.TrimSpace(a.Action))
	var gitArgs []string
	switch action {
	case "status":
		gitArgs = append([]string{"status", "--short", "--branch"}, a.Args...)
	case "diff":
		gitArgs = append([]string{"diff"}, a.Args...)
	case "log":
		gitArgs = append([]string{"log", "--oneline", "-n", "20"}, a.Args...)
	default:
		return "", errInvalidArg("action must be one of: status, diff, log")
	}
	for _, arg := range a.Args {
		if looksMutatingGitArg(arg) {
			return "", errInvalidArg("mutating git args are not allowed; use bash if the user asked to commit/push")
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", gitArgs...)
	cmd.Dir = g.workdir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	result := out.String()
	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("git %s timed out.\n%s", action, truncate(result)), nil
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Sprintf("Exit code %d\n%s", exitErr.ExitCode(), truncate(result)), nil
		}
		return "", fmt.Errorf("git: %w", err)
	}
	if strings.TrimSpace(result) == "" {
		return "(no output)", nil
	}
	return truncate(result), nil
}

func looksMutatingGitArg(arg string) bool {
	lower := strings.ToLower(arg)
	for _, bad := range []string{
		"commit", "push", "pull", "rebase", "merge", "reset", "checkout", "switch",
		"add", "rm", "mv", "stash", "tag", "branch", "-i", "--interactive",
		"--no-index", "--output", "-o", "--ext-diff", "--exec-path", "-c",
		"--upload-pack", "--receive-pack",
	} {
		if lower == bad || strings.HasPrefix(lower, bad+"=") {
			return true
		}
	}
	// -O / --output=file style
	if strings.HasPrefix(lower, "-o") && lower != "-oneline" {
		return true
	}
	return false
}
