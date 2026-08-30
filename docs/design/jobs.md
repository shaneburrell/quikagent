# Spec: background jobs (Slice 2)

**Status:** proposed. Not in the binary.

Depends on Slice 1 ([serve.md](serve.md)): a long-lived process,
durable permission profiles, and reconnect that does not lose
approvals. Slice 3 ([projects.md](projects.md)) adds per-job workdir
and worktrees; jobs can ship first with one process workdir.

## Workaround until this ships

```sh
# crontab — one-shot, no inbox, no overlap control
0 7 * * * cd /path/to/repo && quikagent -yes -p "summarize git status"
```

Limits of the workaround:

- `-yes` is print-only; the web UI cannot auto-approve.
- `question` in print skips with a stub (`No interactive frontend…`);
  the model should assume and continue. Jobs may still choose fail.
- Persist is end-of-turn. Crash mid-turn loses the user message and
  in-flight tools.
- No overlap lock, inbox, or completion notify.
- Hooks are pre/post **tool**, not a scheduler.

Treat cron + `-yes -p` as a lab script, not the product.

## Goals

- Persist a job: prompt, schedule or webhook, workdir, session,
  permission profile, notify URL.
- Run it without a human in front of Allow/Deny.
- Show runs in a web inbox (status, duration, last error).
- Notify when a run finishes or fails.
- Survive process crash mid-run (WAL) or at least restart the job
  as failed-and-retryable.

## Non-goals (Slice 2)

- Multi-tenant queues, per-user billing, GitHub App install UI.
- Interactive approvals from a phone for a background job (use a
  profile instead).
- Parallel jobs in one working tree (Slice 3 worktrees).

## Job model

Stored under `~/.quikagent/jobs/<id>.json` (or a small SQLite file —
implementation choice; JSON is enough for single-user).

```json
{
  "id": "20260829-ab12",
  "title": "morning triage",
  "prompt": "Summarize uncommitted changes and open a plan.",
  "workdir": "/home/ubuntu/src/quikagent",
  "session_id": "",
  "profile": "unattended",
  "schedule": { "cron": "0 7 * * *", "timezone": "America/New_York" },
  "webhook": { "path": "/hooks/jobs/morning", "secret": "…" },
  "notify": { "url": "https://example/hook", "events": ["done", "fail"] },
  "overlap": "skip",
  "enabled": true
}
```

Exactly one of `schedule` or `webhook` is required for automatic
runs. A job can also be `POST /jobs/:id/run` from the inbox.

| Field | Rules |
|-------|--------|
| `profile` | Must not be `interactive`. Default `unattended`. |
| `session_id` | Empty = new session per run. Set = append to that chat. |
| `overlap` | `skip` (default), `queue`, or `cancel-previous`. |
| `workdir` | Must exist in Slice 2. Clone/worktree is Slice 3. |

Run record (`~/.quikagent/jobs/<id>/runs/<run-id>.json`):

```json
{
  "run_id": "…",
  "job_id": "20260829-ab12",
  "started": "…",
  "ended": "…",
  "status": "running|done|failed|skipped|canceled",
  "session_id": "…",
  "error": "",
  "usage": { "prompt_tokens": 0, "completion_tokens": 0 }
}
```

## Scheduler

In-process ticker in `quikagent serve` (cron expression + timezone).
No extra binary. Disabled when the process is TUI/`--web`/`-p`.

Webhook ingress: `POST /hooks/jobs/<name>` with a shared secret
header. Payload is optional context appended to the prompt. Bind
stays loopback; the tunnel/mesh is the public front.

Concurrency: one **run** per job unless `overlap: queue`. Global cap
(default 1) so N crons cannot stampede the LLM. Slice 1 still has
one live agent; the job runner **is** that agent until Slice 3
allows many workdirs.

## Unattended policy

Reuse Slice 1 profiles.

| Event | `unattended` | `readonly` |
|-------|----------------|------------|
| Mutating tool not in `allow` | Deny, continue or fail the run (profile flag) | N/A (no mutating tools) |
| `question` | `fail` the tool (default) or `skip` with a stub answer | same |
| Approval timeout | N/A (no prompt) | N/A |

Do not hang a job on Allow/Deny. That is the whole point.

Print-mode `question` skips with a stub when there is no frontend.
Jobs may still choose fail or skip per profile.

## Inbox (web)

New page or sidebar on the Slice 1 UI:

- List jobs and last run status.
- Enable/disable, run now, cancel run.
- Open the session transcript for a run.

TUI can wait. API sketch:

| Method | Path |
|--------|------|
| GET | `/jobs` |
| POST | `/jobs` |
| PATCH | `/jobs/:id` |
| POST | `/jobs/:id/run` |
| POST | `/jobs/:id/cancel` |
| GET | `/jobs/:id/runs` |

Unauthenticated, loopback-only, same threat model as `serve`.

## Notify

On `done` / `fail`, HTTP POST `notify.url` with JSON
`{job_id, run_id, status, session_id, error}`. Optional later:
email, Slack. No notify is valid (inbox only).

Do not send the API key or file contents in the webhook body.

## Crash / WAL

Today persist is **end-of-turn**. A SIGKILL mid-tool loses the turn.

Slice 2 minimum:

1. On run start, append a `running` record and the user message to
   the session JSONL **before** the first model call.
2. After each tool result, append that message (mid-turn WAL).
3. On `serve` start, any `running` record becomes `failed`
   (`interrupted`) unless WAL shows a completed turn.

Retry is a new run, not a silent resume of a half-applied `write`.

## Budgets

Optional `max_steps` (default: agent max, 50) and
`max_completion_tokens` per run. Exceed → cancel, status `failed`,
notify. No dollar accounting in Slice 2 (usage fields only).

## Docs when this ships

New user guide `docs/jobs.md` (schedules, profiles, webhooks,
`question` policy, cron workaround). Link from
[../hosting.md](../hosting.md) and the README docs table.

## Unlock

Slice 3 so two jobs do not share one dirty working tree.
