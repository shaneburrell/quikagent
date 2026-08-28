package tools

import (
	"fmt"
	"strings"

	"quikagent/internal/config"
)

// AttachMCP connects configured stdio MCP servers and registers their tools.
// Servers with empty command are skipped. Failed servers are reported as
// warnings and do not abort startup.
func AttachMCP(r *Registry, servers map[string]config.MCPServer) (warnings []string, err error) {
	for name, srv := range servers {
		if strings.TrimSpace(srv.URL) != "" && strings.TrimSpace(srv.Command) == "" {
			warnings = append(warnings, fmt.Sprintf("mcp %s: remote MCP URL is configured but not yet supported; use a stdio command", name))
			continue
		}
		if strings.TrimSpace(srv.Command) == "" {
			continue
		}
		if e := attachOneMCP(r, name, srv); e != nil {
			warnings = append(warnings, fmt.Sprintf("mcp %s: %v", name, e))
		}
	}
	return warnings, nil
}
