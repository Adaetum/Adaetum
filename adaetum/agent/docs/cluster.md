# Cluster: host agents and the cluster agent

Status: phase 1 is built (the host's local database and check-ins). Everything
after it is a design, recorded so the decisions are not accidents.

## Shape

- **Host agent**: the Go binary installed on every host, outside Kubernetes. It
  works alone, on one node, before a cluster exists. Its database is a local Turso
  file (`internal/store`) that holds its events and is its source of truth.
- **Cluster agent**: the same binary, run as a pod once Kubernetes is up and there
  are two or more nodes. It is the cluster's single endpoint: it collects events from
  the host agents, keeps the cluster view the dashboard reads, and later hosts
  cluster-wide work.
- **Check-in**: every host agent records an `agent.checkin` event (hostname,
  version) at start and every five minutes. The cluster agent compares the host IDs
  that have checked in with the servers Kubernetes lists, and the dashboard shows
  the servers **not running the agent**, and those running an out-of-date one, so they can be fixed. Detection only for
  now; installing the agent on a missing host stays with Ansible.

## Both installs are required

The host agent exists so that troubleshooting and direct host actions still work
when Kubernetes is down, so it is never a client of the cluster agent:

- **Independent.** Every feature a host agent offers works with the cluster agent
  unreachable. Sending events upward is best-effort and never blocks anything.
- **Directly reachable, out of band.** The host agent can listen on every
  interface (`serve -listen 0.0.0.0:8443`), so an operator reaches any host on the
  local network by IP address or hostname with no cluster, tailnet, or internet
  involved. Listening beyond loopback is refused unless both protections are on:
  every request except `/healthz` needs the access token (`-token-file`, mode 600),
  and the agent serves HTTPS with a certificate it makes for itself
  (`internal/tlscert`), covering the host's names and all its addresses and renewed
  when they change. The startup log prints the certificate's SHA-256 fingerprint to
  check on first visit; pin it with `curl --cacert /var/lib/adaetum/tls/agent.crt`.
  Which networks can reach the port is the host firewall's job, not the agent's.
  Not built yet: the Ansible role that opens the port in the right firewall zone,
  and serving the web UI from the agent (it needs a login screen), so today this
  path serves the API and MCP, not the pages.
- **Kept current.** The agent binary is installed and upgraded on hosts by Ansible
  (the `image-cleanup` role already takes the binary as an input). The check-in
  carries the version, so the cluster agent can flag hosts that are behind, not
  only hosts with no agent.
- **The cluster agent is the front door, not the only door.** It aggregates the
  cluster and links to or proxies each host's own page for host-level actions; the
  host agent stays the authority for anything on its machine.

## Why events, and one writer per row

Hosts and the cluster agent disagree about the world for periods of time (a node
is down, the cluster is still booting). Eventual consistency is only easy when
nothing needs merging, so:

- an event is **append-only and owned by one host**; nobody edits it, and nobody
  else writes it;
- an event's identity is `hostID:seq`, so delivering it twice is harmless and the
  receiver ignores repeats;
- delivery progress is one cursor per host (the highest sequence acknowledged);
  everything above it is resent until acknowledged.

The cluster agent's dashboard state is derived from events. It is not a second
place to edit host facts.

## What Turso gives us, checked 2026-11-04

- **Local engine without cgo.** `turso.tech/database/tursogo` is a `database/sql`
  driver; it embeds its native library and unpacks it at run time into
  `TURSO_GO_CACHE_DIR` (default: the user cache directory). Under systemd, set
  `TURSO_GO_CACHE_DIR=/var/lib/adaetum/cache`. It needs Go 1.24 or newer, and the
  agent is no longer a single fully static binary.
- **Sync is last-push-wins.** Turso's own push/pull sync resolves conflicts by
  letting the last push win, and the first run bootstraps the local file *from the
  remote*, so the remote must be reachable then (`BootstrapIfEmpty` can turn that
  off). Both argue against using it for host facts. A running central sync server
  is documented only as a local test tool.
- So the recommended transport is the agent's own: the host POSTs its pending
  events to the cluster agent's endpoint and advances its cursor on the reply.
  Turso is the storage engine on both sides; no Turso Cloud account is needed.

## "The hosts elect a master"

Hosts do not need to elect anyone: the cluster agent is a pod, so Kubernetes keeps
it running and reschedules it if its node fails. What does need coordination is
more than one replica of that pod. Use a Kubernetes `Lease` so exactly one replica
writes at a time; the others stay warm. Until a second replica exists, one pod with
its database on a persistent volume is enough. If this is not what you meant by
"elect", this section is the one to change.

## Phases

1. **Done.** Local database, event outbox with cursor, host ID, check-ins
   (`serve -data-dir`). Not yet verified on a Rocky host (see below).
2. Cluster agent endpoint: ingest events idempotently, store them in its own
   database, answer with the acknowledged cursor. Host side: the sender loop, which
   treats an unreachable cluster as normal and never blocks anything.
3. Cluster view: compare check-ins with Kubernetes nodes; a "servers without the
   agent" card and per-server "last seen" on the dashboard.
4. Cluster agent as a pod (image, manifest in `pods/`, volume, `Lease`), installed
   by the existing delivery path.
5. Work on top: the Ansible jobs engine, policy, and the embedding queue described
   in the README.

## Adopting a host automatically

A host that is a cluster node joins the cluster agent with no token and nothing to type.
Kubernetes is the notary:

1. The cluster agent publishes its address and certificate fingerprint in the ConfigMap
   `adaetum/adaetum-cluster-agent-endpoint`. Neither is secret.
2. A host agent with no link, on a machine that has RKE2's credentials, reads that ConfigMap,
   and publishes its own public key as the Node annotation `adaetum.io/host-key` on its own
   Node. Only that node, or a cluster administrator, can write it.
3. It enrols without a token. The cluster agent approves it only if the Node it names carries
   the very key it presented. Naming a node is not enough, which is why a stranger cannot
   claim `node-12484`: they cannot put their key on that Node.
4. The host saves the link and exits with status 75 so its service restarts and starts sending
   and copying accounts (they read the link once, at startup).

A reinstalled node comes back with a new key under the same name and republishes it; because
Kubernetes approved the old key, Kubernetes' word replaces it. A host a person approved by hand
is never replaced this way. A machine Kubernetes does not list is untouched and still joins with
`adaetum-agent join` and a join token. Hosts that are not the first node (workers) can use the
same path where the node's own credentials exist; otherwise they use a token.

## Installing and updating the agent remotely

Ansible over SSH already has administrator rights on every server, so it is also how
the agent gets onto a server and how it is updated. One playbook does both
(`ansible/playbooks/host-agent.yml`, role `host-agent`): install when there is no
agent, upgrade when the binary changed, repair when something drifted. It is safe to
run again.

- **Detect.** The cluster agent compares Kubernetes' servers with the check-ins. A
  server with no agent is *missing*; one running an older version than the cluster
  agent itself is *outdated*.
- **Plan.** `membership.PlanInstall` turns that into "install here, upgrade there". It
  only ever plans servers the cluster itself lists, never a machine that merely
  checked in from outside, and it leaves quiet servers alone (the machine may simply
  be off; look first, don't reinstall). The Cluster page shows the plan and the exact
  command, ending in `--check` so it can be rehearsed.
- **Do.** The Cluster page's "Open in Automation" fills in the servers; a person
  rehearses (`--check`) and then runs the playbook from the page. The agent does not
  push anything: it **actuates Ansible** (`internal/ansiblerun`), which does the work
  over SSH. See "Launching Ansible from the page" below.

Because this runs a program as root on every server, it is deliberately narrow: one
server at a time (`serial: 1`), the binary must come from a checked source (a
controller file, or a URL with a required SHA-256), the access token is created once
and never rotated by a re-run, and the join token is single use, kept out of
Ansible's output and off the command line. Binary signing is not built; until it is,
the checksum is the integrity check.

## Launching Ansible from the page

The pattern for anything that changes servers: **Ansible does the work; the agent
launches it.** The page lists a fixed catalog of playbooks (`ansiblerun.Catalog`),
shows a form for the variables each declares, and the agent runs `ansible-playbook`
with them and streams the output back.

What keeps it safe, since it runs Ansible as an administrator:

- only catalogued playbooks and their declared variables, each checked against a
  pattern; no free-form file, path, or variable (`ansible_become_pass` and friends
  are refused);
- servers must be named exactly (no `all`, no patterns), at most 50, and the
  install playbooks run `serial: 1`;
- rehearsal (`--check --diff`) is the default; a real run asks for confirmation;
- secrets (the join token) reach Ansible in a private file, never the command line,
  and are scrubbed from the output shown and recorded;
- one run at a time, every launch recorded before it starts (a run that cannot be
  recorded is refused) in a private JSON-lines file, with who, what, and the result;
- off unless started with `-allow-ansible`, with Ansible installed, a token, and a
  writable `-ansible-audit`; never over `-ssh`.

`serve -allow-ansible` and `cluster -allow-ansible` both offer it; the cluster agent
is the natural home. Where Ansible runs is the `Executor`: `LocalExec` runs
`ansible-playbook` on the agent's machine (works now); `KubernetesJob`, which would
run it as a Job from the existing runner's pod template, is a stub. Adding a
playbook to the catalog is a deliberate code change, reviewed with the playbook.

## What is built and what is not

Built and tested: the host database and check-ins, `membership.Assess` (missing, stale,
outdated servers), the host sender, enrollment with join tokens, signed
machine-to-machine requests, the cluster agent role (`adaetum-agent cluster`:
enroll, approve, receive events, cluster view) and `adaetum-agent join`. The web
interface is embedded in the binary with a sign-in screen and a Cluster page (servers
without the agent, machines waiting for approval, and making join tokens). Trust is described in
[auth](auth.md). Run end to end against a real cluster in development with
`cluster -ssh`, which reads the node list read-only.

Not built (search the code for `STUB` or `INTENT`):

| What | Where |
|---|---|
| The cluster agent as a pod (image, manifest in `pods/`, volume, `Lease`) | `cmd/adaetum-agent/cluster.go` |
| Listing nodes from inside the cluster (service account) | `nodeLister` in the same file |
| Cluster-to-host actions, per-person accounts, revocation | [auth](auth.md) |
| Running playbooks as Kubernetes Jobs instead of on the agent's machine | `internal/ansiblerun/kubejob.go` |
| Condition triggers: deciding *when* to run a playbook, not only on demand | `internal/automation` package comment |
| Signed agent binaries (today only a SHA-256 is checked) | `ansible/automation-roles/host-agent` |
| An agent-version and install policy in `platform.yaml` | not started |

## Not verified yet

The native Turso library has been run on macOS only. It must be run once on a Rocky
Linux 10 host (glibc, SELinux, `TURSO_GO_CACHE_DIR` writable by the service) before
the agent is installed anywhere with `-data-dir`.

## Updates

Cluster > "Adaetum updates" sets the release channel (stable or beta) and how updates
arrive: **manual** (only the Check button), **notify** (the agent checks every six hours
and shows when an update is available), or **automatic proposals** (as notify, plus it
opens the GitOps pull request once per release). Every mode stops at a pull request that
a person reviews and merges; hosts are still updated through the rehearsal-first
`host-agent` playbook. The choice is the cluster agent's own runtime setting, kept in
`<data-dir>/update-settings.json` (not in `platform.yaml`, which only renders into
manifests); changes and proposals are recorded in `release-proposals.log`. Automatic
proposals need the same GitOps access as the manual button.
