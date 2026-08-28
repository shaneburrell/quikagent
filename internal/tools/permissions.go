package tools

import (
	"encoding/json"
	"regexp"
	"strings"
)

// MatchVerdict represents the outcome of permission matching.
type MatchVerdict int

const (
	MatchNone MatchVerdict = iota
	MatchAllow
	MatchDeny
)

// RuleMatcher provides pattern matching for permissions.
type RuleMatcher struct {
	allow map[string][]*regexp.Regexp
	deny  map[string][]*regexp.Regexp
}

// NewRuleMatcher creates a new rule matcher from permission rules.
func NewRuleMatcher(allow, deny []string) *RuleMatcher {
	m := &RuleMatcher{
		allow: make(map[string][]*regexp.Regexp),
		deny:  make(map[string][]*regexp.Regexp),
	}

	for _, rule := range deny {
		parts := strings.SplitN(rule, "(", 2)
		tool := parts[0]
		pattern := "*"
		if len(parts) > 1 {
			pattern = strings.TrimSuffix(parts[1], ")")
		}
		m.deny[tool] = append(m.deny[tool], compilePattern(pattern))
	}

	for _, rule := range allow {
		parts := strings.SplitN(rule, "(", 2)
		tool := parts[0]
		pattern := "*"
		if len(parts) > 1 {
			pattern = strings.TrimSuffix(parts[1], ")")
		}
		m.allow[tool] = append(m.allow[tool], compilePattern(pattern))
	}

	return m
}

// Match checks if a tool call matches the rules.
func (m *RuleMatcher) Match(toolName, args string) MatchVerdict {
	targets := permissionTargets(toolName, args)

	// Check deny rules first (deny wins)
	denyPatterns, hasDeny := m.deny[toolName]
	if hasDeny {
		for _, pattern := range denyPatterns {
			for _, target := range targets {
				if matchPattern(pattern, target) {
					return MatchDeny
				}
			}
		}
	}

	// Allow only when every target matches at least one allow pattern.
	// A multi-file apply_patch must not auto-allow just because one path matches.
	allowPatterns, hasAllow := m.allow[toolName]
	if hasAllow && len(targets) > 0 {
		allAllowed := true
		for _, target := range targets {
			matched := false
			for _, pattern := range allowPatterns {
				if matchPattern(pattern, target) {
					matched = true
					break
				}
			}
			if !matched {
				allAllowed = false
				break
			}
		}
		if allAllowed {
			return MatchAllow
		}
	}

	// No matching rule. Callers fall through to interactive approval
	// (or auto-allow for read-only tools).
	return MatchNone
}

// CheckPermission matches a tool call against allow/deny rules without the
// caller managing a RuleMatcher. Returns MatchNone when no rules are set.
func CheckPermission(allow, deny []string, name, args string) MatchVerdict {
	if len(allow) == 0 && len(deny) == 0 {
		return MatchNone
	}
	return NewRuleMatcher(allow, deny).Match(name, args)
}

// NeedsInteractiveApproval reports whether a tool call should prompt the
// user when no allow/deny rule matched. Mutating file tools, task
// subagent spawns, mutating bash, and MCP tools require a prompt.
func NeedsInteractiveApproval(name, args string) bool {
	switch name {
	case "write", "edit", "apply_patch", "task", "todo":
		return true
	case "bash":
		return BashLooksMutating(args)
	default:
		return strings.HasPrefix(name, "mcp_")
	}
}

// compilePattern compiles a pattern into a regexp.
func compilePattern(pattern string) *regexp.Regexp {
	if pattern == "*" {
		return regexp.MustCompile(".*")
	}

	// Escape literal parts and convert * to .*
	escaped := regexp.QuoteMeta(pattern)
	re := strings.ReplaceAll(escaped, `\*`, `.*`)
	return regexp.MustCompile("^" + re + "$")
}

// matchPattern matches a string against a compiled pattern.
func matchPattern(re *regexp.Regexp, s string) bool {
	return re.MatchString(s)
}

// permissionTargets extracts one or more match strings from tool arguments.
func permissionTargets(toolName, args string) []string {
	if toolName == "apply_patch" {
		if paths := applyPatchPaths(args); len(paths) > 0 {
			return paths
		}
	}
	return []string{toolTarget(toolName, args)}
}

// toolTarget extracts the primary target from tool arguments for matching.
func toolTarget(toolName, args string) string {
	switch toolName {
	case "bash":
		var payload struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(args), &payload); err == nil {
			return payload.Command
		}
	case "read", "write", "edit", "glob", "grep", "list", "apply_patch":
		var payload struct {
			FilePath string `json:"file_path"`
			Path     string `json:"path"`
			Pattern  string `json:"pattern"`
			Patch    string `json:"patch"`
		}
		if err := json.Unmarshal([]byte(args), &payload); err == nil {
			if payload.FilePath != "" {
				return payload.FilePath
			}
			if payload.Path != "" {
				return payload.Path
			}
			if payload.Pattern != "" {
				return payload.Pattern
			}
			if paths := applyPatchPaths(args); len(paths) > 0 {
				return paths[0]
			}
		}
	}

	// For unknown tools or failed parsing, return the full args
	return args
}

func applyPatchPaths(args string) []string {
	var payload struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil || payload.Patch == "" {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(payload.Patch, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Add File: ", "*** Delete File: ", "*** Update File: ", "*** Move to: "} {
			if strings.HasPrefix(line, prefix) {
				p := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if p != "" {
					paths = append(paths, p)
				}
			}
		}
	}
	return paths
}
