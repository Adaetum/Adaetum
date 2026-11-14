# Adaetum host agent

A single Go binary that runs directly on a host, outside Kubernetes, so it keeps
working while RKE2 is down. Over time it becomes the management agent and API
for that host and the backend for a portal/dashboard. Today it does one thing:
report what is on a host, read-only.

This directory is deliberately separate from `ansible/` and `pods/`. It owns
host-level inspection and, later, host adoption. It does not own cluster
workloads (`pods/`), public configuration (`platform.yaml`), or secrets
(OpenBao).

## Current scope (slice 1)

- `adaetum-agent inspect` prints a JSON report covering:
  - the host: OS, kernel, failed systemd units, resources (uptime, CPU load,
    memory, disks), and, on RKE2 hosts, how many container images are unused
    and how much disk their compressed copies take (only the machine the agent
    runs on can answer these);
  - RKE2: services, version, nodes (roles, version, pressure, cordon), workloads,
    pods, Argo CD applications, storage claims (with Longhorn health), cert-manager
    certificates, ingresses, and recent Warning events.

  Metrics are a point-in-time snapshot; rates such as CPU percent need history
  and wait for the local database. Cluster queries run concurrently over one
  shared SSH connection.
- `adaetum-agent serve` exposes the same report at `GET /api/v1/inspect` and
  `GET /healthz`.
