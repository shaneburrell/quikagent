# Review catalog dispositions (Wave 3)

Items fixed in the security campaign (P0–P1 + selected P2) are covered by
regression tests under `internal/{tools,agent,session,server}`.

## Wontfix (documented threat model)

| # | Item | Reason |
|---|------|--------|
| — | Bash host FS access | Intentional for a coding agent; env secrets scrubbed |
| — | API key encryption | `config.yaml` remains plaintext mode 0600 |
| — | Full web auth / mTLS | Loopback default + `--web-listen-all` gate instead |
| — | Harvester NOPASSWD sudo | Lab image convenience; documented in README |
| — | Tab toggles mode | Documented keybinding |
| — | `term.Size()-1` | Deliberate margin against terminal wrap |
| — | go.mod Go 1.27 pin | Matches local toolchain |
| — | Multiply≈add soak false-pass | Eval hygiene; not a product bug |

## Deferred (follow-up)

| Area | Notes |
|------|-------|
| SSE backpressure metrics | Non-blocking drop remains; JS now checks `/turn` status |
| MCP child reap on exit | Soft-fail attach + timeouts landed; Close() registry later |
| Router deep-merge routes | Fallback still works; merge polish later |
| Compaction ↔ session Replace wiring in TUI | Compact boundary fix in agent; TUI pendingBase sync later |
| Wide-char cursor / PageUp keys | TUI polish backlog |
| CI `-race` / GitLab CI | GitHub workflow remains; optional later |

This file is the closeout checklist for the ~100-item review campaign.
