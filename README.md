# quikagent

A minimal terminal coding agent. Hand-rolled Go, no TUI framework — the
core is a frontend-agnostic event loop that drives a custom alternate-screen
TUI (with an OpenCode-style right sidebar) and an optional web UI.

MIT licensed — see [LICENSE](LICENSE).

## Install

```sh
brew install shaneburrell/tap/quikagent
```

Or build from source:

```sh
go build -o quikagent ./cmd/quikagent
./quikagent   # first run opens a setup screen if no API key is configured
```

## Setup

On first interactive launch (or `/setup`), enter your API key, base URL, and
model. Values are saved to `~/.quikagent/config.yaml` (mode `0600`), including
the API key.

You can still use the environment (wins over the file — useful for CI):

```sh
export QUIKAGENT_API_KEY=...
```

| Variable                 | Default                          |
|--------------------------|----------------------------------|
| `QUIKAGENT_API_KEY`      | (from `config.yaml` if unset)    |
| `QUIKAGENT_BASE_URL`     | `https://api.openai.com/v1`      |
| `QUIKAGENT_MODEL`        | `gpt-4o`                         |
| `QUIKAGENT_SMALL_MODEL`  | `gpt-4o-mini`                    |
| `QUIKAGENT_ROUTER`       | off (`1`/`true` enables)         |
| `QUIKAGENT_ROUTER_MODEL` | `arch-router-1.5b`               |

Any OpenAI-compatible endpoint works. First-run `/setup` (or env) is the
usual way to set the key, base URL, and model.

Config example (`~/.quikagent/config.yaml`):

```yaml
api_key: "..."
base_url: https://api.openai.com/v1
model: gpt-4o
small_model: gpt-4o-mini
max_tokens: 8192
sidebar: true
router:
  enabled: true
  model: arch-router-1.5b
  routes:
    nano:  { model: gpt-4o-mini, description: "Fast mechanical work…" }
    coder: { model: gpt-4o, description: "Write and refactor code…" }
    qwen:  { model: gpt-4o, description: "Architecture and deep debug…" }
    other: { model: gpt-4o, description: "Fallback…" }
```

Legacy `~/.quikagent/config.json` is still read if YAML is missing; the next
save writes YAML.

Do **not** set `model` to `arch-router-1.5b` — it only emits route JSON.

Print (`-p`) and `--web` modes require a key via env or an existing
`config.yaml` (they do not open the setup screen).

## Usage

```sh
quikagent                 # interactive TUI
quikagent -p "task"       # print mode
quikagent -yes -p "task"  # print mode, auto-approve mutating tools (print only)
quikagent --continue      # resume latest session (including an empty latest)
quikagent --session <id>  # resume by id
quikagent --plan          # plan (read-only) mode
quikagent --web :8080     # web UI (loopback)
quikagent --desktop       # bind a free loopback port and open the system browser
quikagent -version
```

## Keys

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

Commands: `/setup` (alias `/connect`), `/config`, `/models`, `/model [name\|auto]`,
`/router [on\|off]`, `/help`, `/clear`, `/sessions`, `/resume <id>`, `/compact`,
`/refresh`, `/undo`, `/redo`, `/init`.

- **`/models`** — pick from API `/v1/models` (merged with config defaults); first row is **auto (Arch-Router)**.
- **`/model auto`** / **F2** — enable per-turn Arch-Router; pin a model disables routing until auto again.
- **`/config`** — settings list (connection, model picker, router, sidebar default).
- **`ctrl+p`** — command palette.

Status bar shows `auto·nano→` or `pin·` before the model name. The sidebar
MODEL section lists mode, last route, and the route→model map.

Mutating `bash` / `write` / `edit` / `apply_patch` prompts for `y`/`n`/`A` (always this **tool name** for the rest of the session) in the TUI. Web **Always** is the same tool-name scope. `--yes` auto-approves in **print mode only**; it does not change the web UI.

## Sidebar

When the terminal is wide enough, a right pane shows session, model+route,
context tokens, MCP servers, `git status`, recent sessions, and workdir.
Toggle with `ctrl+b`. Default on/off is stored as `sidebar` in config.yaml.

## Arch-Router

