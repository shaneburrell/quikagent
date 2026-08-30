package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"quikagent/internal/llm"
)

// Question is a structured prompt the model can send to the user.
type Question struct {
	Header  string   `json:"header"`
	Prompt  string   `json:"question"`
	Options []string `json:"options"`
}

// AskFunc blocks until the user answers q. Return the chosen option
// text or a custom answer. A non-nil error is shown to the model.
type AskFunc func(ctx context.Context, q Question) (string, error)

// QuestionTool lets the model ask the user a structured question.
type QuestionTool struct {
	ask AskFunc
}

// QuestionSkipStub is returned when there is no interactive frontend
// (print mode). The model should assume and continue.
const QuestionSkipStub = "No interactive frontend. State your assumption and continue. Do not call question again."

// NewQuestion builds a question tool. If ask is nil, Run returns
// QuestionSkipStub so print mode can continue without hanging.
func NewQuestion(ask AskFunc) *QuestionTool {
	return &QuestionTool{ask: ask}
}

// SetAsk installs or replaces the frontend callback.
func (t *QuestionTool) SetAsk(fn AskFunc) { t.ask = fn }

func (t *QuestionTool) ReadOnly() bool { return true }

func (t *QuestionTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "question",
		Description: "Ask the user a structured question with optional choices. Use when you need a decision before continuing. Provide a short header, the question, and 2–6 options; the user may also type a custom answer.",
		Parameters:  []byte(`{"type":"object","properties":{"header":{"type":"string","description":"Short heading shown above the question"},"question":{"type":"string","description":"The question to ask"},"options":{"type":"array","items":{"type":"string"},"description":"Suggested answers (2–6)"}},"required":["question"]}`),
	}
}

func (t *QuestionTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var q Question
	if err := json.Unmarshal(args, &q); err != nil {
		return "", errInvalidArg(err.Error())
	}
	q.Prompt = strings.TrimSpace(q.Prompt)
	if q.Prompt == "" {
		return "", errInvalidArg("question is required")
	}
	if len(q.Options) > 6 {
		q.Options = q.Options[:6]
	}
	if t.ask == nil {
		return QuestionSkipStub, nil
	}
	ans, err := t.ask(ctx, q)
	if err != nil {
		return "", err
	}
	ans = strings.TrimSpace(ans)
	if ans == "" {
		return "", fmt.Errorf("question: empty answer")
	}
	return "User answered: " + ans, nil
}
