# Contributing

Thanks for wanting to improve quikagent.

## Build and test

```sh
go test ./...
go build -o quikagent ./cmd/quikagent
```

Smoke-check the binary with `-version`, print mode (`-p`), or `--web` on loopback.

See [AGENTS.md](AGENTS.md) for package layout and conventions.

## Pull requests

- Keep changes focused. Match existing Go style: small packages, table-driven tests, clear errors.
- Add or update tests for behavior you change.
- Do not commit API keys, `~/.quikagent/config.yaml`, SSH keys, kubeconfigs, or `.env` files.
- File tools must stay sandboxed to the workdir (except `fetch`).
- Do not add heavy frameworks without a clear need.

## Reporting issues

Use GitHub issues for bugs and feature requests. For sandbox escapes, prompt-injection, or key leaks, see [SECURITY.md](SECURITY.md) instead of filing a public issue.
