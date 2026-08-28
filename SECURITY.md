# Security

## Reporting a vulnerability

Do **not** open a public GitHub issue for:

- Tool sandbox escapes or workdir bypasses
- Prompt-injection that leaks secrets or runs unexpected tools
- API key, session, or credential exposure
- Anything that would help attack a running `--web` instance

Report privately via [GitHub Security Advisories](https://github.com/shaneburrell/quikagent/security/advisories/new) once the public repository exists, or email shane@shaneburrell.com.

Please include a short description, impact, and steps to reproduce.

## Non-goals (documented threat model)

- `bash` is not filesystem-sandboxed; keys and tokens are scrubbed from its environment.
- `~/.quikagent/config.yaml` stores the API key in plaintext (mode `0600`).
- The web UI binds loopback by default. `--web-listen-all` is opt-in.
