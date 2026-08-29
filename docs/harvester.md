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

Long-running agent host (loopback only):

```sh
quikagent --web 127.0.0.1:8080
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
commit secrets). Clone this repository onto the VM with a deploy key or
HTTPS.

Print-mode automation should pass `-yes` when auto-approval is required
(`quikagent -yes -p "..."`).

## Next

[hosting.md](hosting.md) · [install.md](install.md)
