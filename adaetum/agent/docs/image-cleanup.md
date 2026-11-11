# Reclaiming container image space

Status: **proposal.** Nothing here changes a server. The agent only reports image
usage, read-only. Numbers were measured on 2026-11-04 against the two production
RKE2 servers, using read-only commands.

## Problem

Container images are the largest consumer of disk on these servers, and most are
not in use:

| | node-a | node-b |
|---|---|---|
| Disk used | 158 GiB (67%) | 195 GiB (83%) |
| Unpacked images (kubelet's figure) | 87 GiB | 108 GiB |
| Compressed copies (containerd content store) | 30 GiB | not measured |
| Images no container uses | 190 of 226 (89% by size) | 122 of 237 (72% by size) |
| Estimated space they occupy | about 104 GiB | at least 78 GiB |

containerd stores every image twice: unpacked layers that containers run from, and
the compressed copies it downloaded. The kubelet's image figure counts only the
first. An earlier version of this document and of the storage view missed the
second, which made images look smaller and "system and other files" look larger
than they are. Only the server the agent runs on can measure its compressed
copies, so the second server's figure is a lower bound.

"Estimated space" applies the unused share (which the runtime reports in
compressed bytes) to the whole image footprint. It is an estimate, and the exact
figure is only learned by deleting something.

## "Unused" is not the same as "safe to delete"

An image no container uses on this server can still matter:

| Unused image category | node-a | node-b |
|---|---|---|
| **Failover cache**: needed by pods on the *other* server | 24% (about 25 GiB) | 4% (about 3 GiB) |
| **Unused cluster-wide**: no pod on either server needs it | 76% (about 79 GiB) | 96% (about 75 GiB) |

If node-b fails, its pods move to node-a, and the images they need are
the failover cache. Deleting them turns a fast restart into a download, or a
failure if the registry is unreachable. The recovery repository exists to work
during outages.

Where the removable images come from matters too, because deleting means
downloading again. `docker.io` rate-limits pulls. `ghcr.io` and
`registry.*.ts.net` need credentials or a running Tailscale network, which are
exactly what is missing in an outage. Many removable images are superseded
versions of one repository (`octo` has 14 stored versions).

## What already exists

Researched before building anything:

| Tool | Verdict |
|---|---|
| **Kubelet image garbage collection** (built in) | **Adopt.** Removes the oldest unused images when disk crosses a threshold, and never an image a container uses. This cluster's kubelet loads drop-ins from `/var/lib/rancher/rke2/agent/etc/kubelet.conf.d` (`--config-dir`), so no custom code is needed. |
| **RKE2 embedded registry mirror** (Spegel; GA since December 2024, off here) | **Decide.** Servers fetch images from each other. Helps when a registry is unreachable and a peer has the image. Does not help when the peer holding it is down, so with two servers it cannot replace the failover cache. Needs containerd to keep compressed copies. |
| **containerd `discard_unpacked_layers`** | **Decide.** Deletes the compressed copies after unpacking: about 30 GiB per server. Cannot be combined with the registry mirror. |
| **kube-image-keeper** | **Skip.** Version 1 is end-of-life. Version 2 rewrites image references through a webhook, which can block pod creation. |
| **A registry cache (Harbor, Zot)** | **Skip.** A heavy extra service for a small gain. |
| **Longhorn trim and snapshot-cleanup jobs** | **Later.** Apps hold about 61 GiB, so the gain is small today. |
| **Our own manual cleanup command** | **Built, narrowly.** See "The manual cleanup command" below. |

## Recommendation: a kubelet policy

Configure the kubelet's own cleanup with lower thresholds, as a declarative
setting. No new privileged code, no authentication problem, and the kubelet
already knows which images are in use.

Draft drop-in (a new file; it merges over `00-rke2-defaults.conf` by sort order):

```yaml
# /var/lib/rancher/rke2/agent/etc/kubelet.conf.d/10-adaetum-image-gc.conf
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
imageGCHighThresholdPercent: 75   # start cleaning above this disk usage (default 85)
imageGCLowThresholdPercent: 60    # clean until usage falls to this (default 80)
```

Proposed `platform.yaml` keys, so the policy has one public source of truth and the
existing Ansible host role renders the file:

```yaml
spec:
  imageCleanup:
    startAtDiskPercent: 75
    freeDownToDiskPercent: 60
```

What it would do today (estimates from the measurements above):

| | Disk now | Effect of 75% / 60% |
|---|---|---|
| node-b | 83% | Over 75%, so the kubelet cleans down toward 60%: about 54 GiB. Enough unused images exist to reach it. |
| node-a | 67% | Below 75%, so nothing happens until it grows. |
| With the 85% / 80% defaults | | node-b starts soon but frees only about 12 GiB. |

The kubelet decides by the whole disk's usage, which also includes Longhorn data and
the operating system, not by image size alone.

**Apply** the file at the next maintenance reboot (the existing 03:00 to 06:00
Kured window) instead of restarting RKE2 separately, since a kubelet reads its
configuration at start.

**Age limit left off.** `imageMaximumGCAge` (remove images unused for a set time)
exists on this cluster and went stable around Kubernetes 1.35, but its design notes
leave kubelet restarts unresolved, and a test suggests the clock restarts when the
kubelet does. Your servers reboot on a schedule, so a long limit may never fire.
Treat it as optional until tested.

**Limits to accept or fix:**
- The kubelet cleans oldest-unused first and does not know about failover cache or
  registries, so a low target can remove failover images too.
- It has not been tested on this cluster. Confirm the drop-in is honored on
  v1.35.6 on a non-production node before any rollout, then read the effect on
  disk and on pod restarts.

## Gaps this research found

- **No disk monitoring runs on this cluster.** The repository defines
  `kube-prometheus-stack`, but there is no Prometheus, Alertmanager, or
  node-exporter on the cluster, and its only two alert rules belong to other apps.
  Nothing alerts on a filling disk apart from this UI. Whether that is intended
  needs an answer; restoring it would add disk alerts (verify that the chart's
  default rules are enabled in `pods/observability/prometheus.app.yaml`).
- **Detection.** The agent now measures the compressed copies and counts them as
  container images, so the breakdown is accurate for the server the agent runs on.

## Decisions needed

1. **Policy numbers.** Are 75% and 60% the right starting point? A lower target
   frees more but shrinks the failover cache.
2. **Failover cache.** The kubelet policy cannot protect it. Accept that, or keep
   the target high enough that it usually survives?
3. **Registry mirror or discard compressed copies.** They are mutually exclusive.
   My lean is neither for now: apply the policy and measure.
4. **Monitoring.** Is the missing Prometheus stack intentional?

## Order

1. Done: measure compressed copies, so the numbers are right.
2. Test the kubelet drop-in on a non-production node; add `imageCleanup` to
   `platform.yaml` and the host Ansible role.
3. Apply at the maintenance window; watch real disk behavior for a few weeks.
4. Decide on the mirror versus discarding copies, and on monitoring.

## The manual cleanup command

Built, tested, and not yet run on a production server. It complements the policy:
the policy keeps disks from filling, and this command lets an operator reclaim a
known amount on purpose.

```
adaetum-agent images plan  [-ssh user@host] [-level aggressive|careful] [-o plan.json]
sudo adaetum-agent images apply -plan plan.json [-dry-run] [-max N]
```

- **`plan` is read-only** and works over the development SSH mode. It shows what
  could be removed and why every other image is kept. A plan is a preview, not an
  authorization: it expires in 10 minutes, and apply re-derives everything from
  live state.
- **`apply` runs only on the server itself, as root.** Mutation lives in its own
  package (`internal/cleanup`), which the read-only inspector and the SSH mode
  cannot reach. It asks the operator to type the server's name, defaults to at
  most 10 images per run, and holds a lock so two runs cannot overlap.

**What it will never remove** (each rule has tests, including the real-data
cases below):

1. An image any container on the server uses, running or stopped. Checked again
   immediately before each removal, so a container that starts mid-run is caught.
2. An image referenced anywhere in the cluster: pods on every server, workload
   templates (including ones scaled to zero), cron jobs, and **rollback history**
   (old ReplicaSets and ControllerRevisions). If the cluster view cannot be read,
   or comes back empty, there is no plan.
3. System images (everything under `docker.io/rancher/`, and the pause image).
4. Images from registries not on the allowed list (`docker.io`, `quay.io`,
   `registry.k8s.io`, `lscr.io` by default), because deleting them may leave
   recovery depending on a registry that is down.
5. Images with no name, and images from an app with no version in use, unless the
   operator asks with `-include-orphans`.
6. Anything not in the plan the operator reviewed.

**Safeguards while removing:** one image at a time by ID (never `crictl rmi
--prune`, which would take the failover cache with it); no audit trail means no
deletion (every step is written to `/var/log/adaetum/image-cleanup.log` before it
happens); it stops after three failures; Ctrl-C stops between images.

**What it found on the real servers (read-only plans, 2026-11-04).** A first
version did not read rollback history and proposed 15 to 17 images per server. An
independent cross-check against the live cluster found that most of them were
named by old ReplicaSets, which a rollback needs. The planner now protects them.
With every safeguard on, the default plan is small:

| Plan | node-a | node-b |
|---|---|---|
| Default (superseded versions, public registries) | 3 images, about 1 GiB on disk | 4 images, about 1 GiB |
| `-include-orphans` (apps with no version anywhere in the cluster) | 37 images, about 23 GiB | 44 images, about 27 GiB |
| plus `-allow-registry ghcr.io` | 57 images, about 42 GiB | 57 images, about 42 GiB |
| plus every registry, including `registry.*.ts.net` | 77 images, about 52 GiB | 77 images, about 60 GiB |

Disk figures scale the runtime's compressed sizes by about 2.5 and are estimates.
The orphan images belong to apps that are not deployed now (Plane, OnlyOffice, an
AMP game server, OpenSearch, Grafana, Prometheus), so a later deployment would
download them again from their public registries.

**Known gaps:**

- The planner matches images against the raw text of the cluster listing, so
  images named in environment variables (operators such as CDI and KubeVirt pass
  the images they launch later that way), arguments, and annotations are kept.
  An early version read only `image` fields and would have listed `cdi-importer`
  as removable under `-include-orphans`. Known virtual machine custom resources are
  read too when the cluster defines them. Other custom resources that name images
  in ways not covered here would still be invisible, which is why the default
  scope requires a sibling version in use.
- "Referenced by the cluster" is judged on this server's view of the API; a
  server that cannot reach it produces no plan.
- No image has ever been removed with it. On 2026-11-04 `apply -dry-run` was run on
  node-b for the default plan (4 images) and the `-include-orphans` plan (44
  images, `-max 0`): every image passed the live checks, nothing was removed (237
  images before and after), and the audit log recorded only `would-remove`. The
  first real run should be small: `-max` of a handful, then check.

## From the UI, and on a schedule

Workloads > Advanced > Cluster storage has a "Free up space" card: review and clean, schedule, and
history. It is a front end for the same plan and apply code as the command line.

**Who can delete.** A button that deletes needs authentication, and the agent has
none of its own yet, so deletion is enabled only when all of this holds, and the
UI says which condition is missing when it does not:

- the agent runs on the server itself (never in `-ssh` mode, which stays
  read-only even if flags and a token are supplied);
- it runs as root, on an RKE2 host;
- it was started with `-allow-changes` and a token file readable only by root
  (mode 600, at least 32 characters, made with `adaetum-agent token create`).

Previews and reads need no token, like the rest of the agent's read API. Applying,
cancelling, and saving a schedule need `Authorization: Bearer <token>`. The agent
never answers a cross-origin preflight, so a page on another site cannot drive it.
The browser keeps the token for the session only. The agent listens on loopback;
reach it with an SSH tunnel and do not bind it to a routable address, because it
speaks plain HTTP.

**The browser never chooses what is deleted.** "Remove" sends a plan ID (plus a
limit and a dry-run flag). The agent applies only a plan it made itself, once,
within 10 minutes, and the apply step re-checks every image against live state.

**Schedules are unattended deletion**, so they are narrower than a manual run:
the same plan rules, lock, and audit trail; at most N images per run (default 10);
an optional "only if a disk is over X% full" condition; a **check only** mode that
records what would be removed and deletes nothing; and a missed run (agent or
server down at the scheduled time) is skipped, not run hours late. The schedule is
stored on the server, in `/var/lib/adaetum/image-cleanup-schedule.json`, and run
by the agent. It is off by default, and defaults to the maintenance window
(04:00).

**History** is read from the audit log, so it cannot disagree with it. The audit
log records the trigger (command line, this page, or schedule), writes an intent
entry before each removal and a separate `removed` entry after it succeeds, and
labels a dry run as "would remove".

**Using it on a server today.** The UI is not yet served by the Go binary, so run
the agent on the server and tunnel to it from where the UI runs:

```bash
# on the server (as root)
adaetum-agent token create                      # writes /etc/adaetum/agent-token
adaetum-agent serve -allow-changes -token-file /etc/adaetum/agent-token

# on your machine
ssh -L 8080:127.0.0.1:8080 user@server
cd adaetum/agent/web && npm run dev                      # then open http://localhost:5173
```

## Levels: aggressive by default

Re-downloading an image is acceptable here, so the default level is **aggressive**:
the plan contains every image no container on this server uses. That includes
images another server runs (the failover cache), images kept for rollback, apps
scaled to zero, apps with no version running, and images from any registry. The
CLI, API, UI, and schedules all default to it; **careful** (the earlier behavior,
with its opt-in switches) is one choice away.

Two floors are kept at both levels, because they are about whether the server can
start and run, not about re-downloading:

1. An image any container on this server uses (checked again right before each
   removal).
2. System images: everything under `docker.io/rancher/`, the pause image, and any
   image containerd marks **pinned** (preloaded for RKE2, which containerd's own
   garbage collection also never removes).

Behavior that differs from careful: an aggressive plan still works when the API
server is down (it does not need the cluster view to be safe, and a full disk is
exactly when the cluster may be unhealthy), and says so in the plan. A careful plan
still refuses without it.

Measured on 2026-11-04 with read-only plans, an independent check against the
runtime found none of the planned images held by a container, pinned, or
system-named:

| Level | node-a | node-b |
|---|---|---|
| Careful (default registries) | 3 images, about 1 GiB of disk | 4 images, about 1 GiB |
| **Aggressive** | **173 images, about 90 GiB** | **115 images, about 75 GiB** |

The first real aggressive cleanup on a production server improved its free disk
space by roughly 45% (operator-reported, not a figure measured by this document).

## Decision needed: images served from inside the cluster

The bootstrap scripts pull the Ansible runner image from a Gitea registry that runs
inside the cluster (they special-case that registry for exactly this reason). An
aggressive plan lists old and "referenced" `ansible-runner` images from that
registry. Re-downloading is not possible when the cluster is the thing that is down,
which is the situation the runner exists for. Options: accept it; add a small keep
list (a repository pattern that is never planned, defaulting to the runner image);
or protect any image whose registry runs in-cluster. The first run of an aggressive
cleanup should wait on this choice.

## Proposal: share images between servers by default

RKE2 embeds Spegel, a peer-to-peer image mirror. Be precise about what it does.
**It does not deduplicate disk**: each server still keeps a full copy of the images
it runs, and containerd already stores each layer once per server. It shares
*downloads*: a server that needs an image fetches it from a neighbor over the
network instead of the internet, even when the registry is down. That is what makes
aggressive cleanup cheap to undo: a removed image comes back from a peer.

What the RKE2 documentation requires (checked 2026-11-04):

- `embedded-registry: true` in the config of **server** nodes.
- A `registries.yaml` on **every** node that lists the registries to mirror, for
  example `mirrors:` with `"*":` for all of them. A node with no entries does not
  take part.
- Nodes must reach each other on their **internal IPs** over TCP **5001** (peer
  network) and **9345** (registry). On this cluster those are Tailscale addresses
  (`100.x`), so the tailnet access rules must allow both ports between the servers.
- Not shared: the `latest` tag. Trust model: equal trust between all members, and
  images pulled with credentials can be served to peers that have none. Mirror by
  digest where possible.
- containerd must keep the compressed layer copies (`discard_unpacked_layers` off),
  which is the current state and is why that 30 GiB per server is not reclaimed.

What the documentation does not say, and must be tested on one server first: whether
RKE2 needs a restart to turn it on, how a two-server cluster behaves, and what
happens to a pull when the peer holding the image is down.

Limits to be honest about: with two servers, if the only server holding an image
fails, its image is gone until the registry is reachable again. Sharing makes
aggressive cleanup much safer while both servers are up; it does not replace the
registry.

Where it would be configured: RKE2's server config already uses drop-ins
(`/etc/rancher/rke2/config.yaml.d/10-adaetum-single-node-pods.yaml`), so sharing is
a second drop-in. `/etc/rancher/rke2/registries.yaml` is already managed by the
bootstrap phase scripts (with a marker, and a rule that an unmanaged file is never
overwritten), so enabling sharing means merging into that file, not replacing it.
Applying it to the live cluster means a restart, to be done in the maintenance
window.
