# quikagent

A minimal terminal coding agent in Go. Hand-rolled TUI (no framework),
OpenAI-compatible LLM client, sandboxed tools, JSONL sessions, and an
optional loopback web UI.

MIT licensed — see [LICENSE](LICENSE).

## Install

```sh
brew install shaneburrell/tap/quikagent
```

Or clone and build (module path is `quikagent`; do not `go install github.com/…`):

```sh
git clone https://github.com/shaneburrell/quikagent.git
cd quikagent
go build -o quikagent ./cmd/quikagent
```

See [docs/install.md](docs/install.md) for requirements and `quikagent -version`.

## Quick start

First interactive launch opens a setup screen (`/setup`) for API key, base URL,
and model. Values are saved to `~/.quikagent/config.yaml` (mode `0600`).

```sh
export QUIKAGENT_API_KEY=...   # optional; wins over the file
quikagent                      # TUI
```

Print and `--web` modes need a key via env or an existing config file (they do
not open setup). Any OpenAI-compatible endpoint works (**BYOK** — you pay the
provider). Defaults and overlays are in [docs/config.md](docs/config.md).

To run on an always-on box and open the UI from another machine, bind
loopback and use SSH, Tailscale, or Cloudflared — [docs/hosting.md](docs/hosting.md).
Do not use `--web-listen-all` as hosted access.

## Usage

```sh
quikagent                         # interactive TUI
quikagent -p "task"               # print mode
quikagent -yes -p "task"          # print mode, auto-approve mutating tools
quikagent --continue              # resume latest session
quikagent --session <id>          # resume by id
quikagent --plan                  # plan (read-only) mode
quikagent --web :8080             # web UI (loopback)
quikagent --desktop               # loopback web UI in the system browser
quikagent --export <id>           # print a session as markdown
quikagent --continue --export x   # export latest session
quikagent -version
```

Keys, slash commands, sessions, and approvals: [docs/usage.md](docs/usage.md).
Web UI: [docs/web-ui.md](docs/web-ui.md).
Remote / always-on: [docs/hosting.md](docs/hosting.md).
Tools and sandbox: [docs/tools.md](docs/tools.md).
Skills, hooks, custom agents: [docs/extending.md](docs/extending.md).

## Docs

| Guide | Topic |
|-------|--------|
| [Install](docs/install.md) | Homebrew, tarball, source build |
| [Config](docs/config.md) | YAML, env, providers, MCP, permissions |
| [Usage](docs/usage.md) | CLI, TUI, print, plan |
| [Web UI](docs/web-ui.md) | Loopback SSE UI, approvals, sessions |
| [Hosting](docs/hosting.md) | Always-on + SSH / Tailscale / Cloudflared |
| [Tools](docs/tools.md) | Built-ins, sandbox, `@` mentions |
| [Extending](docs/extending.md) | Skills, hooks, commands, agents |
| [Lab VM](docs/harvester.md) | Harvester cloud-init (not the hosted guide) |
| [Architecture](docs/architecture.md) | Package map |
| [Design specs](docs/design/) | Proposed `serve` / jobs / projects (not shipped) |

## License

MIT — see [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

Do not file public issues for sandbox escapes, prompt-injection, or key leaks.
See [SECURITY.md](SECURITY.md).
