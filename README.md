# Adaetum `/uh-day-tum/`

<a href="./images/main.png">
  <img src="./images/main-crop.png" alt="A chained and locked Adaetum operator's book in a stone sanctuary" width="100%">
</a>

> Adaetum is an open-source, MIT-licensed platform that turns a fresh Rocky Linux
> machine into a security-first, self-hosted RKE2 Kubernetes cluster with GitOps,
> secrets, identity, monitoring, and recovery built in—and keeps the recipe
> needed to rebuild it.

**Status:** every tagged [release](https://github.com/Adaetum/Adaetum/releases)
(currently 0.6.0) is considered production ready. `main` and all other
branches are alpha and can change or break at any time, so run a release, not a
branch. Rocky Linux 10 on one machine is the supported starting point. See
[Project status and boundaries](#project-status-and-boundaries) and the
[versioning policy](docs/versioning.md).

Adaetum is an opinionated platform-activation project for homelab operators. It
guides you from a new machine to an RKE2 cluster with GitOps, secrets, identity,
recovery, observability, and host maintenance integrated from the beginning.

The name is a play on *adytum*: the inner sanctuary of a Greek temple. The idea
is simple—your infrastructure should have a protected source of truth, a clear
ritual for change, and a recovery path that exists before you need it.

Adaetum does not replace the tools you already want to use. It composes mature
upstream projects into one operating model: Gitea holds the in-cluster desired
state, Argo CD reconciles it, OpenBao owns application secrets, Authentik guards
operator-facing services, and Ansible keeps the hosts aligned.

For its intended audience, Adaetum is an **OpenShift replacement**: a way to
get an integrated, security-conscious application platform while retaining a
smaller, inspectable stack built on RKE2 and upstream tools—without OpenShift's
operational and resource overhead. It is an alternative operating model, not a
drop-in or API-compatible OpenShift clone.

Adaetum also occupies some of the same ground as **Sidero Omni and Talos
Linux**: declarative Kubernetes and host lifecycle management across machines
you own. The difference is deliberate. Adaetum keeps a conventional Rocky
Linux host beneath Kubernetes, so operators retain package, service, and
Ansible-level control instead of adopting an immutable appliance OS. That adds
flexibility while keeping desired state, patching, and one-node-at-a-time
maintenance under GitOps control.

## Why Adaetum?

Building a cluster is easy to demo. Owning one through failed disks, expired
credentials, upgrades, and the next rebuild is the harder problem.

Adaetum gives you:

- **A guided first-node path.** `task init` prepares the recovery repository,
  provider access, platform profile, verified Rocky Linux media, and unattended
  installer.
- **A real source-of-truth handoff.** Bootstrap seeds the new cluster's Gitea
  repository; Argo CD then owns reconciliation inside the cluster.
- **Recovery outside the failure domain.** A standalone private GitHub
  repository remains the out-of-band configuration and break-glass copy.
- **Secrets with an authority model.** Bootstrap credentials are temporary;
  OpenBao and scoped Kubernetes integrations own steady-state delivery.
- **Private and public access paths.** Tailscale, ingress-nginx, external-dns,
  Cloudflare, cloudflared, and Authentik form the supported connectivity model.
- **Day-2 operations included.** Health enforcement, observability, policy, and
  one-node-at-a-time Rocky Linux maintenance are part of the platform contract.
- **An OpenShift alternative you own.** GitOps, identity, secrets, policy,
  storage, ingress, and observability arrive as one coherent platform without
  making OpenShift itself the cluster distribution.
- **Declarative hosts without an immutable OS.** Adaetum brings the
  machine-lifecycle goals of Sidero and Talos to general-purpose Rocky Linux,
  preserving direct host extensibility while automating configuration,
  patching, draining, and rolling reboots.

This is for operators who want a cohesive, inspectable system—not a bag of
installation snippets and not a new proprietary control plane.

## One system, three operating moments

<a href="./images/operating.png">
  <img src="./images/operating-crop.png" alt="Adaetum operating model shown as Day 0, Day 1, and Day 2 plus" width="100%">
</a>

### Day 0 — Activate

Run `task init` from a fresh checkout. Adaetum creates or reuses a standalone
private recovery repository, reviews the public platform profile, authorizes
the supported providers, verifies Rocky Linux media, and produces the installer
for the first physical host or VM.

The installed node moves through explicit bootstrap phases: input validation,
temporary secret creation, RKE2 activation, OpenBao initialization, the
Gitea/Argo CD control-pair handoff, GitOps realization, late live-state
reconciliation, and encrypted recovery export.

### Day 1 — Prove and grow

Verify storage, ingress, identity, secrets, observability, and GitOps. Add
server or workload nodes when the first node is stable. The initial install is
deliberately single-node; availability grows with the cluster instead of being
pretended into existence during bootstrap.

### Day 2+ — Operate through Git

Routine platform changes flow through the in-cluster Gitea repository and Argo
CD. The packaged Ansible runner performs bounded host reconciliation, while
`dnf-automatic` and Kured coordinate updates and reboots one node at a time by
default. Gitea can push-mirror outward to the private GitHub recovery repository
without turning GitHub into a second writable source of truth.

Whether a change comes from a person, a script, or an AI assistant, the path is
the same: edit desired state, review the diff, commit it, and let the cluster
reconcile.

## The agent and web app

The Adaetum agent is a single Go binary that runs directly on each host, outside
Kubernetes, so it keeps working while RKE2 is down. A built-in web app on top of
it is where an operator sees the state of the platform and acts on it:

- **Overview:** overall health and an ordered list of what needs attention.
- **Workloads:** one card per application, with a plain-language setup score for
  resources, scaling and storage.
- **Cluster:** which servers run the agent, host details, and Adaetum updates
  with a stable or beta channel.
- **Find apps:** search public Helm charts and container images and turn a result
  into a reviewable draft in the repository's own `pods/` layout.
- **Automation, Security, People, Settings:** run catalogued Ansible playbooks
  (off by default, with a rehearsal first and a record of every launch),
  time-limited security switches, accounts, and the optional assistant.

It is read-only by default and does not run commands on servers; it launches
Ansible, which stays the one way hosts are changed. An optional AI assistant
(Anthropic Claude, or an OpenAI-compatible service for testing) is off until
you choose and enable it, and it asks before changing anything. A read-only
[Model Context Protocol](https://modelcontextprotocol.io) server lets other AI
assistants inspect a host the same way.

The design goal is **recovery login**: with the cluster down, you sign in on a
host with your own account and repair it. That work is in progress; see
[recovery login](adaetum/agent/docs/recovery-login.md), the
[agent README](adaetum/agent/README.md) and
[memory and the assistant](adaetum/agent/docs/memory.md).

## How the ownership model works

| Boundary | Authority |
| --- | --- |
| Public cluster configuration | [`platform.yaml`](platform.yaml) in your private recovery repository |
| Installer and bootstrap implementation | [`ks-src/`](ks-src) and [`ansible/`](ansible) |
| In-cluster product stack | [`pods/`](pods) |
| Day-2 desired state | The cluster's Gitea repository, reconciled by Argo CD |
| Application secrets | OpenBao, delivered through scoped CSI or External Secrets paths |
| Disaster recovery copy | Your standalone private GitHub repository and encrypted recovery export |

`platform.yaml` is the single public, non-secret configuration contract.
Generated `.env` values, `pods/cluster-config/cluster-config.env`, rendered
manifests, installers, and recovery archives are outputs—not competing sources
of truth.

GitHub does not allow private forks of a public repository. Adaetum therefore
uses a standalone private recovery repository. Setup configures `origin` and
`upstream` in a temporary checkout without changing the checkout you launched from.

## Security is part of the architecture

<a href="./images/security.png">
  <img src="./images/security-crop.png" alt="Adaetum security model illustrated as protected systems and identities" width="100%">
</a>

Adaetum treats access, secrets, policy, patching, and recovery as platform
concerns rather than cleanup work:

- Tailscale provides the operator and node overlay, including Tailscale SSH.
- Authentik protects the normal routed operator interfaces.
- OpenBao becomes the authority for Adaetum-managed application credentials.
- External Secrets and the Secrets Store CSI driver deliver scoped workload
  copies without making Kubernetes Secrets the master record.
- Kubewarden and Kubescape provide admission policy and security feedback.
- Prometheus, Grafana, and Alertmanager expose platform health.
- `dnf-automatic` and Kured apply host updates with a cluster lock, drain
  safety, maintenance controls, and a default reboot concurrency of one.
- Phase 99 exports encrypted recovery material before removing transitional
  bootstrap authority.

Security-first does not mean magic. A single-node cluster will have downtime
during a host reboot, and workload availability on a multi-node cluster still
depends on replicas, storage placement, and valid PodDisruptionBudgets.

## What gets deployed

| Capability | Upstream projects |
| --- | --- |
| Host and Kubernetes foundation | Rocky Linux 10, Ansible, RKE2, Rancher, Longhorn, cert-manager |
| GitOps and source control | Argo CD, Gitea, Gitea Actions |
| Secrets, identity, and policy | OpenBao, External Secrets, Secrets Store CSI, Authentik, Kubewarden, Kubescape |
| Networking and edge | Tailscale, ingress-nginx, external-dns, Cloudflare, cloudflared, kube-vip |
| Operations | Homepage, Prometheus, Grafana, Alertmanager, Kured |
| Adaetum's own software | Host agent and web app (Go, React), sign-in service (Bun, Better Auth), embedding service |

Adaetum is activation and integration code. Those upstream applications remain
the operator-facing products, with their own APIs, interfaces, and release
cycles.

## How Adaetum compares

| | Adaetum | OpenShift (OKD is its community edition) | Talos Linux with Omni | RKE2 or k3s on their own |
| --- | --- | --- | --- | --- |
| What it is | An integrated platform that installs and operates a cluster on machines you own | A commercial Kubernetes application platform from Red Hat | An immutable Kubernetes operating system (Talos) and its management console (Omni) | Kubernetes distributions only |
| Kubernetes | RKE2 | OpenShift's own distribution | Upstream Kubernetes on Talos | RKE2 or k3s |
| Host operating system | Rocky Linux 10, a normal mutable Linux | Red Hat CoreOS, managed by the platform | Talos, with no shell or SSH, changed through an API | Any Linux you provide |
| Host changes | Ansible, with rolling patching and reboots | The platform's machine config operators | Machine configuration applied through the API | You do it yourself |
| GitOps, secrets, identity, policy, monitoring | Included and wired together | Included (OpenShift's own components) | Not included | Not included |
| Recovery | A private repository you own, with an encrypted recovery export | Backup tooling you set up | Backups you set up | Backups you set up |
| License and support | MIT, community project, no support contract | Commercial support contracts | Mixed; see the vendor | Open source |

Adaetum is not API-compatible with OpenShift and does not run OpenShift
Operators. For an honest, detailed comparison, including where Adaetum is
weaker, see [Adaetum compared with OpenShift and Talos with Omni](docs/comparison.md).
See also [Project status and boundaries](#project-status-and-boundaries).

## FAQ

### What is Adaetum?

An open-source platform that installs RKE2 Kubernetes on a Rocky Linux machine
you own and wires up GitOps (Argo CD, Gitea), secrets (OpenBao), identity
(Authentik), policy, monitoring and recovery. It is made for homelab and small
self-hosted operators who want the whole stack, not just a cluster.

### Is it production ready?

Every tagged release (currently 0.6.0) is considered production ready, and it is
the version to run. As with any software, a new release can ship with bugs or
breaking changes, so read the release notes before upgrading. `main` and every
other branch are alpha: they change frequently and can break. The Adaetum agent and web app are newer and ship as
beta pre-releases. Rocky Linux 10 on a single machine is the supported starting
point. The [public roadmap](TODO.md) lists what remains.

Adoption is still low. As it grows and more people contribute, Adaetum will
adopt more of the standard, industry-normal practices for release management,
such as a regular release cadence, formal release candidates, and clearer
upgrade and support guarantees. See the
[versioning policy](docs/versioning.md) for how releases work today.

### How is it different from k3s?

Adaetum uses RKE2, not k3s. Both come from the Rancher and SUSE family. k3s is
the lightweight distribution built for small and edge machines. RKE2 is the
security-focused one, hardened to CIS benchmarks with a FIPS-capable build, which
suits a security-first platform. Neither installs GitOps, secrets, identity,
policy or recovery. Adaetum adds those, so it is a larger system than k3s on its
own. If all you want is a small cluster, k3s alone is lighter.

### How is it different from OpenShift?

OpenShift is a commercial platform with support contracts and its own APIs and
Operators. Adaetum is an MIT-licensed, community alternative built from upstream
projects on RKE2. It is a different operating model, not an API-compatible
clone, so workloads that depend on OpenShift-specific APIs need migration.

### How is it different from Talos Linux and Omni?

They cover similar ground: declarative Kubernetes and host lifecycle across
machines you own. Talos is an immutable operating system with no shell, managed
through an API. Adaetum keeps a normal Rocky Linux host, so you keep package,
service and Ansible-level control, and it handles patching, draining and rolling
reboots for you.

### Which operating systems does it support?

Rocky Linux 10 is the stable target. Ubuntu 24.04 is experimental and disabled
by default.

### Do I need more than one machine?

No. Install is always a single node, and you add more later when the first one
is stable.

### What accounts and tools do I need?

A GitHub, Cloudflare and Tailscale account, which setup walks you through
connecting. Setup downloads and verifies the Rocky Linux media if you do not have
it. Docker is not required for building the installer ISO on supported
platforms.

### Where does my configuration live, and can I leave?

In a standalone private repository you own, with the public settings in
[`platform.yaml`](platform.yaml). Everything it deploys is an upstream open-source
project, and Adaetum is MIT-licensed, so you are not tied to a proprietary
control plane.

### Can an AI assistant operate it?

Yes, through the same path as a person: edit the desired state, review the diff,
commit it, and let the cluster reconcile. The web app also has an optional
assistant and a read-only MCP server. See
[The agent and web app](#the-agent-and-web-app).

### What does the name mean and how is it said?

It is said "uh-day-tum" and is a play on *adytum*, the inner sanctuary of a
Greek temple: a protected source of truth for your infrastructure.

## Quick start

Install [Task](https://taskfile.dev/docs/installation), then clone the public
project and start the guided setup:

```bash
git clone https://github.com/Adaetum/Adaetum.git
cd Adaetum
task init
```

You can instead download and extract the repository ZIP, open a terminal in
the extracted folder, and run `task init`. Setup installs Git if needed and
creates a local recovery snapshot from the extracted files. Files excluded by
`.gitignore` stay local; other files in that folder become part of the private
recovery repository.

`task init` opens a setup window. It walks through eight steps—repository,
providers, profile, installer media, validation, rendering, publishing, and
finalizing—and finishes with the path to the generated ISO. Attach that ISO to
the first machine and boot it. On first run, setup installs missing tools and
builds the window using a supported local package manager. ISO tools are bundled
for supported platforms, so Docker is not required there.

You will need:

- A supported package manager for any missing setup tools
- Access to GitHub, Cloudflare, and Tailscale; setup guides you through
  connecting them
- Internet access to download and verify Rocky Linux media if you do not
  already have a supported image
- **An internet connection on the target machine for the whole install and first
  boot.** Installation does not work offline. The machine joins your Tailscale
  network on first boot and must not finish booting without that protection,
  and the install reports its progress and logs to your own Cloudflare storage.

Useful setup paths:

| Command | Purpose |
| --- | --- |
| `task init` | Supported guided first run (opens the setup window) |
| `task init:silent` | Replay setup from saved credentials with no window and no questions |
| `task init:dryrun` | Show the engine's read-only plans; pass `-- --window` to rehearse the full guided UI with assumed-success responses and no real checks or changes |
| `task init:clean` | Open the window ignoring saved credentials and values |
| `task initialize` | Run setup with no window; answer questions with `-- --answer id=value` |
| `task platform:validate` | Validate the public platform profile |
| `task platform:render` | Regenerate profile-owned outputs |

For prerequisites, provider permissions, installation behavior, and recovery
operations, follow the [Bootstrap and recovery guide](setup.md).

## Project status and boundaries

- Rocky Linux 10 is the stable installer target.
- Ubuntu 24.04 support is experimental and disabled by default.
- The supported external integrations are GitHub, Cloudflare, and Tailscale;
  Adaetum does not claim an untested provider-plugin abstraction.
- Installation requires an active internet connection on the target machine;
  there is no offline install.
- Bootstrap is optimized for one node at install time. Additional nodes and
  higher availability come afterward.
- Major design changes may require deliberate adoption in the private recovery
  repository; Adaetum does not promise an in-place compatibility API between
  every architecture generation.
- Adaetum does not implement OpenShift-specific APIs, Operators, routes, or
  enterprise support contracts; workloads depending on them require migration.

## Documentation

- [Bootstrap and recovery guide](setup.md)
- [Comparison with OpenShift and Talos with Omni](docs/comparison.md)
- [Third-party software and licenses](docs/licenses.md)
- [Architecture and authority audit](docs/audit.md)
- [Automatic host maintenance](docs/host-maintenance.md)
- [External integration boundaries](docs/integrations.md)
- [Host agent and web app](adaetum/agent/README.md)
- [Release evidence](docs/release-evidence.md)
- [Versioning policy](docs/versioning.md)
- [Maintainer triage and release process](docs/maintainer-process.md)
- [Public roadmap](TODO.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md) and [support policy](SUPPORT.md)

## Contributing

Contributions are welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md), use
the issue templates for proposals and bugs, and keep pull requests focused.
Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Adaetum is released under the [MIT License](LICENSE).
