package tools

import (
	"testing"
)

func TestRuleMatcher(t *testing.T) {
	// Test basic matching
	matcher := NewRuleMatcher([]string{"bash(git *)"}, []string{})

	// Should allow git commands
	result := matcher.Match("bash", `{"command":"git status"}`)
	if result != MatchAllow {
		t.Errorf("Expected MatchAllow for 'git status', got %v", result)
	}

	// Should deny non-git bash commands
	result = matcher.Match("bash", `{"command":"ls -la"}`)
	if result != MatchNone {
		t.Errorf("Expected MatchNone for 'ls -la', got %v", result)
	}
}

func TestRuleMatcherDeny(t *testing.T) {
	// Test deny precedence
	matcher := NewRuleMatcher([]string{"bash(*)"}, []string{"bash(ls *)"})

	// Should deny ls commands
	result := matcher.Match("bash", `{"command":"ls -la"}`)
	if result != MatchDeny {
		t.Errorf("Expected MatchDeny for 'ls -la', got %v", result)
	}

	// Should allow git commands
	result = matcher.Match("bash", `{"command":"git status"}`)
	if result != MatchAllow {
		t.Errorf("Expected MatchAllow for 'git status', got %v", result)
	}
}

func TestRuleMatcherExactMatch(t *testing.T) {
	matcher := NewRuleMatcher([]string{"read(/etc/passwd)"}, []string{})

	result := matcher.Match("read", `{"file_path":"/etc/passwd"}`)
	if result != MatchAllow {
		t.Errorf("Expected MatchAllow for exact match, got %v", result)
	}
}

func TestCheckPermission(t *testing.T) {
	tests := []struct {
		name  string
		allow []string
		deny  []string
		tool  string
		args  string
		want  MatchVerdict
	}{
		{
			name: "empty rules",
			tool: "bash",
			args: `{"command":"git status"}`,
			want: MatchNone,
		},
		{
			name:  "deny wins over allow",
			allow: []string{"bash(*)"},
			deny:  []string{"bash(rm *)"},
			tool:  "bash",
			args:  `{"command":"rm -rf /tmp/x"}`,
			want:  MatchDeny,
		},
		{
			name:  "bash prefix pattern allows",
			allow: []string{"bash(git status*)"},
			tool:  "bash",
			args:  `{"command":"git status --short"}`,
			want:  MatchAllow,
		},
		{
			name:  "bash prefix pattern does not match other commands",
			allow: []string{"bash(git status*)"},
			tool:  "bash",
			args:  `{"command":"rm -rf /"}`,
			want:  MatchNone,
		},
		{
			name:  "unmatched tool",
			allow: []string{"bash(git *)"},
			deny:  []string{"bash(rm *)"},
			tool:  "write",
			args:  `{"file_path":"README.md"}`,
			want:  MatchNone,
		},
		{
			name:  "write path pattern",
			allow: []string{"write(*.md)"},
			tool:  "write",
			args:  `{"file_path":"README.md"}`,
			want:  MatchAllow,
		},
		{
			name:  "write path field (canonical JSON)",
			allow: []string{"write(*.md)"},
			tool:  "write",
			args:  `{"path":"README.md"}`,
			want:  MatchAllow,
		},
		{
			name:  "apply_patch blob path",
			allow: []string{"apply_patch(*.go)"},
			tool:  "apply_patch",
			args:  `{"patch":"*** Begin Patch\n*** Update File: cmd/quikagent/main.go\n*** End Patch\n"}`,
			want:  MatchAllow,
		},
		{
			name:  "apply_patch all targets must match allow",
			allow: []string{"apply_patch(*.go)"},
			tool:  "apply_patch",
			args:  `{"patch":"*** Begin Patch\n*** Update File: ok.go\n*** Add File: evil.sh\n*** End Patch\n"}`,
			want:  MatchNone,
		},
		{
			name:  "apply_patch deny wins on any target",
			allow: []string{"apply_patch(*)"},
			deny:  []string{"apply_patch(*.sh)"},
			tool:  "apply_patch",
			args:  `{"patch":"*** Begin Patch\n*** Update File: ok.go\n*** Add File: evil.sh\n*** End Patch\n"}`,
			want:  MatchDeny,
		},
		{
			name:  "apply_patch all go files allowed",
			allow: []string{"apply_patch(*.go)"},
			tool:  "apply_patch",
			args:  `{"patch":"*** Begin Patch\n*** Update File: a.go\n*** Add File: b.go\n*** End Patch\n"}`,
			want:  MatchAllow,
		},
		{
			name: "apply_patch blob path denied",
			deny: []string{"apply_patch(*.go)"},
			tool: "apply_patch",
			args: `{"patch":"*** Begin Patch\n*** Add File: internal/foo.go\n*** End Patch\n"}`,
			want: MatchDeny,
		},
		{
			name: "edit path pattern denied",
			deny: []string{"edit(*.go)"},
			tool: "edit",
			args: `{"file_path":"internal/tools/tool.go"}`,
			want: MatchDeny,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckPermission(tt.allow, tt.deny, tt.tool, tt.args)
			if got != tt.want {
				t.Errorf("CheckPermission(%v, %v, %q, %s) = %v, want %v",
					tt.allow, tt.deny, tt.tool, tt.args, got, tt.want)
			}
		})
	}
}

func TestNeedsInteractiveApproval(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args string
		want bool
	}{
		{"write", "write", `{"path":"x"}`, true},
		{"edit", "edit", `{"path":"x"}`, true},
		{"apply_patch", "apply_patch", `{"patch":"..."}`, true},
		{"task", "task", `{"description":"x","prompt":"y"}`, true},
		{"todo", "todo", `{"todos":[]}`, true},
		{"read", "read", `{"path":"x"}`, false},
		{"bash git", "bash", `{"command":"git status"}`, false},
		{"bash rm", "bash", `{"command":"rm -rf /tmp/x"}`, true},
		{"mcp", "mcp_search", `{}`, true},
		{"list", "list", `{"path":"."}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsInteractiveApproval(tt.tool, tt.args); got != tt.want {
				t.Fatalf("NeedsInteractiveApproval(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}
}
