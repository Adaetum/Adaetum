# Adaetum `/uh-day-tum/`

<a href="./images/main.png">
  <img src="./images/main-crop.png" alt="Adaetum platform overview" width="100%">
</a>

A play on - *Adytum*: the inner sanctuary of a Greek temple | [https://Adaetum.com](https://Adaetum.com)

## Security-First Operations and Resilience

Adaetum is an opinionated platform-activation system for taking a fresh machine
to a working RKE2-based cluster with GitOps, recovery, and security controls
built in from the start.

> If infra and ops are "Greek to me," welcome to the Adaetum: the inner sanctum.
> Whisper to your AI oracle, ship the commit, watch Argo CD do the ritual.

It is aimed at operators who want a reproducible way to bootstrap a cluster,
grow it over time, and keep managing it through a single Git-driven path
instead of a pile of one-off install steps and hand-maintained node state.

At a high level, Adaetum is designed to give you:

- a repeatable day-0 bootstrap path from installer media to an operational cluster
- a day-1 path for adding nodes, applying your baseline configuration, and stabilizing the platform
- a day-2+ model where Gitea and Argo CD become the primary source of truth for ongoing changes
- a declarative platform shape that makes automation, scripting, and even AI-assisted changes practical because the desired state lives in code
- a security-first foundation that treats access, secrets, recovery, and policy as part of the platform rather than later add-ons
- a baseline portal and UI layer so operators can validate internal and public access paths quickly after bootstrap

## Recommended Path

If you are setting up Adaetum for the first time, start here:

- [setup.md](setup.md): supported first-time setup flow

That guide is the primary setup path. It walks through `task initialize`,
credential collection, artifact publishing, and the first-node install flow.

## What This Repo Does

This repository contains the activation materials that power that workflow:

- the Ansible runner container used for bootstrap and enforcement
- the playbooks and roles used for platform activation
- the kickstart templates and ISO customization flow used to install nodes
- the publishing workflow for bootstrap artifacts and edge delivery

Adaetum is designed to own day 0 and day 1 directly. After that, the intended
steady state is for the deployed Gitea instance to hold the platform
configuration and for Argo CD to reconcile that desired state into the cluster.

The bootstrap and publishing design intentionally leans on Cloudflare R2,
Cloudflare Workers, and GitHub Actions to handle a large share of the artifact
delivery and automation workload. That split is deliberate: the default setup
is meant to fit within free-tier service limits wherever practical.

## Operating Model

<img src="./images/operating-crop.png" alt="Adaetum operating model" width="100%">

### Day-0 operations

Day 0 is about getting from an unprepared machine to an initial working
platform:

- run `task initialize` to generate bootstrap configuration and installer artifacts
- install the first node from the generated media
- perform the initial break-glass bootstrap
- stand up the baseline platform services needed for GitOps, secrets, and recovery

> [!TIP]
> Detailed first-node install and bootstrap steps are documented in
> [`setup.md`](setup.md).

### Day-1 operations

Day 1 is about turning that first-node bootstrap into a more durable cluster
baseline:

- add additional server nodes for cluster failover
- add workload capacity nodes
- verify cluster health, storage, ingress, secrets, and GitOps reconciliation
- verify routed UI access from the portal landing page (`home.<domain>`)
- verify you can authenticate to Homepage with the bootstrap-generated portal credential
- update platform configuration to the user baseline through Gitea, such as
  editing pod configuration to enable alerts and other desired defaults
- transition routine platform changes into the Git-driven operating path

### Day-2+ operations

Day 2+ is the steady state. Day-to-day changes should flow through GitOps using
the cluster's Gitea as the authoritative source repository for cluster and
workload state.

Because the platform is defined as code, that also makes higher-level
automation easier to use: whether the change comes from a person, a script, or
an AI assistant, the practical path is the same. Update the pod definitions or
other desired-state files, commit the change, and watch Argo CD reconcile it.

Gitea is also intended to run Actions for platform automation. For repository
replication, the intended model is for Gitea to stay authoritative inside the
cluster and push-mirror outward to GitHub as an out-of-band recovery copy of
the platform repository when stable GitHub credentials are available at
bootstrap time.

This repo no longer treats GitHub and Gitea as symmetric writable peers.
Gitea's mirror model can safely support an existing Gitea repo pushing outward
to GitHub, but it does not provide a safe automatic bidirectional mirror where
both sides remain independently writable. In practice, that means:

- bootstrap can still seed Gitea from GitHub
- steady-state GitOps should write to Gitea
- GitHub is the optional mirrored backup, not a second live source of truth
- when bootstrap has a stable PAT-style GitHub credential, Phase 50 configures
  the Gitea bootstrap repo to push outward to the GitHub source repo on every
  Gitea push

For recovery workflows, `task fetch-backup` is the supported way to retrieve
the emergency kit, including the passwords and bootstrap materials generated
during initial setup.

Homepage itself is treated as an operator-facing admin surface, not a public
anonymous landing page. It lives behind the standard Authentik-protected routed
UI path just like the other operator-facing cluster services.

This repo remains the source for activation, installer generation, and recovery
workflows, but the intended operator experience is Git-driven change
management rather than manual node-by-node administration. Gatekeeper is
intended to actively prevent unmanaged manual changes.

## Security

<img src="./images/security-crop.png" alt="Adaetum security model" width="100%">

Adaetum is designed around a security-first operating model rather than
security as a later add-on. Bootstrap is handled through the break-glass flow,
secrets and sensitive platform state are anchored in OpenBao, and Gatekeeper
is used to push the cluster away from unmanaged or policy-breaking changes.
Authentik is now part of the normal routed UI path: the standard operator-facing
UI hosts are expected to redirect to Authentik for login before access is
granted.

At the host layer, the repo seeds an `ansible` user and relies on Tailscale for
node-to-node connectivity and operator access. Nodes join the tailnet during
first boot with Tailscale SSH enabled, and cluster discovery is built around
reaching peer nodes over Tailscale rather than exposing a traditional flat
management network.

By default, administrative access is expected to happen over Tailscale SSH
using Tailscale-managed identities rather than through local passwords or
traditional password-based SSH.

Ansible is part of that model twice: first as the bootstrap engine that brings
the platform up, and then as the packaged runner container that is deployed
back into the cluster as a pod and managed through Argo CD. That lets the same
automation continue enforcing the intended baseline after initial activation
instead of stopping at first boot, while also giving the platform a single
management path and a single operational source of truth.

Cloudflare is part of that story too. The first-boot payload is published there
and retrieved at install time behind a shared key, which means even reused or
older installation media can still pull the current bootstrap content. In
practice, that reduces drift between older and newer nodes because the
bootstrap path is tied to the latest published Ansible payload rather than only
to whatever happened to be baked into the ISO. That shared key is established
through `task initialize`, so regenerating setup from another system or
changing that key effectively revokes payload access for older ISOs that still
carry the previous value.

> [!IMPORTANT]
> Installer media still carries one bootstrap secret: the shared Worker token.
> That token is used at first boot to fetch the rest of the runtime/bootstrap
> secrets from the R2/Worker path into a root-only env file. The ISO does not
> retain the broader platform secret set, but first boot now depends on
> network reachability to the bootstrap Worker endpoint and on that bootstrap
> token remaining valid.

For the operator-facing routed UI host pattern and the `.local` / public-domain
split, see the collapsed routing model section in [`setup.md`](setup.md).

## How the Repo Is Organized

Adaetum is built around a few core layers:

- `ansible/` contains the activation logic, including playbooks, automation
  roles, bootstrap scripts, and the runner container resources used to bring up
  and enforce the platform.
- `pods/` contains supporting platform manifests that are applied as part of
  the broader bootstrap and GitOps workflow. The public-safe base plus the
  committed cluster config in `pods/cluster-config/cluster-config.env` define the
  user-owned manifest surface that `task initialize` writes and re-renders.
- `ks-src/` contains the source templates, manifests, and shared fragments used
  to compile installer-facing kickstarts into `dist/ks-templates/`.

The kickstart and ISO pipeline is what allows Adaetum to support installer-led
activation flows. Rocky Linux is the primary supported path today, and the
kickstart source structure is designed so other operating system installers can
be added through the same compiled workflow over time.

## Tech Stack and Third-Party Software

Adaetum deploys and operates around this core stack:

| Layer | Stack |
| --- | --- |
| OS and host bootstrap | [![Rocky Linux](https://img.shields.io/badge/Rocky%20Linux-10G-10B981?logo=rockylinux&logoColor=white)](https://rockylinux.org/) [![Ansible](https://img.shields.io/badge/Ansible-Automation-EE0000?logo=ansible&logoColor=white)](https://github.com/ansible/ansible) [![ansible-runner](https://img.shields.io/badge/ansible--runner-Enforcement-EE0000?logo=ansible&logoColor=white)](https://github.com/ansible/ansible-runner) |
| Cluster foundation | [![RKE2](https://img.shields.io/badge/RKE2-Kubernetes-0F172A?logo=kubernetes&logoColor=white)](https://github.com/rancher/rke2) [![Rancher](https://img.shields.io/badge/Rancher-Management-0075A8?logo=rancher&logoColor=white)](https://github.com/rancher/rancher) [![Longhorn](https://img.shields.io/badge/Longhorn-Storage-00C7B7?logoColor=white)](https://github.com/longhorn/longhorn) [![cert-manager](https://img.shields.io/badge/cert--manager-TLS-326CE5?logo=kubernetes&logoColor=white)](https://github.com/cert-manager/cert-manager) |
| GitOps and source of truth | [![Argo CD](https://img.shields.io/badge/Argo%20CD-GitOps-EF7B4D?logo=argo&logoColor=white)](https://github.com/argoproj/argo-cd) [![Gitea](https://img.shields.io/badge/Gitea-Forge-609926?logo=gitea&logoColor=white)](https://github.com/go-gitea/gitea) |
| Secrets, identity, and policy | [![OpenBao](https://img.shields.io/badge/OpenBao-Secrets-111827?logoColor=white)](https://github.com/openbao/openbao) [![Authentik](https://img.shields.io/badge/Authentik-SSO-FD4B2D?logo=authentik&logoColor=white)](https://github.com/goauthentik/authentik) [![Gatekeeper](https://img.shields.io/badge/Gatekeeper-Policy-4F46E5?logo=openpolicyagent&logoColor=white)](https://github.com/open-policy-agent/gatekeeper) [![Kubescape](https://img.shields.io/badge/Kubescape-Security-00B2A9?logo=kubernetes&logoColor=white)](https://github.com/kubescape/kubescape) [![argocd-audit](https://img.shields.io/badge/argocd--audit-Compliance-5E6AD2?logo=argo&logoColor=white)](https://argo-cd.readthedocs.io/) |
| Networking and edge access | [![Tailscale](https://img.shields.io/badge/Tailscale-Mesh%20VPN-0F4BFF?logo=tailscale&logoColor=white)](https://tailscale.com/) [![ingress-nginx](https://img.shields.io/badge/ingress--nginx-Ingress-009639?logo=kubernetes&logoColor=white)](https://github.com/kubernetes/ingress-nginx) [![external-dns](https://img.shields.io/badge/external--dns-DNS-4285F4?logo=kubernetes&logoColor=white)](https://github.com/kubernetes-sigs/external-dns) [![Cloudflare](https://img.shields.io/badge/Cloudflare-Edge-F38020?logo=cloudflare&logoColor=white)](https://www.cloudflare.com/) [![cloudflared](https://img.shields.io/badge/cloudflared-Tunnel-F38020?logo=cloudflare&logoColor=white)](https://github.com/cloudflare/cloudflared) |
| Observability, portal, and automation | [![Prometheus](https://img.shields.io/badge/Prometheus-Metrics-E6522C?logo=prometheus&logoColor=white)](https://github.com/prometheus/prometheus) [![Grafana](https://img.shields.io/badge/Grafana-Dashboards-F46800?logo=grafana&logoColor=white)](https://github.com/grafana/grafana) [![Alertmanager](https://img.shields.io/badge/Alertmanager-Alerts-E6522C?logo=prometheus&logoColor=white)](https://github.com/prometheus/alertmanager) [![Homepage](https://img.shields.io/badge/Homepage-Portal-111827?logoColor=white)](https://github.com/gethomepage/homepage) [![Headlamp](https://img.shields.io/badge/Headlamp-Kubernetes_UI-326CE5?logo=kubernetes&logoColor=white)](https://headlamp.dev/) [![Apprise](https://img.shields.io/badge/Apprise-Notifications-4B5563?logoColor=white)](https://github.com/caronc/apprise) [![GitHub Actions](https://img.shields.io/badge/GitHub%20Actions-Automation-2088FF?logo=githubactions&logoColor=white)](https://github.com/features/actions) |

In practice, Rocky Linux is installed through the repo's kickstart flow,
Ansible bootstraps the machines and is then repackaged as an in-cluster runner
pod that Argo CD can keep deployed for ongoing enforcement, and RKE2 provides
the Kubernetes foundation. Rancher, Longhorn, and cert-manager round out the
core platform; Argo CD and Gitea take over for GitOps and source-of-truth
management; OpenBao, Gatekeeper, Kubescape, and argocd-audit cover secrets,
policy, and compliance; Tailscale, ingress-nginx, external-dns, Cloudflare,
and cloudflared handle connectivity and edge access; and Prometheus, Grafana,
Alertmanager, Homepage, Headlamp, Apprise, and GitHub Actions support
observability, access validation, and automation.

Adaetum integrates and deploys third-party open source and hosted software.
All product names, logos, and trademarks in this repository remain the
property of their respective owners and are used here only to identify the
components this platform automates. Inclusion in this stack does not imply
endorsement, partnership, or authorship by those upstream projects or vendors.
