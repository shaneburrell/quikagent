package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanToolRecordsSteps(t *testing.T) {
	p := NewPlan()
	if !p.ReadOnly() {
		t.Fatal("plan must be read-only")
	}
	out, err := p.Run(context.Background(), json.RawMessage(`{"title":"mc","steps":[{"id":"s1","title":"scaffold","detail":"go mod","files":["go.mod"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 step") {
		t.Fatalf("out = %q", out)
	}
	got := p.Plan()
	if got.Title != "mc" || len(got.Steps) != 1 || got.Steps[0].Status != "pending" {
		t.Fatalf("%+v", got)
	}
	if !got.HasWork() {
		t.Fatal("pending should be work")
	}
	todos := got.Todos()
	if len(todos) != 1 || todos[0].Content != "scaffold" {
		t.Fatalf("%+v", todos)
	}
}

func TestPlanToolRecordsConfirm(t *testing.T) {
	p := NewPlan()
	_, err := p.Run(context.Background(), json.RawMessage(`{"steps":[{"id":"init","title":"git init","confirm":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := p.Plan()
	if len(got.Steps) != 1 || !got.Steps[0].Confirm {
		t.Fatalf("%+v", got)
	}
	if got.HasDispatchableWork() {
		t.Fatal("confirm-only plan is not dispatchable")
	}
	if !got.HasWork() {
		t.Fatal("confirm pending is still work")
	}
}

func TestPlanToolRequiresDepsOnTestSteps(t *testing.T) {
	p := NewPlan()
	_, err := p.Run(context.Background(), json.RawMessage(`{"steps":[{"id":"impl","title":"write lib","files":["lib.go"]},{"id":"t","title":"add tests","files":["lib_test.go"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := p.Plan()
	if len(got.Steps) != 2 || len(got.Steps[1].Deps) != 1 || got.Steps[1].Deps[0] != "impl" {
		t.Fatalf("expected inferred deps, got %+v", got.Steps[1])
	}
}

func TestPlanToolRejectsDuplicateIDs(t *testing.T) {
	p := NewPlan()
	_, err := p.Run(context.Background(), json.RawMessage(`{"steps":[{"id":"a","title":"one"},{"id":"a","title":"two"}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v", err)
	}
}
