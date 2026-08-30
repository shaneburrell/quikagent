package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"quikagent/internal/llm"
	"quikagent/internal/session"
	"quikagent/internal/tools"
)

func TestOrchestrateRoutesStepText(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "scaffold done"},
		{text: "PASS"},
		{text: "model done"},
		{text: "PASS"},
		{text: "workers finished the scaffold and model."},
	}}
	a := newTestAgent(t.TempDir(), fake)
	r := &fakeRouter{
		coder: "coder-model",
		queue: []struct{ route, model string }{
			{route: "nano", model: "nano-q4"},
			{route: "coder", model: "coder-model"},
		},
	}
	a.SetRouter(r)
	a.SetRouterEnabled(true)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "scaffold", Title: "go.mod", Detail: "init module", Status: "pending", Files: []string{"go.mod"}},
		{ID: "domain", Title: "internal/model", Detail: "write pane types", Status: "pending", Files: []string{"internal/model"}, Deps: []string{"scaffold"}},
	}})
	events := collect(t, run(a, "go"))
	if r.calls < 2 {
		t.Fatalf("router calls = %d", r.calls)
	}
	joined := strings.Join(r.texts, "\n---\n")
	if !strings.Contains(joined, "go.mod") || !strings.Contains(joined, "pane types") {
		t.Fatalf("router saw %q", joined)
	}
	if strings.TrimSpace(r.texts[0]) == "go" {
		t.Fatal("first route should be step text, not go")
	}
	var routes []string
	for _, e := range events {
		if e.Type == EventRoute && e.StepID != "" && !strings.Contains(e.StepID, "/review") {
			routes = append(routes, e.Name+":"+e.Model)
		}
	}
	if len(routes) < 2 || routes[0] != "nano:nano-q4" || routes[1] != "coder:coder-model" {
		t.Fatalf("routes = %v events=%+v", routes, events)
	}
	p := a.Plan()
	if len(p.Steps) != 2 || p.Steps[0].Status != "done" || p.Steps[1].Status != "done" {
		t.Fatalf("plan = %+v", p)
	}
	if a.Model() != "fake" {
		t.Fatalf("parent model = %s", a.Model())
	}
}

func TestOrchestrateParallelDisjointFiles(t *testing.T) {
	fake := &fakeLLM{
		delay: 50 * time.Millisecond,
		scripts: []script{
			{text: "a done"},
			{text: "b done"},
			{text: "PASS"},
			{text: "both done"},
		},
	}
	a := newTestAgent(t.TempDir(), fake)
	r := &fakeRouter{route: "coder", model: "coder-model", coder: "coder-model"}
	a.SetRouter(r)
	a.SetRouterEnabled(true)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "a", Title: "pkg a", Status: "pending", Files: []string{"internal/a"}},
		{ID: "b", Title: "pkg b", Status: "pending", Files: []string{"internal/b"}},
	}})
	start := time.Now()
	collect(t, run(a, "go"))
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("elapsed %s; expected parallel workers", elapsed)
	}
	p := a.Plan()
	if p.Steps[0].Status != "done" || p.Steps[1].Status != "done" {
		t.Fatalf("plan = %+v", p)
	}
}

func TestOrchestrateOverlappingFilesSerial(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "a done"},
		{text: "PASS"},
		{text: "b done"},
		{text: "PASS"},
		{text: "serial done"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	r := &fakeRouter{route: "coder", model: "coder-model", coder: "coder-model"}
	a.SetRouter(r)
	a.SetRouterEnabled(true)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "a", Title: "same file", Status: "pending", Files: []string{"main.go"}},
		{ID: "b", Title: "also main", Status: "pending", Files: []string{"main.go"}},
	}})
	collect(t, run(a, "go"))
	p := a.Plan()
	if p.Steps[0].Status != "done" || p.Steps[1].Status != "done" {
		t.Fatalf("plan = %+v", p)
	}
}

func TestOrchestrateHonorsDeps(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "base"},
		{text: "PASS"},
		{text: "child"},
		{text: "PASS"},
		{text: "ok"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	r := &fakeRouter{route: "coder", model: "coder-model", coder: "coder-model"}
	a.SetRouter(r)
	a.SetRouterEnabled(true)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "base", Title: "base", Status: "pending", Files: []string{"a.go"}},
		{ID: "child", Title: "child", Status: "pending", Files: []string{"b.go"}, Deps: []string{"base"}},
	}})
	collect(t, run(a, "go"))
	p := a.Plan()
	if p.Steps[0].Status != "done" || p.Steps[1].Status != "done" {
		t.Fatalf("plan = %+v", p)
	}
}

func TestFilesOverlap(t *testing.T) {
	if !filesOverlap(nil, []string{"a.go"}) {
		t.Fatal("empty files should conflict")
	}
	if filesOverlap([]string{"internal/a"}, []string{"internal/b"}) {
		t.Fatal("disjoint should not overlap")
	}
	if !filesOverlap([]string{"internal/model"}, []string{"internal/model/fs.go"}) {
		t.Fatal("prefix should overlap")
	}
}