- `adaetum-agent mcp` serves the same read-only capabilities to AI assistants and
  other [Model Context Protocol](https://modelcontextprotocol.io) clients over
  stdio (see [MCP](#mcp-model-context-protocol)).
- Everything is read-only. The only programs inspection may execute are the
  fixed allowlist in `internal/inspect/inspect.go`.
- `-ssh user@host` makes either command inspect another machine over SSH.
- `serve` listens on `127.0.0.1:8080` by default (plain HTTP; reach it through an
  SSH tunnel). With `-listen 0.0.0.0:28443 -token-file /etc/adaetum/agent-token` it
  is reachable from the local network over HTTPS with a self-signed certificate,
  and every request but `/healthz` needs the token. It refuses to listen beyond
  loopback without a valid token file, and never in `-ssh` mode. The web
  interface is built into the binary (`npm run build` in `web/`, then rebuild)
  with a sign-in screen. See [cluster](docs/cluster.md) and [auth](docs/auth.md).
- The cluster agent can also run an **assistant** (a Claude conversation in a drawer on the right
  of every page, opened from the sidebar or by any "Fix with assistant" button, that
  can do what the page can do, asking before it changes anything; the thread is
  [assistant-ui](https://github.com/assistant-ui/assistant-ui) over the agent's own API,
  in `web/src/components/assistant/`) and keeps **memory**
  it can recall by meaning, with the embedding model running as its own pod
  ([`embed/`](../embed/README.md)) that you choose and test under Settings > Embedding. All of it is described
  in [memory and the assistant](docs/memory.md);
  the assistant is off until someone picks an AI service (Anthropic, or a free OpenAI-compatible one for testing) and switches it on, under Settings > AI model or with `-enable-assistant`.
- `join` and `cluster` are the host and cluster halves of the cluster: a host
  enrolls with a one-time token and a pinned certificate, then sends signed
  events to the cluster agent, which shows which servers are not running the agent
  and the playbook that installs or updates it (`ansible/playbooks/host-agent.yml`).
- `serve -allow-ansible` (and `cluster -allow-ansible`) lets the Automation page
  launch catalogued playbooks on demand, with a form for their variables, a
  rehearsal mode, live output, and a record of every launch. The agent only
  actuates Ansible; it never pushes anything itself. Needs Ansible, a token, and a
  writable `-ansible-audit`; see [cluster](docs/cluster.md). The security rule behind it:
  the agent does not run commands on servers, it launches Ansible; running commands
  over SSH is an explicit, off-by-default override on the cluster agent only, with a
  switch under Settings > Security (time-limited, confirmed, recorded). See
  [security boundary](docs/security-boundary.md).

  The use case the whole design must serve is **recovery login**: with the cluster down,
  a person signs in on a host with their own account and repairs it. See
  [recovery login](docs/recovery-login.md). People sign in with their own accounts when the
  agent is started with `-auth-socket` and `-auth-secret-file` (the auth service is in
  [`auth/`](../auth/README.md)); the access token keeps working beside it and alone without it.

The purpose of this slice is to validate the report contract against a real,
already-running RKE2 host before building anything on top of it.

## Web UI (development)

`web/` is a React + TypeScript (Vite) UI built on the
[Radix Themes](https://www.radix-ui.com/themes) design system, with TanStack
Table for data tables and Lucide icons. Fonts are bundled so the UI works on
appliances without internet access. It is read-only and organized as:

- **Overview**: overall health, an ordered list of what needs attention, status
  tiles, resources, and servers.
- **Workloads**: one card per app, each opening to its own page (see below). Cluster-wide
  disk capacity, image cleanup, every deployment, recent warnings, security certificates and
  every web address sit under its Advanced section.
- **Cluster**: which servers run the agent, plus a "This server" section for the machine
  this agent reports on (shown even when the cluster view can't be read).
  Pages are linkable (`#/workloads?problems=1`), and light, dark, and system themes are
  supported.
- **Find apps**: searches public Helm charts (Artifact Hub) and container images
  (Docker Hub) and shows who publishes each and a rough starting-point draft.
  Converting an app to the repo's own `pods/` layout is a separate step. It installs
  nothing. The assistant uses the same two searches (`search_apps`,
  `preview_app_install`). Only those two fixed hosts are contacted, and every
  result is treated as untrusted text (`internal/catalog`). The same package serves
  app logos for the cards on the Apps and Find apps pages. The agent fetches them
  (never the browser): first from the community dashboard-icons set (keyed by exact app name), then from Artifact Hub when a well-used chart has
  exactly that name (or, for a Helm app, the very chart Argo CD deploys it from), then Docker Hub (a logo a
  verified, official or well-used image of that name has uploaded), then GitHub: the
  avatar of the organisation behind a well-known repository named exactly like the app
  (never a person's avatar). Only
  small images of known types are passed on, and an app with a generic name gets a
  letter instead of someone else's logo. The cluster agent keeps what it found, and
  what it found missing, in its database, so a restart doesn't re-ask. The Workloads page
  shows each app on its own, leaves the platform's own apps out unless one needs
  attention, and lists which namespaces are system or broken out in `web/src/lib/apps.ts`.
  Each card also shows a setup score as a four-axis shape with four section scores:
  Resources (requests and limits set and sensible), Fit (against live metrics-server
  usage), Scaling (judged only where an autoscaler exists) and Storage (are its volumes
  the right size: how full, how fast they are filling, and oversized ones; see
  `web/src/lib/storageSizing.ts`). Missing requests or limits cost
  points, so an app with none scores near zero; every deduction is a plain reason, and
  clicking a card shows them with the numbers behind them. The dialog's Fix button hands
  the assistant the findings and suggested values; it never edits the cluster. Suggestions
  come from one reading, so they add missing values or raise tight ones and never shrink
  (`web/src/lib/sizing.ts` holds the weights).
  The host agent records usage history (`internal/usagehistory`): every 5 minutes it
  reads each workload's CPU and memory from the metrics server and keeps one row per
  workload per hour (the busiest copy's peak and the average) for 14 days, in
  `usage-history.db` beside the host database (`-history-dir`, defaulting to
  `-data-dir`; allowed with `-ssh` as a dev aid). Fit and the suggested values then use
  the highest and the usual use over the last days, the dialog charts them against the
  request and limit, and a value is only suggested for shrinking once about two days
  are recorded. It also records how full each volume is, to see how fast it fills. A
  volume that is too small is fixed by growing it (its storage class must allow
  expansion, which the report states); one that is too large cannot be shrunk in place,
  so its fix is a planned migration the assistant rehearses first. The history observes
  only; Prometheus remains the metrics system.
  The **Workloads** page is the simplified, app-first view: one card per app. A card opens
  the app's own page (`#/workloads?app=<namespace>/<name>&tab=...`) with tabs for the
  overview and score, sizing, storage, "How it fits" and details. "How it fits" traces
  address → service → copies → volumes from the cluster's ingresses and services, and lists
  the other apps whose start-up settings name this app's services (and the reverse). That
  last part is a hint read from environment values and arguments, not a traffic record.
  Links to Rancher, Argo CD and Longhorn are built from those tools' own ingress hosts, so a
  tool with no web address in the cluster has no link. Each tab ends with a closed
  "Advanced" fold-out holding the full tables the old Apps, Storage and Deployments pages showed,
  scoped to the app; the list page's Advanced holds what is about the whole cluster: disk
  capacity, image cleanup and every deployment (including ones that belong to no app). Those
  three pages are gone; alerts link straight to that section.
  **Settings** (last in the sidebar) holds General (this browser's preferences, including
  Advanced user mode, which keeps every Advanced section open), AI model, Memory, Embedding
  and Security as tabs.

The UI favors plain language over Kubernetes vocabulary. A namespace is an
*app*, a workload is a *component*, a pod is an *instance*, a node is a *server*,
and Argo CD applications are *deployments*. `src/lib/vocabulary.ts` is the only
place that maps raw Kubernetes and Argo CD terms to those words (raw values stay
on hover and in the Technical details under Cluster > This server). Storage shows
how space is set aside (reserved, allocated, unallocated) and how much is
actually used, per app, rather than a list of claims; that math lives in
`src/lib/storage.ts`.

What counts as unhealthy is defined once in `src/lib/status.ts` and rolled up in
`src/lib/attention.ts`; change those, not individual pages. In development the
UI talks to a locally running agent through Vite's proxy; serving the compiled
assets from the Go binary comes later.

To develop against a host that must not run the agent (such as a production
appliance), run the agent locally in SSH mode. It runs the same allowlisted
read-only commands over SSH, reads only the files listed in `readableFiles`
(`/etc/os-release` and a few `/proc` files), and installs nothing on the target.
It uses your existing SSH key and never prompts.

```bash
cd adaetum/agent
go run ./cmd/adaetum-agent serve -ssh user@host
cd web && npm install && npm run dev   # http://localhost:5173
```

## The contract

`inspect.Report` is the first public contract of the agent. `schemaVersion` is
bumped whenever a change would break a consumer. Fields that cannot be read are
left empty and described in `errors` rather than failing the whole report.

## Intended direction (not built)

Recorded so they are decisions, not accidents. None of this is implemented.

- **Host adoption**: take a machine, add it to the cluster, and lock it down, as
  idempotent, resumable steps with state in the local database. Rocky Linux is
  the only target until a concrete need for another distribution exists.
- **Persistence**: each host has a local Turso database (`internal/store`, built:
  `serve -data-dir` records an `agent.checkin` event at start and every five
  minutes), the host's source of truth for its own state and events. Once
  Kubernetes is up and has two or more nodes, the same binary runs as a cluster
  agent pod, the cluster's single endpoint: host agents send it their events,
  and it shows which servers are not running the agent. Eventual consistency is
  acceptable. See [cluster](docs/cluster.md) for the design, what was checked about
  Turso, and what is not built.
- **Events are append-only and owned by one host**, so central never resolves
  conflicts. Avoid shared mutable rows.
- **Ansible stays the owner** of host configuration; the agent runs it rather
  than reimplementing it.
- **Frontend**: a React portal served by the agent as compiled static assets.
- **Semantic embeddings** (memory and retrieval): a dedicated, stateless
  Kubernetes pod, not a model inside each host agent. Go with MiniLM-L6-v2 INT8
  through ONNX Runtime, limited to 1 to 2 CPU cores, one replica by default, free
  to be rescheduled onto any healthy node. Agents submit embedding jobs through
  the Turso-backed durable queue; the pod processes them asynchronously and
  stores vectors in Turso. Memory management and retrieval stay in the Go
  backend. An embedding failure must never block normal operation: jobs wait in
  the queue and retrieval falls back to non-semantic search. This builds on the
  persistence layer above: its local half exists, the central queue and the
  cluster agent do not, so it is not started.

## MCP (Model Context Protocol)

MCP is a standard interface of the agent, next to the HTTP API, not an add-on.
`adaetum-agent mcp [-ssh user@host]` speaks MCP over stdio. Register it with a
client such as Claude Code:

```bash
claude mcp add adaetum -- /path/to/adaetum-agent mcp -ssh ansible@node-a
```

Rules that keep it standard:

- **One implementation per capability.** A tool wraps a function the HTTP API and
  UI already use (`cmd/adaetum-agent/mcp.go`); it never reimplements one.
- **Read-only by default.** Every tool is annotated `readOnlyHint`, except the
  security-switch tool, which is annotated as changing things so clients ask first. Removing images, running playbooks, or anything else that
  changes a server stays on the token-gated HTTP path.
- **stdout is protocol only.** Diagnostics go to stderr.

Tools today: `get_host_report`, `get_automation_status`, `plan_image_cleanup`, all
read-only. With `-cluster`, `-fingerprint` and `-token-file` it offers everything the
cluster agent's page can do (cluster, launching and cancelling playbooks, join tokens,
approving machines, security switches, direct commands) plus memory. Tools that change
things are marked so the client asks you to approve each call, and turning security
switches **on** additionally needs `-allow-security-changes` (with a recorded reason and
a time limit). See
[security boundary](docs/security-boundary.md).
Remote (HTTP) transport comes with the agent's own authentication.

Design, command line, UI, and schedule for removing unused container images:
[reclaiming container image space](docs/image-cleanup.md). `images plan` is
read-only (aggressive by default: everything no container on the server uses; `-level careful` keeps what
the cluster or a recovery might need); deleting (`images apply`, or the "Free up space" card under Workloads > Advanced)
works only when the agent runs on the server itself, as root, with an access token.
The first real aggressive cleanup on a production server improved its free disk
space by roughly 45% (operator-reported; the audit log has the exact figures).
The `image-cleanup` role runs `images run` on a daily timer from `spec.imageCleanup`
in `platform.yaml`.

Open questions: the node join trust model, how the cluster agent authenticates
host agents, and how steps divide between Go and Ansible.

## Build and run

Go 1.24 or newer is required (the local database driver needs it).

```bash
cd adaetum/agent
go test ./...
go build -o ../../dist/agent/adaetum-agent ./cmd/adaetum-agent
```

To inspect a remote RKE2 host, cross-compile for it and run it there:

```bash
cd adaetum/agent
GOOS=linux GOARCH=amd64 go build -o ../../dist/agent/adaetum-agent-linux-amd64 ./cmd/adaetum-agent
scp ../../dist/agent/adaetum-agent-linux-amd64 user@host:/tmp/adaetum-agent
ssh user@host sudo /tmp/adaetum-agent inspect
```

Use `GOARCH=arm64` for ARM hosts. `sudo` is needed because the RKE2 kubeconfig is
root-readable.

To try a change on a development node without rebuilding the ISO, run
`task dev:push -- [host] [agent|auth]` from the repository root. It builds the host agent
and the sign-in service from this checkout, copies them to the node over SSH, restarts
both services and reports whether they came up. With no host it uses the one online
`node-*` machine on your tailnet. It replaces those two binaries only; the token, accounts
and certificate stay. It is for development nodes.

`task dev:push:pod -- [host]` does the same for the pod: it builds the cluster agent and
sign-in images (needs Docker), loads them into the node and rolls the pod onto them. That
pauses Argo CD, which would otherwise restore the pinned release, and prints how to resume
it. `task dev:push:all -- [host]` runs both.

## Testing approach

Use the cheapest environment that cannot misrepresent the behavior under test.
Unit tests use fakes for the host seams. Containers cover inspection and service
logic later. Firewall, SELinux, SSH hardening, and reboots need real VMs.
