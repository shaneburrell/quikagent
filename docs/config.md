# Config

Settings load in this order: built-in defaults, `~/.quikagent/config.yaml`
(or legacy JSON), project overlay `./.quikagent/config.yaml`, then
environment. **Env wins.**

The home file is created with mode `0600` and stores the API key in
plaintext (not OS-keychain encrypted). Do not commit it.

Print (`-p`) and `--web` require a key via env or an existing file; they
do not open the setup screen.

## Environment

| Variable | Default | Notes |
|----------|---------|--------|
| `QUIKAGENT_API_KEY` | (from config file) | Required for print/web |
| `QUIKAGENT_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible `/v1` |
| `QUIKAGENT_MODEL` | `gpt-4o` | Chat model |
| `QUIKAGENT_SMALL_MODEL` | `gpt-4o-mini` | Compaction summarizer |
| `QUIKAGENT_PLAN_MODEL` | | Plan-mode chat model; empty honors Arch-Router |
| `QUIKAGENT_ROUTER` | off | `1` / `true` / `yes` / `on` enables |
| `QUIKAGENT_ROUTER_MODEL` | `arch-router-1.5b` | Must emit route JSON only |
| `QUIKAGENT_PROVIDER` | | Named entry under `providers:` |
| `QUIKAGENT_WEBSEARCH_URL` | | Enables the `websearch` tool |
| `QUIKAGENT_WEBSEARCH_KEY` | | Optional bearer for websearch |

## Home file

`~/.quikagent/config.yaml` example:

```yaml
api_key: "..."
base_url: https://api.openai.com/v1
model: gpt-4o
small_model: gpt-4o-mini
plan_model: gpt-4o            # optional; used for plan (read-only) turns
max_tokens: 8192
sidebar: true
provider: lab                 # optional; selects providers.lab
providers:
  lab:
    base_url: https://llm.example/v1
    api_key: "..."
    model: my-coder
    models: [my-coder, my-fast]
router:
  enabled: true
  model: arch-router-1.5b
  routes:
    nano:  { model: gpt-4o-mini, description: "Fast mechanical work…" }
    coder: { model: gpt-4o, description: "Write and refactor code…" }
    qwen:  { model: gpt-4o, description: "Architecture and deep debug…" }
    other: { model: gpt-4o, description: "Fallback…" }
mcpServers:
  demo:
    command: npx
    args: ["-y", "@modelcontextprotocol/server-everything"]
  # url: https://…   # accepted but skipped (stdio only; see extending.md)
permissions:
  allow: [read, glob, grep]
  deny: ["bash(rm *)"]
websearch_url: https://search.example/v1
websearch_key: "..."
lsp:
  command: gopls
  args: []
```

Do **not** set `model` to `arch-router-1.5b` — that model only emits route JSON.

Legacy `~/.quikagent/config.json` is read if YAML is missing; the next save
writes YAML.

## Project overlay

`./.quikagent/config.yaml` field-merges over the home file:

- `permissions.allow` / `permissions.deny` slices are appended (project
  does not wipe home deny).
- `mcpServers` merge by name.
- `lsp` fields merge.
- `router.enabled` applies only when the key is present. A bare `router:`
  block does not disable the home router.
- Other set fields (model, base URL, …) overlay as usual.

## Providers

`providers:` is a map of named OpenAI-compatible endpoints. `provider:`
(or `QUIKAGENT_PROVIDER`) copies that entry onto `base_url` / `api_key` /
`model`. A provider that omits `api_key` clears the active key so a
previous key cannot leak to the wrong host.

`providers.*.models` is accepted in YAML but **not used** yet
(`KnownModels` merges the active model, `small_model`, `plan_model`,
router targets, and the API `/v1/models` list).

## Permissions

Rules are `tool` or `tool(glob)` (deny wins). Examples: `read`,
`bash(git status*)`. Used by the TUI, print mode, and the web UI.

## Compaction

`small_model` summarizes older turns. Manual `/compact` in the TUI.
Auto-compaction also runs at the start of a turn when the session has
more than 40 messages (keeps the last 12). The web UI has no compact
control.

## Router

Optional per-turn Arch-Router (OpenCode-compatible prompts). Built-in
route keys: `nano`, `coder`, `qwen`, `other`. Enable via config,
`QUIKAGENT_ROUTER=1`, `/router on`, `/model auto`, or `/models` → auto.

## Next

[usage.md](usage.md) · [tools.md](tools.md) · [extending.md](extending.md) · [hosting.md](hosting.md)
