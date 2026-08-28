# Usage

## CLI

```sh
quikagent [flags] [prompt...]
```

A trailing prompt (when not starting with `-`) is the same as `-p`.

| Flag | Action |
|------|--------|
| (none) | Interactive TUI; first run opens setup if no API key |
| `-p "task"` | Print mode: one turn on stdout, then exit |
| `-yes` | Auto-approve mutating tools in **print mode only** |
| `--continue` | Resume the latest session (creates one if none) |
| `--session <id>` | Resume by session id |
| `--plan` | Start in plan (read-only tools) mode |
| `--web ADDR` | Web UI. Host defaults to `127.0.0.1` (`8080` or `:8080`) |
| `--web-listen-all` | Allow `--web` to bind a non-loopback address |
| `--desktop` | Bind a free loopback port and open the system browser |
| `--export <id>` | Print a session as markdown and exit |
| `--continue --export <id>` | Export the latest session (`<id>` is ignored) |
| `-version` | Print version and exit |

`--web` cannot be combined with `-p`. `--desktop` is a loopback browser
open, not a native webview.

Sessions are JSONL under `~/.quikagent/sessions`.

## TUI keys

| Key | Action |
|-----|--------|
| `enter` | send |
| `shift+enter` | newline |
| `tab` | build / plan |
| `f2` / `shift+f2` | cycle models (favorites + auto) |
| `ctrl+p` | command palette |
| `ctrl+b` | toggle sidebar (needs width ≥ 100) |
| `shift+↑/↓`, `pgup`/`pgdn`, or mouse wheel | scroll transcript |
| wheel on input | move caret in multiline input |
| `↑/↓` (empty input) | prompt history |
| `esc` | clear input / close overlay |
| `ctrl+c` | cancel turn |
| `ctrl+q` | quit |

## Slash commands

`/setup` (alias `/connect`), `/config`, `/models`, `/model [name|auto]`,
`/router [on|off]`, `/plan`, `/build`, `/mode [plan|build]`, `/help`,
`/clear`, `/sessions`, `/resume <id>`, `/compact`, `/refresh`, `/undo`,
`/redo`, `/init`.

- **`/plan`** / **`/build`** / **`/mode`** — switch tool surface (works during a turn). `/mode` with no argument toggles. Same as **Tab**.

- **`/models`** — pick from API `/v1/models` (merged with config defaults); first row is **auto (Arch-Router)**.
- **`/model auto`** / **F2** — enable per-turn routing; pinning a model disables it until auto again.
- **`/config`** — connection, model, router, sidebar default.
- **`/init`** — write a starter `AGENTS.md` in the workdir if missing.
- **`/compact`** — summarize older turns to free context (uses `small_model`).

Status bar shows `auto·nano→` or `pin·` before the model name.

### Project commands

Markdown files in `.quikagent/commands/*.md` become extra slash commands.
`/foo` submits the contents of `.quikagent/commands/foo.md` as the user
turn.

## Approvals

Mutating `bash` / `write` / `edit` / `apply_patch` prompt `y` / `n` / `A`
in the TUI. **Always** (`A`) is for that **tool name** for the rest of
the session. Web **Always** is the same scope.

`-yes` auto-approves in print mode only; it does not change the web UI.

## Print, plan, web

- **Print:** one turn, stdout. Prompts on stdin unless `-yes`.
- **Plan:** read-only tools only (`--plan`, **Tab**, or `/plan` / `/build`).
- **Web:** SSE UI on loopback by default. Do not bind on the LAN unless
  you pass `--web-listen-all` on purpose.

## Sidebar

When the terminal is wide enough, a right pane shows session, model and
route, context tokens, MCP servers, `git status`, recent sessions, and
workdir. Toggle with `ctrl+b`. Default on/off is `sidebar` in config.

## Next

[tools.md](tools.md) · [config.md](config.md)
