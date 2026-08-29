# Install

## Homebrew

```sh
brew install shaneburrell/tap/quikagent
quikagent -version
brew upgrade quikagent
```

The formula tracks [GitHub Releases](https://github.com/shaneburrell/quikagent/releases)
(darwin and linux, amd64 and arm64).

## Release tarball

Each `v*` tag publishes archives via GoReleaser (`quikagent_<version>_<os>_<arch>.tar.gz`
plus `checksums.txt`). Download, verify the checksum, unpack, and put
`quikagent` on your `PATH`.

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

Upgrade a source install with `git pull` and `go build` again.

## After install

Interactive first run opens setup (`/setup`) for API key, base URL, and
model. Values go to `~/.quikagent/config.yaml` (mode `0600`).

**Print (`-p`) and `--web` do not open setup.** Set `QUIKAGENT_API_KEY`
or create the config file first:

```sh
export QUIKAGENT_API_KEY=...
quikagent -p "say hi"
quikagent --web 127.0.0.1:8080
```

Any OpenAI-compatible endpoint works (BYOK). Defaults: [config.md](config.md).

## Next

[config.md](config.md) · [usage.md](usage.md) · [hosting.md](hosting.md)
