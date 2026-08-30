# AGENTS.md — quikagent

Tool-agnostic project guidance for coding agents (Cursor, OpenCode, quikagent, Claude Code, etc.).

## Overview

**quikagent** is a minimal terminal coding agent in Go: hand-rolled TUI, OpenAI-compatible LLM client, sandboxed tools, JSONL sessions, optional web UI and Arch-Router.

Human product docs live in [docs/](docs/).

- Module: `quikagent`
- Entry: `cmd/quikagent`
- License: MIT

## Build & test

```sh
gofmt -w .
go vet ./...
golangci-lint run ./...
go test ./...
go test -race ./...
go build -o quikagent ./cmd/quikagent
```

Prefer `go test ./...` (and `golangci-lint run ./...` when the linter is installed) before claiming a change works. Use the local binary for smoke checks (`-version`, `-p`, `--web`).

CI runs vet, golangci-lint, build, test, and race. Do not add Dependabot.

## Keep Go and modules current

No automated dependency PRs. When bumping:

1. Set the `go` line in `go.mod` to the current stable toolchain when you intend to require it.
2. `go get -u=patch ./...` for routine updates; `go get -u ./...` only when you mean minor/major bumps.
3. `go mod tidy`
4. `go test ./...` and `golangci-lint run ./...`
5. Smoke-build `./cmd/quikagent` (`-version`).

Do not bump a module that is only used in comments or docs. Commit `go.mod` and `go.sum` together.

## Layout

| Path | Role |
|------|------|
| `cmd/quikagent` | CLI flags, print/web wiring, first-run setup |
| `internal/config` | YAML + env config |
| `internal/llm` | OpenAI-compatible SSE client |
| `internal/router` | Arch-Router model selection |
| `internal/tools` | Tool registry + sandbox (+ MCP) |
| `internal/agent` | Model↔tool loop, system prompt |
| `internal/session` | JSONL + `.trace.jsonl` sidecar under `~/.quikagent/sessions` |
| `internal/tui` | Alternate-screen TUI |
| `internal/server` | HTTP/SSE web frontend |
| `internal/hooks` | Project `.quikagent/hooks/` pre-tool / post-tool |
| `internal/mention` | `@path` / `@git` expansion in user turns |
| `deploy/harvester` | Lab VM manifests (Harvester) |

## Conventions

- Match existing Go style: small packages, clear errors, table-driven tests.
- Keep the TUI and agent loop frontend-agnostic via events.
- Tools must stay sandboxed to the workdir (except `fetch`).
- Do not add heavy frameworks without a clear need.
- Prefer editing existing files over new abstraction layers.

## Secrets & safety

- Session traces (`<id>.trace.jsonl`) stay local (mode `0600`) and must
  not be sent to the model or committed.
- Never commit API keys, deploy private keys, or `~/.quikagent/config.yaml`.
- Env vars (`QUIKAGENT_API_KEY`, etc.) win over config file — useful for CI/lab.
- Do not open `--web` on LAN by default; bind `127.0.0.1`. Remote access
  is SSH, Tailscale, or Cloudflared ([docs/hosting.md](docs/hosting.md)).
  `--web-listen-all` is not a supported hosted path.
- Print mode requires `--yes` to auto-approve mutating tools; otherwise prompts on stdin. `--yes` does not apply to `--web`.
- `bash` is intentionally **not** filesystem-sandboxed (coding agent needs host tools); keys/tokens are scrubbed from its env. File tools (`read`/`write`/`edit`/`glob`/`grep`) are sandboxed to the workdir (symlink-safe).
- API key in `config.yaml` is plaintext with mode `0600` (not OS-keychain encrypted).
- Never commit or push unless the user explicitly asks.
- Harvester cloud-init must use `REPLACE_WITH_SSH_PUBKEY` — do not commit real SSH keys.

## Config

User prefs live in `~/.quikagent/config.yaml` (mode `0600`). See [docs/config.md](docs/config.md) for variables, keys, and router setup.

Project overlay `./.quikagent/config.yaml` field-merges over the home file
(permissions slices, MCP servers by name, LSP fields; router `enabled` only
when the key is explicit).
`permissions.allow` / `permissions.deny` use `tool` or `tool(glob)` (deny
wins). Optional: `providers` / `provider`, `websearch_url`, `lsp.command`,
skills (`.quikagent/skills/`), custom agents (`.quikagent/agents/`), hooks
(`.quikagent/hooks/pre-tool`, `post-tool`). `--desktop` opens the loopback
web UI in the system browser (not a native webview). `--workdir PATH`
sets the sandbox root (TUI, `-p`, `--web`). Print `--timeout` defaults
to 20m (`0` disables). `--yes` does not apply to `--web`.

## Cursor (local)

`.cursor/` is gitignored. Clone-local rules, skills, and hooks live there
and are not part of the public repo.

## Optional Claude Code

If you use Claude Code and want the same guidance, symlink:

```sh
ln -sf AGENTS.md CLAUDE.md
```

Keep `AGENTS.md` as the single source of truth.
