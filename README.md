# Adaetum `/uh-day-tum/`

<a href="./images/main.png">
  <img src="./images/main-crop.png" alt="A chained and locked Adaetum operator's book in a stone sanctuary" width="100%">
</a>

> Turn a fresh Rocky Linux machine into a security-first, self-hosted Kubernetes
> platform—and keep the recipe needed to rebuild it.

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
uses a standalone private repository as `origin` and preserves canonical
Adaetum as `upstream`.

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
- Gatekeeper and Kubescape provide policy and security feedback.
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
| Secrets, identity, and policy | OpenBao, External Secrets, Secrets Store CSI, Authentik, Gatekeeper, Kubescape |
| Networking and edge | Tailscale, ingress-nginx, external-dns, Cloudflare, cloudflared, kube-vip |
| Operations | Homepage, Headlamp, Prometheus, Grafana, Alertmanager, Kured |

Adaetum is activation and integration code. Those upstream applications remain
the operator-facing products, with their own APIs, interfaces, and release
cycles.

## Quick start

Clone the public project and start the guided setup:

```bash
git clone https://github.com/Adaetum/Adaetum.git
cd Adaetum
task init
```

The walkthrough has five sections—Repository, Providers, Profile, Installer,
and Bootstrap—and finishes by printing the generated ISO path. Attach that ISO
to the first machine and boot it.

You will need:

- Git and Python 3
- [Task](https://taskfile.dev/docs/installation)
- accounts with GitHub, Cloudflare, and Tailscale
- a supported Rocky Linux 10 Minimal or DVD installer image, which setup can
  discover or download and verify
- Docker only when building the ISO locally instead of reusing a published copy

Useful setup paths:

| Command | Purpose |
| --- | --- |
| `task init` | Supported guided first run |
| `task init:silent` | Replay previously saved choices on the same workstation |
| `task init:dryrun` | Rehearse the journey without provider calls or mutations |
| `task init:clean` | Replace saved provider credentials and generated runtime values |
| `task initialize` | Rerun bootstrap preparation without the first-run account walkthrough |
| `task platform:validate` | Validate the public platform profile |
| `task platform:render` | Regenerate profile-owned outputs |

For prerequisites, provider permissions, installation behavior, and recovery
operations, follow the [Bootstrap and recovery guide](setup.md).

## Project status and boundaries

- Rocky Linux 10 is the stable installer target.
- Ubuntu 24.04 support is experimental and disabled by default.
- The supported external integrations are GitHub, Cloudflare, and Tailscale;
  Adaetum does not claim an untested provider-plugin abstraction.
- Bootstrap is optimized for one node at install time. Additional nodes and
  higher availability come afterward.
- Major design changes may require deliberate adoption in the private recovery
  repository; Adaetum does not promise an in-place compatibility API between
  every architecture generation.
- Adaetum does not implement OpenShift-specific APIs, Operators, routes, or
  enterprise support contracts; workloads depending on them require migration.

## Documentation

- [Bootstrap and recovery guide](setup.md)
- [Architecture and authority audit](docs/audit.md)
- [Automatic host maintenance](docs/host-maintenance.md)
- [External integration boundaries](docs/integrations.md)
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
