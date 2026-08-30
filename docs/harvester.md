# Lab VM (Harvester)

Manifests under [deploy/harvester/](../deploy/harvester/) are a starting
point for a lab VM (8 CPU / 32 GiB, Ubuntu cloud image) on Harvester.
Replace every `REPLACE_WITH_*` placeholder (image ID, storage class,
VLAN/network, SSH public key) with values from **your** cluster before
apply.

```sh
export KUBECONFIG=/path/to/harvester.kubeconfig
kubectl apply -f deploy/harvester/quikagent-lab-cloudinit.yaml
kubectl apply -f deploy/harvester/quikagent-lab.yaml
```

Cloud-init grants passwordless sudo for the lab `ubuntu` user
(intentional for this image). Do not commit a real SSH public key;
leave `REPLACE_WITH_SSH_PUBKEY` in git.

Cloud-init installs Go, tmux, and `~/.quikagent`. It does **not**
install or build quikagent, clone this repo, or seed an API key. Do
those by hand after SSH.

Long-running agent host (loopback only). Pin the sandbox with
`--workdir` so the unit does not need a new `WorkingDirectory=` when
the lab repo changes:

```sh
quikagent --workdir /home/ubuntu/src/modelmove --web 127.0.0.1:8080
```

Remote access from a laptop or phone is documented in
[hosting.md](hosting.md) (SSH, Tailscale, or Cloudflared). The SSH
pattern for this VM:

```sh
ssh -L 8080:127.0.0.1:8080 ubuntu@<vm-ip>
# then open http://127.0.0.1:8080
```

Do not bind the web UI on the LAN. Interactive TUI soaks: SSH in and
run under `tmux`. Seed `~/.quikagent/config.yaml` on the VM (never
commit secrets).

GitHub write from the lab:

- Use **HTTPS remotes** plus `gh auth git-credential`. Pin
  `gh config set git_protocol https` — it can drift back to SSH.
- A deploy key on `shaneburrell/quikagent` cannot push
  `modelmove` or the Homebrew tap. Those remotes must be HTTPS with
  the `gh` token (`repo` scope).
- `permissions.deny: ["bash(rm *)"]` blocks `rm -rf /tmp/...`. Reuse a
  directory or `mkdir` a new one; do not weaken deny-wins.

Print-mode automation should pass `-yes` when auto-approval is required
(`quikagent -yes -p "..."`). `--timeout` (default 20m) and
`--max-steps` apply to `-p`. **`-yes` does not apply to `--web`** —
unattended lab work stays print mode.

## Next

[hosting.md](hosting.md) · [install.md](install.md)
