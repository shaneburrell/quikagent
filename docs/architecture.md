# Architecture

Frontend-agnostic agent loop: the model streams events; the TUI and web
UI subscribe to the same event types.

```
cmd/quikagent        flags, print mode, web wiring, first-run setup
internal/config      YAML + env (+ router, api_key, KnownModels)
internal/llm         OpenAI-compatible SSE + ChatOnce + ListModels
internal/router      Arch-Router selection
internal/tools       registry + sandbox + MCP
internal/agent       model→tool loop, events (read-only batches may run in parallel)
internal/session     JSONL + .trace.jsonl in ~/.quikagent/sessions
internal/tui         TUI + sidebar + setup/config/palette/models
internal/server      HTTP/SSE web frontend
internal/hooks       pre-tool / post-tool scripts
internal/mention     @path / @git expansion
internal/text        output clipping
```

User guides: [install.md](install.md), [hosting.md](hosting.md),
[web-ui.md](web-ui.md). Proposed daemon / jobs / projects:
[design/](design/). Conventions when changing this tree:
[AGENTS.md](../AGENTS.md).
