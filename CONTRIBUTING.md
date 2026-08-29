# Contributing

Thanks for wanting to improve quikagent.

Product behavior for users lives in [docs/](docs/). Package layout and
conventions for agents are in [AGENTS.md](AGENTS.md).

## Build and test

```sh
gofmt -w .
go vet ./...
golangci-lint run ./...
go test ./...
go build -o quikagent ./cmd/quikagent
```

CI also runs `go test -race ./...`. Config is [`.golangci.yml`](.golangci.yml)
(standard linters plus correctness/security extras). Smoke-check the binary
with `-version`, print mode (`-p`), or `--web` on loopback.

## Go and module updates

Bump the toolchain and modules by hand (no Dependabot). After `go get` /
`go mod tidy`, run lint and tests and commit `go.mod` with `go.sum`.

## Pull requests

- Keep changes focused. Match existing Go style: small packages, table-driven tests, clear errors.
- Add or update tests for behavior you change.
- Do not commit API keys, `~/.quikagent/config.yaml`, SSH keys, kubeconfigs, or `.env` files.
- File tools must stay sandboxed to the workdir (except `fetch`).
- Do not add heavy frameworks without a clear need.
- If you change flags, config, or tools, update the matching page under `docs/`.

## Releases

Push a `v*` tag on GitHub to run GoReleaser (darwin/linux archives). After
the release assets exist, bump the formula in
[shaneburrell/homebrew-tap](https://github.com/shaneburrell/homebrew-tap)
(version + sha256), same as quiksync.

## Reporting issues

Use GitHub issues for bugs and feature requests. For sandbox escapes,
prompt-injection, or key leaks, see [SECURITY.md](SECURITY.md) instead of
filing a public issue.
