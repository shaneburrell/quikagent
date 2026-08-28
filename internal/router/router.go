// Package router implements optional Arch-Router per-turn model selection.
package router

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"quikagent/internal/config"
	"quikagent/internal/llm"
)

// Arch-Router classification prompt (OpenCode-compatible route XML).
const taskInstruction = `You are a helpful assistant designed to find the best suited route.
You are provided with route description within <routes></routes> XML tags:
<routes>

{routes}

</routes>

<conversation>

{conversation}

</conversation>
`

const formatPrompt = `
Your task is to decide which route is best suit with user intent on the conversation in <conversation></conversation> XML tags.  Follow the instruction:
1. If the latest intent from user is irrelevant or user intent is full filled, response with other route {"route": "other"}.
2. You must analyze the route descriptions and find the best match route for user latest intent. 
3. You only response the name of the route that best matches the user's request, use the exact name in the <routes></routes>.

Based on your analysis, provide your response in the following JSON formats if you decide to match any route:
{"route": "route_name"} 
`

var routeNameRe = regexp.MustCompile(`(?i)route["']?\s*:\s*["']?(\w+)`)

// Completer is the non-streaming LLM surface the router needs.
type Completer interface {
	ChatOnce(ctx context.Context, model string, messages []llm.Message, maxTokens int) (string, error)
}

// Router selects a chat model for a user turn via Arch-Router.
type Router struct {
	completer Completer
	cfg       config.RouterConfig
}

// New builds a Router. cfg.Routes should already include defaults.
func New(c Completer, cfg config.RouterConfig) *Router {
	if cfg.Model == "" {
		cfg.Model = config.DefaultRouterModel
	}
	if len(cfg.Routes) == 0 {
		cfg.Routes = config.DefaultRoutes()
	}
	return &Router{completer: c, cfg: cfg}
}

// Select asks Arch-Router which route fits userText and returns
// (routeName, chatModel). On failure it returns ("qwen", qwen model).
func (r *Router) Select(ctx context.Context, userText string) (route, model string, err error) {
	fallback := r.fallback()
	if strings.TrimSpace(userText) == "" {
		return fallback.route, fallback.model, nil
	}
	prompt, err := FormatPrompt(r.cfg.Routes, userText)
	if err != nil {
		return fallback.route, fallback.model, err
	}
	content, err := r.completer.ChatOnce(ctx, r.cfg.Model, []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}, 32)
	if err != nil {
		return fallback.route, fallback.model, err
	}
	route = ParseRoute(content, r.cfg.Routes)
	target, ok := r.cfg.Routes[route]
	if !ok || target.Model == "" {
		return fallback.route, fallback.model, nil
	}
	return route, target.Model, nil
}

type named struct{ route, model string }

func (r *Router) fallback() named {
	if t, ok := r.cfg.Routes["qwen"]; ok && t.Model != "" {
		return named{"qwen", t.Model}
	}
	if t, ok := r.cfg.Routes["other"]; ok && t.Model != "" {
		return named{"other", t.Model}
	}
	return named{"qwen", config.DefaultModel}
}

// FormatPrompt builds the Arch-Router user message.
func FormatPrompt(routes map[string]config.RouteTarget, userText string) (string, error) {
	type routeJSON struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	ordered := []routeJSON{}
	for _, name := range []string{"nano", "coder", "qwen", "other"} {
		if t, ok := routes[name]; ok {
			ordered = append(ordered, routeJSON{Name: name, Description: t.Description})
		}
	}
	for name, t := range routes {
		if name == "nano" || name == "coder" || name == "qwen" || name == "other" {
			continue
		}
		ordered = append(ordered, routeJSON{Name: name, Description: t.Description})
	}
	routesBytes, err := json.Marshal(ordered)
	if err != nil {
		return "", fmt.Errorf("encode routes: %w", err)
	}
	convBytes, err := json.Marshal([]map[string]string{{"role": "user", "content": userText}})
	if err != nil {
		return "", fmt.Errorf("encode conversation: %w", err)
	}
	s := strings.Replace(taskInstruction, "{routes}", string(routesBytes), 1)
	s = strings.Replace(s, "{conversation}", string(convBytes), 1)
	return s + formatPrompt, nil
}

// ParseRoute extracts a route name from Arch-Router output.
func ParseRoute(content string, routes map[string]config.RouteTarget) string {
	trimmed := strings.TrimSpace(content)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		raw := trimmed[start : end+1]
		raw = strings.ReplaceAll(raw, "'", `"`)
		var obj struct {
			Route string `json:"route"`
		}
		if json.Unmarshal([]byte(raw), &obj) == nil {
			name := strings.ToLower(strings.TrimSpace(obj.Route))
			if _, ok := routes[name]; ok {
				return name
			}
		}
	}
	if m := routeNameRe.FindStringSubmatch(trimmed); len(m) == 2 {
		name := strings.ToLower(m[1])
		if _, ok := routes[name]; ok {
			return name
		}
	}
	return "other"
}
