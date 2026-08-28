package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DeniedError is returned when a pre-tool hook ran and rejected the tool.
type DeniedError struct{ Msg string }

func (e *DeniedError) Error() string { return "hook denied: " + e.Msg }

// ExecError is returned when a hook could not be started or was interrupted.
type ExecError struct{ Msg string }

func (e *ExecError) Error() string { return "hook failed: " + e.Msg }

// IsDenied reports whether err is a hook deny (as opposed to an exec failure).
func IsDenied(err error) bool {
	var d *DeniedError
	return errors.As(err, &d)
}

// Payload is stdin JSON for pre/post tool hooks.
type Payload struct {
	Phase  string `json:"phase"`
	Tool   string `json:"tool"`
	Args   string `json:"args"`
	Output string `json:"output,omitempty"`
}

// Pre runs .quikagent/hooks/pre-tool if present. A non-zero exit denies the tool.
func Pre(ctx context.Context, workdir, tool, args string) error {
	return run(ctx, workdir, "pre-tool", Payload{Phase: "pre", Tool: tool, Args: args})
}

// Post runs .quikagent/hooks/post-tool if present. Failures are ignored.
func Post(ctx context.Context, workdir, tool, args, output string) {
	_ = run(ctx, workdir, "post-tool", Payload{Phase: "post", Tool: tool, Args: args, Output: output})
}

func run(ctx context.Context, workdir, name string, p Payload) error {
	if workdir == "" {
		return nil
	}
	path := filepath.Join(workdir, ".quikagent", "hooks", name)
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.Dir = workdir
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if name != "pre-tool" {
			return nil
		}
		msg := strings.TrimSpace(stderr.String())
		var ee *exec.ExitError
		if errors.As(err, &ee) && ctx.Err() == nil {
			if msg == "" {
				msg = err.Error()
			}
			return &DeniedError{Msg: msg}
		}
		if msg == "" {
			msg = err.Error()
		}
		return &ExecError{Msg: msg}
	}
	return nil
}
