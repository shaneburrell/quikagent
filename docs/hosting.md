# Hosting and remote access

quikagent is a **single-user** process. The web UI has **no login**. Anyone
who can reach the port can run tools (including `bash`) and see the
session.

**Supported remote access:** bind loopback, then reach it with **SSH**,
**Tailscale**, or **Cloudflared**. Do not treat `--web-listen-all` as a
hosted path.

Print (`-p`) and `--web` need an API key via `QUIKAGENT_API_KEY` or an
existing `~/.quikagent/config.yaml`. They do not open the TUI setup
screen. Configure once interactively, or set env vars, then start `--web`.

## Threat model

1. Run the UI on `127.0.0.1` only.
2. Put identity and TLS **in front** of quikagent (SSH, Tailscale, or
   Cloudflare Tunnel — optionally Cloudflare Access).
3. Seed secrets on the host; never commit `~/.quikagent/config.yaml`.

`--web-listen-all` exists so a lab can bind `0.0.0.0`. That exposes an
unauthenticated coding agent on the network. It is not a supported
hosted configuration.

Web UI behavior and limits: [web-ui.md](web-ui.md).
Lab VM manifests: [harvester.md](harvester.md).

## Start on the host

Workdir is `--workdir` when set, otherwise the process current
directory (`os.Getwd()`). Pass `--workdir` so a systemd unit can
switch repos without rewriting `WorkingDirectory=`:

```sh
export QUIKAGENT_API_KEY=...   # or use ~/.quikagent/config.yaml
quikagent --workdir /path/to/repo --web 127.0.0.1:8080
# same machine only: quikagent --desktop
```

Keep the process alive with `tmux` or a systemd unit. A dedicated
`quikagent serve` daemon is specified in [design/serve.md](design/serve.md)
and is not in the binary yet.

Example unit (loopback only):

```ini
[Unit]
Description=quikagent web UI (loopback)
After=network.target

[Service]
Type=simple
User=ubuntu
WorkingDirectory=/home/ubuntu
Environment=QUIKAGENT_API_KEY=...
ExecStart=/usr/local/bin/quikagent --workdir /home/ubuntu/src/your-repo --web 127.0.0.1:8080
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

Prefer env for the key on a shared disk. `GET /health` returns `ok` and
does not check the LLM or disk.

## SSH

Lowest-dependency path. Already used on the Harvester lab.

On the host:

```sh
quikagent --web 127.0.0.1:8080
```

On your laptop:

```sh
ssh -L 8080:127.0.0.1:8080 user@host
```

Open `http://127.0.0.1:8080` locally. Traffic stays inside the SSH
tunnel.

## Tailscale

Always-on mesh from a phone or laptop on the same tailnet.

1. Install Tailscale on the host and log in.
2. Keep quikagent on loopback:

```sh
quikagent --web 127.0.0.1:8080
```

3. Proxy with Tailscale Serve (preferred — does not publish to the
   public internet):

```sh
sudo tailscale serve --bg http://127.0.0.1:8080
```

Open the Serve URL Tailscale prints (HTTPS on your tailnet).

**Do not** use Tailscale Funnel as the default. Funnel publishes the
unauthenticated UI to the public internet.

Binding `--web` to a Tailscale IP still leaves the HTTP server without
auth. Prefer `tailscale serve` in front of loopback.

## Cloudflared

HTTPS hostname without opening a port on the host firewall.

1. Install `cloudflared` and create a tunnel (Cloudflare Zero Trust).
2. Point the tunnel at loopback:

```yaml
# example config.yml fragment
ingress:
  - hostname: quikagent.example.com
    service: http://127.0.0.1:8080
  - service: http_status:404
```

3. Run quikagent on the host:

```sh
quikagent --web 127.0.0.1:8080
```

4. Run the tunnel:

```sh
cloudflared tunnel run
```

The hostname is public unless you add **Cloudflare Access** (email/SSO)
in the Cloudflare dashboard. Access stays outside quikagent. A tunnel
with no Access policy is an unauthenticated coding agent on the
internet — do not do that.

## What this does not do

- No in-process password, API token, or mTLS ([REVIEW_DISPOSITIONS.md](REVIEW_DISPOSITIONS.md)).
- No multi-user isolation. One OS user, one workdir, one live turn.
- `-yes` does not apply to `--web`. Mutating tools prompt in the browser
  unless you sit at the UI.
- Closing the browser does not stop an in-flight turn, but reconnect
  does not replay a pending approval or question. Refresh during
  Allow/Deny can leave the turn stuck until you cancel. Details:
  [web-ui.md](web-ui.md).

## Next

[web-ui.md](web-ui.md) · [usage.md](usage.md) · [config.md](config.md) ·
[design/serve.md](design/serve.md) (proposed daemon)
