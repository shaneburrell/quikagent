package agent

import (
	"time"

	"quikagent/internal/session"
	"quikagent/internal/tools"
)

// TraceFunc receives one loop fact. Nil is a no-op (tests).
type TraceFunc func(session.TraceRecord)

// SetTrace installs the optional sidecar sink.
func (a *Agent) SetTrace(fn TraceFunc) {
	a.mu.Lock()
	a.trace = fn
	a.mu.Unlock()
}

// SetTraceFrontend labels later records (tui, web, print).
func (a *Agent) SetTraceFrontend(name string) {
	a.mu.Lock()
	a.traceFrontend = name
	a.mu.Unlock()
}

// BindSessionTrace writes records to sess.TracePath. Safe with a nil session.
func BindSessionTrace(a *Agent, sess *session.Session, frontend string) {
	if a == nil || sess == nil {
		return
	}
	a.SetTraceFrontend(frontend)
	a.SetTrace(func(r session.TraceRecord) { _ = sess.AppendTrace(r) })
}

// BindSessionPlan loads a plan sidecar and persists later updates.
func BindSessionPlan(a *Agent, sess *session.Session) {
	if a == nil || sess == nil {
		return
	}
	if p, err := sess.LoadPlan(); err == nil {
		a.LoadPlan(p)
	}
	a.SetOnPlan(func(p tools.Plan) { _ = sess.SavePlan(p) })
}

// SetTraceStepID labels later records (and events) with a plan step id.
func (a *Agent) SetTraceStepID(id string) {
	a.mu.Lock()
	a.traceStepID = id
	a.mu.Unlock()
}

func (a *Agent) stepID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.traceStepID
}

func (a *Agent) emitTrace(r session.TraceRecord) {
	a.mu.RLock()
	fn := a.trace
	frontend := a.traceFrontend
	turn := a.traceTurn
	stepID := a.traceStepID
	a.mu.RUnlock()
	if fn == nil {
		return
	}
	if r.V == 0 {
		r.V = session.TraceVersion
	}
	if r.TS == "" {
		r.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if r.Turn == "" {
		r.Turn = turn
	}
	if r.Frontend == "" {
		r.Frontend = frontend
	}
	if r.StepID == "" {
		r.StepID = stepID
	}
	fn(r)
}

func newTurnID() string {
	return time.Now().UTC().Format("20060102T150405.000000000")
}
