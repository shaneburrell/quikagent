package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestQuestionRequiresPrompt(t *testing.T) {
	q := NewQuestion(func(ctx context.Context, q Question) (string, error) {
		return "nope", nil
	})
	_, err := q.Run(context.Background(), json.RawMessage(`{"header":"h"}`))
	if err == nil || !strings.Contains(err.Error(), "question is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestQuestionNoFrontend(t *testing.T) {
	q := NewQuestion(nil)
	out, err := q.Run(context.Background(), json.RawMessage(`{"question":"ok?"}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "No interactive frontend") || !strings.Contains(out, "Do not call question again") {
		t.Fatalf("out = %q", out)
	}
}

func TestQuestionAsk(t *testing.T) {
	var got Question
	q := NewQuestion(func(ctx context.Context, qq Question) (string, error) {
		got = qq
		return "use postgres", nil
	})
	out, err := q.Run(context.Background(), json.RawMessage(`{"header":"DB","question":"which store?","options":["sqlite","postgres"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Header != "DB" || got.Prompt != "which store?" || len(got.Options) != 2 {
		t.Fatalf("got = %+v", got)
	}
	if !strings.Contains(out, "use postgres") {
		t.Fatalf("out = %q", out)
	}
}

func TestQuestionAskError(t *testing.T) {
	q := NewQuestion(func(ctx context.Context, qq Question) (string, error) {
		return "", fmt.Errorf("user cancelled")
	})
	_, err := q.Run(context.Background(), json.RawMessage(`{"question":"ok?"}`))
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v", err)
	}
}

func TestQuestionReadOnly(t *testing.T) {
	if !NewQuestion(nil).ReadOnly() {
		t.Fatal("question should be available in plan mode")
	}
}

func TestQuestionOptionsCap(t *testing.T) {
	var got Question
	q := NewQuestion(func(ctx context.Context, qq Question) (string, error) {
		got = qq
		return "a", nil
	})
	payload, err := json.Marshal(map[string]any{
		"question": "pick?",
		"options":  []string{"1", "2", "3", "4", "5", "6", "7", "8"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Run(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if len(got.Options) != 6 {
		t.Fatalf("options cap = %d want 6: %v", len(got.Options), got.Options)
	}
	if got.Options[0] != "1" || got.Options[5] != "6" {
		t.Fatalf("options = %v", got.Options)
	}
}
