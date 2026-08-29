# Extending

Project files live under `.quikagent/` in the workdir. User-wide skills
also load from `~/.quikagent/skills/`.

## AGENTS.md

If `AGENTS.md` exists in the working directory, quikagent appends it to
the system prompt (capped at 32 KiB; must stay inside the workdir).
`/init` writes a starter file when missing.

This repo’s own [AGENTS.md](../AGENTS.md) is tool-agnostic guidance for
coding agents. Optional Claude Code symlink:

```sh
ln -sf AGENTS.md CLAUDE.md
```

## Skills

The `skill` tool loads a named `SKILL.md`:

1. `.quikagent/skills/<name>/SKILL.md`
2. `.quikagent/skills/<name>.md`
3. `~/.quikagent/skills/<name>/SKILL.md`
4. `~/.quikagent/skills/<name>.md`

Names cannot contain path separators.

## Hooks

Executable scripts (no extension required):

- `.quikagent/hooks/pre-tool` — stdin JSON; non-zero exit **denies** the tool
- `.quikagent/hooks/post-tool` — stdin JSON; failures are ignored

Timeout is 10 seconds if the call has no deadline.

```json
{"phase":"pre","tool":"bash","args":"{\"command\":\"git status\"}"}
```

Post-tool also includes `output`.

## Project commands

`.quikagent/commands/<name>.md` becomes `/<name>` in the TUI. The file
body is submitted as the user prompt.

## Subagents (`task`)

The `task` tool runs a child agent (max 20 steps, cannot spawn further
`task` children):

| `agent` | Behavior |
|---------|----------|
| `explore` | Read-only search (plan-mode tools) |
| `general` | Full toolset (default) |
| custom | `.quikagent/agents/<id>.md` |

Custom agent markdown may start with YAML front matter:

```markdown
---
name: reviewer
readonly: true
---
You review diffs. Do not edit files.
```

`name` / `id` set the lookup id; `readonly` / `read_only` (`true` / `1` /
`yes`) forces plan-mode tools.

## MCP

`mcpServers` in config register as `mcp_<name>_<tool>`. **stdio only:**
set `command`, optional `args` and `env`. A server with `url` and no
`command` is skipped with a stderr warning (`remote MCP URL is
configured but not yet supported`). Failed stdio attaches are also
warnings; startup continues.

Project overlay merges MCP servers by name. See [config.md](config.md).

## Next

[config.md](config.md) · [tools.md](tools.md)
