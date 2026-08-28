package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskRequiresPrompt(t *testing.T) {
	_, err := NewTask(nil).Run(context.Background(), json.RawMessage(`{"agent":"explore"}`))
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestTaskRunner(t *testing.T) {
	ttool := NewTask(func(ctx context.Context, agentID, prompt string) (string, error) {
		if agentID != "explore" || prompt != "find x" {
			t.Fatalf("%s %s", agentID, prompt)
		}
		return "found it", nil
	})
	out, err := ttool.Run(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"find x"}`))
	if err != nil || out != "found it" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
