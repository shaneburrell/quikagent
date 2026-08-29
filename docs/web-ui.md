# Web UI

`--web` and `--desktop` serve an embedded HTML page over HTTP with SSE
streaming. It is a loopback companion to the TUI, not a full remote IDE.

Remote access (SSH, Tailscale, Cloudflared): [hosting.md](hosting.md).
CLI flags: [usage.md](usage.md).

## Start

Print and `--web` need a key via env or an existing
`~/.quikagent/config.yaml`. They do not open the TUI setup screen.

```sh
# needs QUIKAGENT_API_KEY or ~/.quikagent/config.yaml
quikagent --web 127.0.0.1:8080
quikagent --web :8080          # same as 127.0.0.1:8080
quikagent --web 8080           # same
quikagent --desktop            # free loopback port + system browser
```

`--web` cannot be combined with `-p`. `--desktop` is a loopback browser
open, not a native webview.

`--web-listen-all` allows a non-loopback bind (for example `0.0.0.0:8080`).
That is **not** a supported hosted path. See [hosting.md](hosting.md).

## What it can do

| Action | How |
|--------|-----|
| Send a turn | Prompt field + Send |
| Stream tokens, tools, errors | `GET /events` (EventSource) |
| Cancel the turn | Cancel button → `POST /cancel` |
| Plan / build | Mode button → `POST /mode` |
| Approve a mutating tool | Allow / Always / Deny → `POST /approve` |
| Answer a `question` | Options, custom text, or Skip → `POST /answer` |
| List / resume / new session | Sidebar → `GET /sessions`, `POST /session/resume`, `POST /session/new` |
| Rehydrate after reload | `GET /api/state` (history, mode, busy, todos) |
| Liveness | `GET /health` → `ok` |

One turn at a time. A second Send while busy returns HTTP 409.
Session switch is also blocked while a turn is running.

`-yes` is **print mode only**. The web UI prompts for mutating
`write` / `edit` / `apply_patch` / `bash` (when the command looks
mutating) and MCP tools unless `permissions.allow` already matches.
**Always** is per tool name for the rest of that web session (cleared
on session switch).

`@path` and `@git` still expand on the server if you type them in the
prompt. There is no mention picker.

## What only the TUI has

- First-run `/setup` and `/config`
- `/models`, F2, `/model`, `/router`
- `/compact`, `/undo`, `/redo`
- Slash commands, command palette, project `.quikagent/commands/`
- Markdown rendering
- Sidebar richness (git, MCP, context tokens) — web shows todos + sessions
- Multiline input (Shift+Enter)

## Reconnect and approvals

Closing the tab does **not** cancel an in-flight turn. EventSource
reconnects and shows a “connection lost” banner.

`/api/state` restores messages, mode, busy, and todos. It does **not**
include a pending approval or question. If you refresh while the agent
is waiting on Allow/Deny, the card is gone and the turn stays blocked
until you Cancel (or the turn ends).

Keep the tab open for approvals, or pre-allow tools in
`permissions.allow` ([config.md](config.md)).

## HTTP surface

Unauthenticated. Same origin as the page. No TLS.

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/` | Embedded UI |
| GET | `/events` | SSE event stream |
| POST | `/turn` | `{ "prompt": "..." }` |
| POST | `/cancel` | Cancel in-flight turn |
| POST | `/mode` | `{ "mode": "plan" }` or `"build"` |
| POST | `/approve` | `{ "id", "allow", "always" }` |
| POST | `/answer` | `{ "id", "answer" }` |
| GET | `/sessions` | List saved sessions |
| GET | `/api/state` | Snapshot for reconnect |
| POST | `/session/resume` | `{ "id" }` |
| POST | `/session/new` | Empty session |
| GET | `/health` | `ok` (not readiness) |

## Next

[hosting.md](hosting.md) · [usage.md](usage.md)
