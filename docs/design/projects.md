# Spec: project registry and worktrees (Slice 3)

**Status:** proposed. Not in the binary.

Depends on Slice 1 `--workdir` + session metadata ([serve.md](serve.md)).
Best with Slice 2 so jobs can name a project instead of a raw path
([jobs.md](jobs.md)).

## Problem today

- One process, one `os.Getwd()`. Switching repos means restart.
- Sessions have no repo field. Resume in repo B can show repo A
  history.
- Parallel jobs (or two web chats) would clobber the same tree.
- No clone-on-demand for "open this GitHub URL on the box".

## Goals

- Named projects: path, default profile, default model.
- Every session and job records which project it used.
- A job can run in an isolated git worktree, not the operator's
  dirty checkout.
- Optional clone if the project path is missing.

## Non-goals (Slice 3)

- Multi-user home directories or container-per-job (Slice 4).
- First-class "open PR" product (can use `git` + `gh` in the job
  prompt).
- Semantic codebase index.

## Project registry

`~/.quikagent/projects.yaml`:

```yaml
projects:
  quikagent:
    path: /home/ubuntu/src/quikagent
    profile: trusted-repo
    remote: git@github.com:shaneburrell/quikagent.git
  notes:
    path: /home/ubuntu/src/notes
    profile: readonly
```

Rules:

- `path` is required and must stay under an allow-listed parent
  (default: the operator's home `src` / configured `projects_root`)
  so a job cannot set `path: /`.
- `remote` is used only when `path` does not exist and
  `clone: true` on the job or project.
- Overlay: `.quikagent/config.yaml` inside `path` still field-merges
  as today.

CLI / HTTP (on `serve`):

```sh
quikagent serve --project quikagent
```

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/projects` | List name, path, exists |
| POST | `/session/new` | `{ "project": "quikagent" }` |
| POST | `/jobs` | `{ "project": "quikagent", … }` |

`--workdir` remains for one-off paths that are not in the registry.

## Session metadata

Each session stores:

```json
{ "project": "quikagent", "workdir": "/home/ubuntu/src/quikagent", "worktree": "" }
```

Resume refuses to attach a session whose `workdir` is not the
process (or job) workdir, unless the client passes
`{ "force": true }`. The web UI shows the mismatch instead of
silently editing the wrong repo.

## Workdir per job

A job with `"project": "quikagent"` sets the agent sandbox to that
path for the run. Two **sequential** jobs can share the project
path (`overlap: skip`).

Two **parallel** jobs on the same project require a worktree
(below) or they are rejected.

Interactive `serve` can switch the live agent workdir when idle
(`POST /project { "name": "notes" }`). Busy → 409, same as today.

## Worktrees

```json
{
  "project": "quikagent",
  "worktree": true,
  "branch": "agent/job-{{run_id}}"
}
```

On run start:

1. `git worktree add` under `~/.quikagent/worktrees/<project>/<run_id>`
   (must stay on the same filesystem as `path`).
2. Agent `Workdir` = that tree.
3. On run `done` / `failed`, keep the tree for inspection; optional
   `worktree_ttl` garbage-collects. Never `git worktree remove --force`
   if the tree has commits not pushed, unless the job says
   `cleanup: discard`.

Interactive chats default to the project path (no worktree) so the
operator sees their files. Background jobs default to `worktree: true`
when the project is a git repo.

## Clone-on-demand

If `path` is missing and `remote` is set:

```sh
git clone -- <remote> <path>
```

Deploy keys live on the host (ssh-agent or `GIT_SSH_COMMAND`), not
in job JSON. Fail the run if clone fails. Do not clone arbitrary
URLs from a webhook body without an allow-list (`remote` on the
project record only).

## Isolation reminder

`bash` is still not filesystem-sandboxed. A worktree stops two jobs
from editing the same files; it does not stop `bash` from `rm -rf`
elsewhere. Slice 4 (container per job) is the real sandbox. Until
then, `permissions.deny` + profile + host user is the control.

## Docs when this ships

User-facing: session↔repo, worktrees, clone-on-demand — either a
short section in [../hosting.md](../hosting.md) or `docs/projects.md`.
Link from the README docs table.

## Unlock

Slice 4 (team/cloud) can map one project to one container. Not now.
