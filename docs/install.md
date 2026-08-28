# Install

## Homebrew

```sh
brew install shaneburrell/tap/quikagent
quikagent -version
```

The formula tracks [GitHub Releases](https://github.com/shaneburrell/quikagent/releases)
(darwin and linux, amd64 and arm64).

## From source

Requires the Go toolchain in [go.mod](../go.mod) (currently Go 1.27).

The module path is `quikagent`, not `github.com/shaneburrell/quikagent`.
Do **not** use `go install github.com/shaneburrell/quikagent/...`.

```sh
git clone https://github.com/shaneburrell/quikagent.git
cd quikagent
go test ./...
go build -o quikagent ./cmd/quikagent
./quikagent -version
```

A local `go build` without a release tag prints `dev`. Release binaries
embed the version via GoReleaser (`-X main.version`).

## Next

Configure an API key and endpoint: [config.md](config.md), then
[usage.md](usage.md).
