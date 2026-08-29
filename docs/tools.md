# Tools

The model gets a registry sandboxed to the working directory (except
`fetch`). Plan mode exposes read-only tools only. A step that contains
only read-only tools (except `question`) may run those calls
concurrently; mutators, MCP, `task`, `todo`, and `question` keep the
whole step serial.

## Built-in

Registered by default:

| Tool | Role | Plan |
|------|------|------|
| `bash` | Host shell (not filesystem-sandboxed) | no |
| `read` | Read a file | yes |
| `write` | Create/overwrite a file | no |
| `edit` | Search/replace in a file | no |
| `glob` | Find paths by pattern | yes |
| `grep` | Search file contents | yes |
| `list` | Directory listing | yes |
| `fetch` | HTTP GET (public URLs; blocks private/loopback) | yes |
| `git` | Git in the workdir | yes |
| `todo` | In-session todo list | no |
| `question` | Ask the user a structured question | yes |
| `apply_patch` | Multi-file patch | no |

## Optional

| Tool | When | Plan |
|------|------|------|
| `skill` | Always available once the agent starts (loads `SKILL.md`; advertises installed names) | yes |
| `task` | Spawns a child agent (`explore`, `general`, or custom) | no |
| `websearch` | `websearch_url` / `QUIKAGENT_WEBSEARCH_URL` | yes |
| `lsp` | `lsp.command` set | yes |
| `mcp_<name>_<tool>` | Each tool from a configured MCP server | no |

## Sandbox

- File tools (`read` / `write` / `edit` / `glob` / `grep` / `list` /
  `apply_patch`) stay inside the workdir (symlink-safe).
- `fetch` is not workdir-scoped; it refuses private and loopback targets.
- `bash` is **not** filesystem-sandboxed (the agent needs host tools).
  Keys and tokens are scrubbed from its environment
  (`API_KEY`, `SECRET`, `TOKEN`, `PASSWORD`, `QUIKAGENT_*`).
- Tool output is capped (32 KiB) before it returns to the model.

## Mentions

In a user turn:

- `@path` expands to file contents, or a directory listing.
- `@git` expands to `git status` plus a diffstat.

Tokens that escape the workdir or fail to resolve are left as-is.

## Next

[extending.md](extending.md) · [usage.md](usage.md)
