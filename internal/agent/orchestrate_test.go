package agent

import (
	"strings"
	"testing"
	"time"

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
		if e.Type == EventRoute {
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