func TestPickNonOverlappingCaps(t *testing.T) {
	got := pickNonOverlapping([]tools.PlanStep{
		{ID: "a", Files: []string{"a"}},
		{ID: "b", Files: []string{"b"}},
		{ID: "c", Files: []string{"c"}},
		{ID: "d", Files: []string{"d"}},
	}, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestPickNonOverlappingEmptyFilesAlone(t *testing.T) {
	got := pickNonOverlapping([]tools.PlanStep{
		{ID: "empty"},
		{ID: "a", Files: []string{"a.go"}},
	}, 2)
	if len(got) != 1 || got[0].ID != "empty" {
		t.Fatalf("empty-files step should take its own wave: %+v", got)
	}
	got = pickNonOverlapping([]tools.PlanStep{
		{ID: "a", Files: []string{"a.go"}},
		{ID: "empty"},
	}, 3)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("empty after named should wait: %+v", got)
	}
}

func TestPickNonOverlappingSkipsConfirm(t *testing.T) {
	got := pickNonOverlapping([]tools.PlanStep{
		{ID: "init", Confirm: true},
		{ID: "a", Files: []string{"a.go"}},
		{ID: "b", Files: []string{"b.go"}},
	}, 3)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("confirm should not block others: %+v", got)
	}
}

func TestPlanModeGoDispatches(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "wrote go.mod"},
		{text: "PASS"},
		{text: "scaffold is in."},
	}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetMode(Plan)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "scaffold", Title: "go.mod", Status: "pending", Files: []string{"go.mod"}},
	}})
	events := collect(t, run(a, "go"))
	if a.Mode() != Build {
		t.Fatalf("mode = %s, want build", a.Mode())
	}
	p := a.Plan()
	if len(p.Steps) != 1 || p.Steps[0].Status != "done" {
		t.Fatalf("plan = %+v", p)
	}
	var dispatched bool
	for _, e := range events {
		if e.Type == EventRoute && strings.Contains(e.Text, "dispatch scaffold") {
			dispatched = true
		}
	}
	if !dispatched {
		t.Fatalf("expected dispatch route: %+v", events)
	}
}

func TestDigitDoesNotDispatch(t *testing.T) {
	fake := &fakeLLM{scripts: []script{{text: "option 5 noted"}}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetMode(Plan)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "scaffold", Title: "go.mod", Status: "pending", Files: []string{"go.mod"}},
	}})
	events := collect(t, run(a, "5"))
	if a.Mode() != Plan {
		t.Fatalf("mode = %s, want plan", a.Mode())
	}
	if a.Plan().Steps[0].Status != "pending" {
		t.Fatalf("step should stay pending: %+v", a.Plan())
	}
	for _, e := range events {
		if e.Type == EventRoute && strings.Contains(e.Text, "dispatch") {
			t.Fatalf("digit should not dispatch: %+v", events)
		}
	}
}

func TestConfirmStepStaysPending(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "tidy done"},
		{text: "PASS"},
		{text: "tidy ok; init still needs you"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "tidy", Title: "tidy go.mod", Status: "pending", Files: []string{"go.mod"}},
		{ID: "init", Title: "git init", Status: "pending", Confirm: true},
	}})
	collect(t, run(a, "go"))
	p := a.Plan()
	if p.Steps[0].Status != "done" {
		t.Fatalf("dispatchable = %+v", p.Steps[0])
	}
	if p.Steps[1].Status != "pending" {
		t.Fatalf("confirm should stay pending: %+v", p.Steps[1])
	}
}

func TestReviewerUsesSmallModel(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "worker done"},
		{text: "PASS"},
		{text: "ok"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	a.opts.SmallModel = "nano-review"
	a.SetPlanModel("qwen-plan")
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "a", Title: "file", Status: "pending", Files: []string{"a.go"}},
	}})
	collect(t, run(a, "go"))
	if len(fake.models) < 2 {
		t.Fatalf("models = %v", fake.models)
	}
	if fake.models[1] != "nano-review" {
		t.Fatalf("reviewer model = %v, want nano-review", fake.models)
	}
	for _, m := range fake.models[:2] {
		if m == "qwen-plan" {
			t.Fatalf("reviewer used plan model: %v", fake.models)
		}
	}
}

func TestReviewFailedFirstLine(t *testing.T) {
	if reviewFailed("PASS\nlooks good") {
		t.Fatal("first-line PASS should succeed")
	}
	if reviewFailed("**PASS**\nlooks good") {
		t.Fatal("markdown PASS should succeed")
	}
	if reviewFailed("Inspected files.\nPASS\nall good") {
		t.Fatal("PASS on an early line should succeed")
	}
	if reviewFailed("--- PASS: TestAdd\nlooks good") {
		t.Fatal("go test PASS line is inconclusive, should accept workers")
	}
	if reviewFailed("The worker said PASS somewhere") {
		t.Fatal("prose without a verdict line should accept workers")
	}
	if !reviewFailed("FAIL\nbroken") {
		t.Fatal("FAIL should fail")
	}
	if reviewFailed("") {
		t.Fatal("empty review is inconclusive, should accept workers")
	}
}

