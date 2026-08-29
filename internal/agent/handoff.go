package agent

import "strings"

// isHandoffText reports a short approval or "implement the plan" request.
func isHandoffText(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "go", "yes", "y", "ok", "do it", "build", "implement",
		"go ahead", "build it", "lgtm", "do the plan":
		return true
	}
	return strings.Contains(s, "implement the plan")
}

func (a *Agent) isHandoff(userText string) bool {
	a.mu.RLock()
	pending := a.planPendingHandoff
	a.mu.RUnlock()
	return pending && isHandoffText(userText)
}

func (a *Agent) clearHandoff() {
	a.mu.Lock()
	a.planPendingHandoff = false
	a.mu.Unlock()
}

func (a *Agent) markHandoff() {
	a.mu.Lock()
	a.planPendingHandoff = true
	a.mu.Unlock()
}

func (a *Agent) coderModel() string {
	a.mu.RLock()
	r := a.router
	home := a.homeModel
	a.mu.RUnlock()
	if rm, ok := r.(interface{ RouteModel(string) string }); ok {
		if m := rm.RouteModel("coder"); m != "" {
			return m
		}
	}
	return home
}

func (a *Agent) plannerModel() string {
	a.mu.RLock()
	plan := a.planModel
	home := a.homeModel
	a.mu.RUnlock()
	if plan != "" {
		return plan
	}
	if rm, ok := a.router.(interface{ RouteModel(string) string }); ok {
		if m := rm.RouteModel("qwen"); m != "" {
			return m
		}
	}
	return home
}
