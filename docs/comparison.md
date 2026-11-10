# Adaetum compared with OpenShift and Talos with Omni

This is an honest comparison, written by the Adaetum maintainers, who are
obviously not neutral. Where the other projects are better, we say so. Adaetum
is free software at an early stage, so some of this will not flatter us. That is
fine: the goal is to help you pick the right tool, not to win.

Facts about other projects come from their public documentation (linked at the
end) and were checked on 2026-11-09. Products change; if something here is out of
date or wrong, please open an issue.

## The short version

| Choose… | If you want… |
| --- | --- |
| **OpenShift** | A commercially supported application platform with a large certified ecosystem, Operators, built-in builds and pipelines, and a vendor to call. |
| **Talos Linux, with or without Omni** | An immutable, API-managed operating system with a minimal attack surface, and a console to manage many clusters. |
| **Adaetum** | A free, MIT-licensed, opinionated stack on a normal Linux host you already know how to operate, with your configuration kept in a private repository you own. |

If you need a support contract, certified compliance, or many clusters managed
centrally, Adaetum is not that today.

## Side by side

| | Adaetum | OpenShift | Talos Linux and Omni |
| --- | --- | --- | --- |
| What it is | An integration of upstream tools that installs and operates a cluster on machines you own | A commercial Kubernetes application platform from Red Hat; OKD is its community edition | Talos is an immutable Kubernetes operating system; Omni is a management console for Talos machines |
| Kubernetes | RKE2 | OpenShift's own distribution | Upstream Kubernetes |
| Host operating system | Rocky Linux 10, a normal mutable Linux | Red Hat CoreOS managed by the cluster (OKD uses a Fedora or CentOS CoreOS variant) | Talos, managed through an API |
| How hosts change | Ansible, plus rolling patching and reboots | Cluster-managed machine configuration and upgrades | Machine configuration sent to the API; images upgraded as a whole |
| Shell access to hosts | Yes (SSH, including Tailscale SSH) | Possible, but the platform expects to manage the host | None by design |
| Integrated GitOps, secrets, identity, policy, monitoring | Included, chosen and wired together | Included, using OpenShift's own components and Operators | Not included; you add your own |
| Multiple clusters from one place | No | Yes, with Red Hat's add-on management products | Yes, with Omni |
| Starting size | One node; more are added later | Single-node or multi-node; supported minimums are sizeable (see below) | One node or more |
| License | MIT | Commercial subscription (OKD is open source) | Talos: MPL-2.0. Omni: Business Source License, source available |
| Support | Community only | Red Hat support contracts (not for OKD) | Sidero commercial support options |
| Maturity | Alpha on `main`; tagged releases are considered production ready; small community | Very mature, large vendor | Mature and widely used |

## Against OpenShift

**Where OpenShift is better**
- **Support and accountability.** You can buy a support contract and a vendor
  stands behind it. Adaetum has none, and OKD, OpenShift's community edition,
  also has none from Red Hat.
- **Ecosystem and certification.** OpenShift has certified Operators, vendor
  validated workloads, and compliance programs that large organizations rely on.
  Adaetum makes no compliance claims.
- **Built-in developer platform.** Builds, pipelines, an integrated image
  registry, routes, and a polished console come with it.
- **Cluster-managed upgrades.** The platform upgrades itself and its hosts as one
  tested unit. Adaetum's upgrade tooling for RKE2 and the host is still on the
  [roadmap](../TODO.md).
- **Scale and multi-cluster management.** Proven at sizes and in fleets Adaetum
  has never been tried at.

**Where Adaetum may fit better**
- **Cost and licensing.** It is MIT licensed with no subscription.
- **Weight.** OpenShift's single-node documentation lists 4 vCPUs (newest
  docs) or 8 vCPUs (older docs), 16 GB of RAM and 120 GB of storage as the
  minimum. Adaetum has not published its own sizing yet, so we cannot claim a
  smaller footprint with numbers. It runs fewer bespoke components, but it also
  runs a full stack of its own (Gitea, Argo CD, OpenBao, Authentik,
  monitoring), so do not assume it is tiny.
- **A smaller, inspectable stack.** Everything is an upstream open-source
  project in a repository you own, with no proprietary control plane.
- **Your own hosts.** A conventional Linux host with ordinary tools, not a
  platform-owned operating system.

**Migration.** Adaetum does not implement OpenShift APIs, Operators, routes, or
Security Context Constraints. Workloads that depend on them need to be changed.

## Against Talos Linux and Omni

**Where Talos and Omni are better**
- **Smaller attack surface.** Talos has no shell or SSH and an immutable system,
  so there is far less on the host to attack or to drift. Adaetum's host is a
  normal mutable Linux, so configuration drift and manual changes are possible,
  even though Ansible works to prevent them.
- **Atomic, API-driven lifecycle.** Whole-image upgrades with a defined rollback
  path. Adaetum's equivalent is on the roadmap.
- **Fleet management.** Omni manages many clusters and machines from one place,
  with identity integration. Adaetum manages one cluster.
- **Maturity and community.** Both are widely used and have vendor backing.

**Where Adaetum may fit better**
- **Production licensing.** Talos is MPL-2.0 and free. Omni's server is under the
  Business Source License: Sidero's documentation says self-hosting it is free
  for non-production use and production use needs a commercial license, or its
  hosted service. Adaetum is MIT with no production gate.
- **A complete stack out of the box.** Talos and Omni give you Kubernetes and
  lifecycle management. GitOps, secrets, identity and policy are yours to add.
  Adaetum chooses and integrates them.
- **A familiar host.** If your team knows Linux, SSH, packages and Ansible, you
  keep all of that. With Talos you learn its API-first way of working.
- **Recovery model.** Your configuration lives in a standalone private
  repository you own, and setup keeps a recovery copy.

## What Adaetum does not do well yet

- **Early and small.** `main` and other branches are alpha; the community is
  small.
- **One OS.** Rocky Linux 10 only. Ubuntu 24.04 is experimental and disabled.
- **One node at install.** Higher availability comes afterward, as nodes are
  added.
- **No off-site backups by default yet.** Application data volumes are not
  backed up off-site automatically. This is on the [roadmap](../TODO.md).
- **Upgrades are not yet managed for you.** RKE2 and host upgrades with
  rehearsal and rollback are planned, not built.
- **The web app is newer than the platform.** The agent and web app ship as beta
  pre-releases, and recovery login is still in progress.
- **Outside accounts are required.** Setup uses GitHub, Cloudflare and
  Tailscale. They are the supported integrations, not optional extras.
- **No published hardware sizing**, support contract, or compliance
  certification.

## Sources

- [Single-node OpenShift requirements, 4.22 docs](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/installing_on_a_single_node/preparing-to-install-sno) and [4.14 docs](https://docs.redhat.com/en/documentation/openshift_container_platform/4.14/html/installing_on_a_single_node/preparing-to-install-sno)
- [OKD4 general availability announcement](https://www.redhat.com/en/blog/okd4-is-now-generally-available) and [OKD Streams](https://www.redhat.com/en/blog/okd-streams-building-the-next-generation-of-okd-together)
- [Sidero: Omni self-hosted, production versus non-production](https://docs.siderolabs.com/omni/self-hosted/production-vs-non-production.md)
- [Sidero: Omni source code now available](https://www.siderolabs.com/blog/omni-source-code-now-available/)
- [Sidero: license and support options](https://www.siderolabs.com/?p=9160)
- [Sidero Labs home page (Talos stays under MPL-2.0)](https://siderolabs.com/)
