# Diagram source: first boot

Source notes for an animated diagram of how a blank machine becomes a one-node
cluster, **as it works today**. The first-boot design is being reworked (see
`docs/firstboot-rework.md`), so this describes the current behavior, checked against
the kickstart fragments in `ks-src/`, `ansible/ansible-scripts/`, and the logs of a
real install. It is meant to be updated as phases are rebuilt.

**One sentence:** the installer ISO puts a minimal system on disk; on first boot the
machine joins the tailnet, downloads the bootstrap bundle from the Worker, and runs a
chain of phases that builds the cluster, moves its secrets into OpenBao, hands control
to Git, and then destroys its temporary authority.

## Actors

| Actor | What it is |
|---|---|
| **Installer ISO** | Rocky Linux installer plus the kickstart, the pinned host agent, the start/join detector and a console helper (Gum). |
| **Kickstart** | The unattended install script that partitions, installs packages, and writes the first-boot services. |
| **`tailscale-firstboot.service`** | A one-shot systemd service. Runs the host-readiness steps and then the bundle bootstrap. Skipped once `/var/lib/tailscale-firstboot.done` exists. |
| **Worker + R2** | Serves the runtime environment file and the bundle, gated by the shared token. Also receives uploaded logs. |
| **Tailscale** | The tailnet the node joins and the source of the "start or join" decision. |
| **Host agent** | `adaetum-agent serve`, enabled by the kickstart, running from the first boot. |
| **Bootstrap phases** | The shell scripts and one Ansible playbook, run in order. |
| **The cluster** | RKE2, then the platform, then OpenBao, Argo CD and Gitea. |

## Order

### A. Install (before the first reboot)

1. The ISO boots and the kickstart runs. It prepares storage, partitions, and
   installs packages.
2. In `%post` (installer context) it copies three things from the install media onto
   the new system: the **host agent** binary to `/usr/local/bin/adaetum-agent`, the
   **start/join detector**, and the **Gum** console helper. It fails the install
   if any is missing.
3. In `%post` it writes the first-boot configuration and services:
   `/etc/tailscale-firstboot.env` (mode 0600; cluster names, the runtime env URL, the
   shared token, Tailscale OAuth client id and tags), the
   `tailscale-firstboot.service` and `ansible-bundle-bootstrap.service` units, the
   log-upload timer, and the **host agent** unit.
4. It creates the host agent's **access token** once
   (`/etc/adaetum/agent-token`, never rotated on a rerun) and enables the agent
   service. It writes a console banner showing where the agent answers.
5. The install finishes and the machine reboots into the new system. The design
   assumes the install media is ejected by then and never used again.

### B. Host readiness (first boot, `tailscale-firstboot`)

In this order, each reported as a step in the log:

1. **Fetch the runtime payload.** `bootstrap-runtime.env` is downloaded from the
   Worker with the shared token and saved to `/etc/bootstrap-runtime.env`. It is
   validated as plain `KEY=value` lines. This is where run-time secrets arrive.
2. **static-network.** Applies static network settings when they were provided.
3. **clock-readiness.** Waits for the clock to synchronize. TLS depends on it.
4. **network-readiness.** Waits for a default route.
5. **tailscaled-start.** Starts the Tailscale daemon.
6. **debug-user.** Creates a debug account only if one is configured.
7. **tailscale-up.** Requests an OAuth access token, creates a **one-time auth key**
   (valid one hour) carrying the node's tags, and joins the tailnet.
8. **Start or join.** The detector reads the tailnet's peers. If another peer
   already has both `tag:cluster` and `tag:server`, this node will **join**; if not,
   it will **start** the cluster. An offline peer still counts.

### C. Bundle and tooling

1. **Download the bundle.** `ansible-runner-bundle.tar.gz` comes from the Worker/R2
   with the shared token and is extracted to `/opt/ansible-runner`. This is the
   bootstrap code; the ISO does not contain it.
2. `bundle-bootstrap` installs `ansible-core` if needed, builds the Ansible
   inventory, and installs `task` and `kubectl` through Homebrew if needed.

### D. The phases

Each phase writes a "done" marker so a rerun can skip it. Logs go to
`/var/log/bootstrap-phases/` and are uploaded to R2.