Optional per-turn model selection (Arch-Router-1.5B), compatible with the
OpenCode plugin prompts. Enable via config, `/config`,
`/models` → auto, `/router on`, `/model auto`, or `QUIKAGENT_ROUTER=1`.
`/model name` pins and disables routing for that session until auto again.

## Tools

`bash`, `read`, `write`, `edit`, `glob`, `grep`, `list`, `fetch`, `git`,
`todo`, `question`, `apply_patch`, `skill`, `task` — sandboxed to the working
directory (except `fetch`). Optional `websearch` (`websearch_url` /
`QUIKAGENT_WEBSEARCH_URL`) and `lsp` (`lsp.command`) when configured.
Plan mode exposes read-only tools only. Optional MCP servers register as
`mcp_<name>_<tool>`.

`@path` in a prompt expands to file contents (or a directory listing);
`@git` expands to `git status` + diffstat. Project overlay
`.quikagent/config.yaml` overlays `~/.quikagent/config.yaml`: permission
allow/deny slices are merged (project does not wipe home deny), `mcpServers`
merge by name, `lsp` fields merge, and `router.enabled` is applied only when
the key is present (a bare `router:` block does not disable the home router).
Named `providers:` / `QUIKAGENT_PROVIDER` switch endpoints. Permission
`allow`/`deny` rules use `tool` or `tool(pattern)` (deny wins). Hooks:
`.quikagent/hooks/pre-tool` and `post-tool` (stdin JSON: `phase`, `tool`,
`args`). Skills live in `.quikagent/skills/` or `~/.quikagent/skills/`;
custom subagents in `.quikagent/agents/*.md`.

## Architecture

```
cmd/quikagent        flags, print mode, web wiring, first-run setup
internal/config      YAML + env (+ router, api_key, KnownModels)
internal/llm         OpenAI-compatible SSE + ChatOnce + ListModels
internal/router      Arch-Router selection
internal/tools       registry + sandbox + MCP
internal/agent       model→tool loop, events
internal/session     JSONL in ~/.quikagent/sessions
internal/tui         TUI + sidebar + setup/config/palette/models
internal/server      HTTP/SSE web frontend
internal/hooks       pre-tool / post-tool scripts
internal/mention     @path / @git expansion
```

## AGENTS.md

Root [AGENTS.md](AGENTS.md) is tool-agnostic project guidance. When present in
the working directory, quikagent appends it to the system prompt (capped at
32 KiB). Optional Claude Code symlink: `ln -sf AGENTS.md CLAUDE.md`.

## Lab VM (Harvester)

Manifests under [deploy/harvester/](deploy/harvester/) are a starting point for
a lab VM (8 CPU / 32 GiB, Ubuntu cloud image) on Harvester. Replace the
`REPLACE_WITH_*` placeholders (image ID, storage class, VLAN/network, SSH
public key) with values from your cluster before apply.

```sh
export KUBECONFIG=/path/to/harvester.kubeconfig
kubectl apply -f deploy/harvester/quikagent-lab-cloudinit.yaml
kubectl apply -f deploy/harvester/quikagent-lab.yaml
```

Long-running agent host uses systemd `quikagent --web 127.0.0.1:8080` (do not
bind the web UI on the LAN). Tunnel from your laptop:

```sh
ssh -L 8080:127.0.0.1:8080 ubuntu@<vm-ip>
# then open http://127.0.0.1:8080
```

Before `kubectl apply`, replace `REPLACE_WITH_SSH_PUBKEY` in
`deploy/harvester/quikagent-lab-cloudinit.yaml` with your real `ssh-ed25519`
public key. Cloud-init grants passwordless sudo for the lab `ubuntu` user
(intentional for this lab image).

Interactive TUI soaks: SSH in and run under `tmux`. Seed
`~/.quikagent/config.yaml` on the VM (never commit secrets). Clone this
repository onto the VM with a deploy key or HTTPS.

Print-mode automation on the VM should pass `-yes` when tool auto-approval is
required (`quikagent -yes -p "..."`).

## License

MIT — see [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Please read the
[code of conduct](CODE_OF_CONDUCT.md) before opening issues or pull requests.

## Security

Do not file public issues for sandbox escapes, prompt-injection, or key leaks.
See [SECURITY.md](SECURITY.md).