func TestOrchestrateContinuesAfterReviewFail(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "a done"},
		{text: "FAIL\nbad"},
		{text: "b done"},
		{text: "PASS"},
		{text: "a failed review; b shipped"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "a", Title: "first", Status: "pending", Files: []string{"a.go"}},
		{ID: "b", Title: "second", Status: "pending", Files: []string{"a.go"}},
	}})
	collect(t, run(a, "go"))
	p := a.Plan()
	if p.Steps[0].Status != "failed" {
		t.Fatalf("review-fail step = %+v", p.Steps[0])
	}
	if p.Steps[1].Status != "done" {
		t.Fatalf("later pending step should still run: %+v", p)
	}
}

func TestLoadPlanSetsTodos(t *testing.T) {
	a := newTestAgent(t.TempDir(), &fakeLLM{})
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "a", Title: "scaffold", Status: "pending", Files: []string{"main.go"}},
	}})
	todos := a.Todos()
	if len(todos) != 1 || todos[0].Content != "scaffold" {
		t.Fatalf("todos = %+v", todos)
	}
}

func TestOrchestrateWithoutRouter(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "done"},
		{text: "PASS"},
		{text: "ok"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "a", Title: "file", Status: "pending", Files: []string{"a.go"}},
	}})
	collect(t, run(a, "go"))
	if a.Plan().Steps[0].Status != "done" {
		t.Fatalf("plan = %+v", a.Plan())
	}
}

func TestScopedFileAllow(t *testing.T) {
	if !scopedFileAllow("write", `{"path":"main.go"}`, []string{"main.go"}) {
		t.Fatal("named write should allow")
	}
	if scopedFileAllow("write", `{"path":"secret.go"}`, []string{"main.go"}) {
		t.Fatal("other path should not allow")
	}
	if scopedFileAllow("bash", `{"command":"git init"}`, []string{"main.go"}) {
		t.Fatal("bash should not auto-allow")
	}
	if !scopedFileAllow("write", `{"path":"go.mod"}`, []string{"internal/a/a.go"}) {
		t.Fatal("go.mod should be in scope for a Go worker")
	}
}

func TestPlanToolEmitsDispatchHint(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{toolCalls: []llm.ToolCall{{ID: "c1", Name: "plan", Arguments: `{"title":"t","steps":[{"id":"a","title":"one","files":["a.go"]}]}`}}},
		{text: "here is the plan"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	a.SetMode(Plan)
	events := collect(t, run(a, "review this"))
	var hint string
	for _, e := range events {
		if e.Type == EventStatus && e.Name == "plan" {
			hint = e.Text
		}
	}
	if !strings.Contains(hint, "1 steps") || !strings.Contains(hint, "go") {
		t.Fatalf("hint = %q events=%+v", hint, events)
	}
}

func TestChildTraceHasStepID(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "done"},
		{text: "PASS"},
		{text: "ok"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	var mu sync.Mutex
	var recs []session.TraceRecord
	a.SetTrace(func(r session.TraceRecord) {
		mu.Lock()
		recs = append(recs, r)
		mu.Unlock()
	})
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "tidy", Title: "tidy", Status: "pending", Files: []string{"go.mod"}},
	}})
	collect(t, run(a, "go"))
	mu.Lock()
	defer mu.Unlock()
	var sawLLM, sawDispatch bool
	for _, r := range recs {
		if r.Type == "dispatch" && r.StepID == "tidy" {
			sawDispatch = true
		}
		if r.Type == "llm" && r.StepID == "tidy" {
			sawLLM = true
		}
	}
	if !sawDispatch || !sawLLM {
		t.Fatalf("missing step_id traces dispatch=%v llm=%v recs=%+v", sawDispatch, sawLLM, recs)
	}
}

func TestSummarizePersistsUserTurn(t *testing.T) {
	fake := &fakeLLM{scripts: []script{
		{text: "done"},
		{text: "PASS"},
		{text: "workers finished tidy"},
	}}
	a := newTestAgent(t.TempDir(), fake)
	a.LoadPlan(tools.Plan{Steps: []tools.PlanStep{
		{ID: "a", Title: "tidy", Status: "pending", Files: []string{"go.mod"}},
	}})
	collect(t, run(a, "go"))
	msgs := a.Messages()
	if len(msgs) < 3 {
		t.Fatalf("messages = %+v", msgs)
	}
	var sawUser, sawAsst bool
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "Summarize what the workers") {
			sawUser = true
		}
		if m.Role == "assistant" && strings.Contains(m.Content, "workers finished") {
			sawAsst = true
		}
	}
	if !sawUser || !sawAsst {
		t.Fatalf("expected persisted summarize turn: %+v", msgs)
	}
}