| Phase | What it does |
|---|---|
| **10** | Read-only check of the profile and runtime payload. Creates no state. |
| **20** | Creates the temporary local secrets in `/var/lib/bootstrap-secrets` (mode 0700). |
| **30** | Runs `platform-bootstrap.yml`: RKE2, the network plugin, storage (Longhorn), certificates, Rancher, ingress, the public tunnel. |
| **40** | Waits for storage, initializes OpenBao, sets its restart auto-unseal, and writes the surviving secrets into it. |
| **50** | Installs the minimal Argo CD and Gitea. |
| **60** | Seeds the cluster repository into Gitea, points Argo CD at it, pins the ingress VIP, and waits for Gitea to take over. |
| **70** | Proves GitOps works: the runner image is in the registry, the app-of-apps exists, and critical apps settle. |
| **90** | Finishes credentials that need running apps and writes them back to OpenBao. |
| **99** | Exports the recovery kit, then destroys the local bootstrap secrets. Skipped on joining nodes. |

### E. Finish

On success the first-boot service removes the runtime env file and the rendered
first-boot scripts and installer kickstart copies (they can contain the shared
token), then writes `/var/lib/tailscale-firstboot.done`. On failure it **keeps**
those artifacts so a resume is possible, and uploads the logs.

## Secret authority over time

| Moment | Authoritative store |
|---|---|
| Before Phase 20 | The runtime env file (delivered by the Worker). |
| Phase 20 to 40 | The local directory `/var/lib/bootstrap-secrets`. |
| Phase 40 onward | OpenBao. |
| After Phase 99 | OpenBao only, plus the exported recovery kit. |

## Handoff barriers

- **Install media to disk.** After `%post`, the media is not used again. Everything
  later comes over the network.
- **Anaconda to systemd.** The kickstart does not run bootstrap. It writes units
  that run on the first real boot.
- **ISO to Worker/R2.** The ISO carries only identifiers and the shared token; the
  runtime env and the bundle are fetched. A fix to the bundle ships without a new
  ISO.
- **Host readiness to bootstrap.** Tailscale joined and start/join decided. A node
  that chooses "join" with no live server to join fails here with a clear message.
- **Local files to OpenBao** (Phase 40). The one authority transition.
- **Bootstrap to GitOps** (Phase 60). Argo CD now follows the Gitea repository.
- **Configured to proven** (Phase 70). Nothing continues on configuration alone.
- **Ladder removed** (Phase 99). Bootstrap-local authority is destroyed only after
  the recovery export succeeds.

## Security barriers

- **Shared token gate.** The Worker serves the runtime env and bundle only to
  requests carrying the shared token. Logs print the URL with the token redacted.
- **Root-only files.** `/etc/tailscale-firstboot.env` is mode 0600; the local
  secret directory is mode 0700.
- **Short-lived node credential.** A node joins the tailnet with a one-time auth
  key that lasts one hour and carries only the node's tags, minted from a scoped
  OAuth client.
- **Cleanup after success.** The env files, rendered scripts and kickstart copies
  that could hold the shared token are deleted; they are kept only after a failure,
  for resume.
- **Local secrets die.** Phase 99 removes `/var/lib/bootstrap-secrets` only after
  the encrypted recovery export succeeds.
- **Host agent.** It listens beyond loopback only with its access token and a
  self-signed HTTPS certificate. It does not run commands on servers; changes go
  through Ansible (see `agent-host-interaction.md`).
- **Joining nodes** do not run Phase 99, because they never created the bootstrap
  authority.

## Failure and retry today

Each phase can be skipped on a rerun if its done marker exists. In practice resuming
has not worked reliably, so a failed install has meant rebuilding the node, which is
one of the reasons for the rework. The failure reason is in the per-phase log and
the uploaded copy (`task fetch-logs`).

## Animation notes

Suggested frames: ISO boots, disk fills with five items; reboot; the node reaches
out to the Worker (token shown as a key that opens a door); the clock and network
check; Tailscale join with a one-hour key; the start/join fork; bundle download;
nine phases as a ladder where the secret-authority label moves from "files" to
"OpenBao"; the last rung is removed. Keep "today" and "to be reworked" visibly
separate, because the order is expected to change.
