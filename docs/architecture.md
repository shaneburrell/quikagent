# Architecture

Frontend-agnostic agent loop: the model streams events; the TUI and web
UI subscribe to the same event types.

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
internal/text        output clipping
```

See [AGENTS.md](../AGENTS.md) for conventions when changing this tree.
