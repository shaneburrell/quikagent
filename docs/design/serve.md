# Spec: `quikagent serve` (Slice 1)

**Status:** proposed. Not in the binary. Do not document this as a
current flag. Today: `quikagent --web 127.0.0.1:8080` plus tmux or a
hand-written systemd unit ([../hosting.md](../hosting.md)).

Single-user, always-on process. Binds **loopback only**. Identity and
TLS stay in SSH, Tailscale Serve, or Cloudflared. No in-process auth,
token, or mTLS (deferred; [../REVIEW_DISPOSITIONS.md](../REVIEW_DISPOSITIONS.md)).

## Goals

- Survive logout, SSH drop, and host reboot (systemd / launchd / Docker).
- One durable process operators can reach from a phone or laptop.
- Reconnect that restores history **and** a pending approval or question.
- Named, durable permission profiles so unattended work does not hang
  on Allow/Deny (jobs in Slice 2 reuse these).
- Web parity for sessions, models, and compact — remote users live here.
- Graceful SIGTERM: drain the in-flight turn or cancel it, close MCP,
  persist, exit.

## Non-goals (Slice 1)

- Multi-user isolation, SaaS, public bind, in-process login.
- Native job scheduler (Slice 2).
- Project registry / worktrees (Slice 3) — `--workdir` is enough here.
- `--web-listen-all` as a hosted path. The flag stays; `serve` never
  uses it.

## CLI

```sh
quikagent serve [--listen 127.0.0.1:8080] [--workdir PATH] [--profile NAME]
```

| Flag | Default | Notes |
|------|---------|--------|
| `--listen` | `127.0.0.1:8080` | Reject non-loopback addresses. |
| `--workdir` | `os.Getwd()` | Sandbox root for this process until Slice 3. |
| `--profile` | `interactive` | Named permission profile (see below). |

`--web` remains for a foreground one-off. `serve` is the supervised
form: same HTTP surface, plus readiness, drain, and profiles.

Print (`-p`) and TUI are unchanged.

## Process lifecycle

```
start → load config + profile → attach MCP (warn, do not abort)
      → bind loopback → ready
SIGTERM → stop accepting turns → wait or cancel in-flight
        → persist session → MCP Close → exit 0
crash   → systemd Restart=on-failure
```

`http.ListenAndServe` today has no drain. `serve` must use
`http.Server.Shutdown` with a bounded timeout.

## HTTP additions (on top of today)

Existing routes stay ([../web-ui.md](../web-ui.md)). Add:

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/ready` | 200 if config has a key (or keyless local LLM is configured), session dir is writable, workdir exists. 503 otherwise. `/health` stays `ok`. |
| GET | `/events` | Honor `Last-Event-ID`. Replay missed events from an in-memory (or on-disk) turn ring. |
| GET | `/api/state` | Include `pending_approval` and `pending_question` when a turn is blocked. |

Without the last two, a phone refresh during Allow/Deny still wedges
the only live turn.

## Permission profiles

Today `permissions.allow` / `deny` are YAML; **Always** is RAM and
session-scoped.

`serve` loads named profiles from `~/.quikagent/profiles/<name>.yaml`
(or a `profiles:` map in config):

```yaml
# ~/.quikagent/profiles/trusted-repo.yaml
allow:
  - read
  - glob
  - grep
  - write
  - edit
  - apply_patch
  - "bash(git *)"
deny:
  - "bash(rm *)"
question: skip          # or fail | prompt
approval_timeout: 5m    # then deny (interactive profile may omit)
```

Built-in names:

| Name | Behavior |
|------|----------|
| `interactive` | Today's web/TUI prompts. Default for `serve`. |
| `unattended` | Deny anything not in `allow`. `question` → `fail` or `skip`. |
| `readonly` | Plan-mode tools only. |

Always-allow from the UI writes into the **session** until restart
unless the operator pins it onto a profile file. Jobs (Slice 2) must
use `unattended` or a custom file — never `interactive`.

## Web parity (minimum)

Remote users will not have a TUI. Slice 1 web must add:

- Session list / resume / new (already on `--web`; keep).
- Model picker + router auto (today TUI-only `/models`).
- `/compact` (today TUI-only; auto-compact at 40 messages still runs).
- Pending approval/question on reconnect (above).

Undo/redo, setup, slash commands, markdown, `@` picker can wait.

## Session ↔ workdir

Today sessions have no repo field. Slice 1 writes `workdir` into
session metadata (sidecar or first JSONL header) so resume in the
wrong CWD is visible. Switching repos without restart is Slice 3.

`--workdir` on `serve` is the process sandbox; it does not create
git worktrees.

## Supervision snippets (ship with the flag)

systemd (loopback only):

```ini
[Unit]
Description=quikagent serve
After=network.target

[Service]
Type=simple
User=ubuntu
WorkingDirectory=/home/ubuntu/src/your-repo
EnvironmentFile=-/etc/quikagent/env
ExecStart=/usr/local/bin/quikagent serve --listen 127.0.0.1:8080 --workdir /home/ubuntu/src/your-repo
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

Docker: process in the container binds `127.0.0.1:8080`; publish that
port only to the host loopback (`-p 127.0.0.1:8080:8080`). Reach it
with SSH/Tailscale/Cloudflared on the **host**. Do not `-p 8080:8080`
on a public interface.

launchd: same bind and `WorkingDirectory` rules.

## Ops

- Seed `QUIKAGENT_API_KEY` or `~/.quikagent/config.yaml` before start
  (`serve` does not open `/setup`).
- `/health` = process up. `/ready` = safe to send a turn.
- Logs: stderr (journald). No log rotation inside the binary.
- One live turn remains in Slice 1 (`409` while busy). Concurrent
  chats are Slice 2/3.

## Docs when this ships

Update [../hosting.md](../hosting.md) to prefer `quikagent serve`.
Keep `--web` as the foreground alias. Do not add listen-all examples
to the hosted guide.

## Depends on / unlocks

Unlocks Slice 2 (jobs need a long-lived runner + profiles) and
Slice 3 (`--workdir` metadata). Does not require them.
