# Third-party software and licenses

Adaetum itself is [MIT licensed](../LICENSE). It deploys a set of upstream
projects that keep their own licenses. This page lists what Adaetum installs or
bundles by default, grouped by license, so you can see what you are running.

This is a practical inventory, not legal advice. Licenses were read from each
project's repository on 2026-11-09 using GitHub's license data and, where that
was unclear, the project's own license files. A project's current license can
differ from the version Adaetum pins. Versions are pinned in
[`pods/`](../pods) (`*.app.yaml`) and [`ansible/`](../ansible); check the license
shipped with the exact version before relying on it.

## What to know first

- **Most of the stack is permissive.** Apache-2.0 and MIT cover the large
  majority of components, and allow use, modification and redistribution with
  attribution.
- **Grafana is AGPL-3.0.** That is a network copyleft license. Running it
  unmodified for your own use is fine. If you modify it and let other people use
  your modified version over a network, you must offer them the source.
- **Redis 8 is not open source in the usual sense.** The Argo CD chart Adaetum
  pins ships Redis 8.2.3, which you may use under the Redis Source Available
  License v2, the Server Side Public License v1, or the AGPL-3.0. Valkey is a
  BSD-3-Clause alternative; Adaetum has not switched to it.
- **Authentik has one proprietary carve-out.** Its core is MIT, but the
  `authentik/enterprise/` directory is under a separate commercial license.
  Adaetum deploys the standard chart and image; the enterprise features need that separate license.
- **GPL software runs as separate programs.** Homepage, Crafty Controller and
  Ansible are GPL-3.0. Adaetum runs them rather than linking them into its own
  code, so they do not change Adaetum's MIT license. Redistributing their
  source or binaries brings that license's obligations with it.
- **Optional apps.** Crafty Controller (a game server manager) is deployed as an
  example workload, not part of the platform.

## By license

### Apache-2.0 (permissive)

| Component | Role |
| --- | --- |
| RKE2 (bundles containerd, etcd, CoreDNS, metrics-server, runc) | Kubernetes distribution |
| Calico | Cluster networking |
| Rancher | Cluster management console |
| Longhorn | Replicated storage |
| cert-manager | Certificates |
| ingress-nginx (bundled with RKE2) | Ingress |
| Argo CD | GitOps |
| Helm | Chart installs |
| External Secrets | Secret delivery |
| Secrets Store CSI driver | Secret delivery |
| Reloader | Restart on secret change |
| Kubewarden | Admission policy |
| Kubescape | Security scanning |
| Headlamp | Kubernetes web UI |
| Prometheus, Alertmanager, kube-prometheus-stack | Monitoring and alerts |
| ntfy | Push notifications (dual-licensed Apache-2.0 or GPL-2.0) |
| Kured | Coordinated reboots |
| Descheduler | Pod rebalancing |
| Goldilocks | Resource recommendations |
| Vertical Pod Autoscaler | Resource sizing |
| kube-vip | Virtual IP |
| external-dns | DNS records |
| cloudflared | Cloudflare Tunnel |
| Grafana MCP server, Kubernetes MCP server, Argo CD MCP server | Assistant tool servers |
| kubectl, Apache httpd | Utilities in helper containers |
| all-MiniLM-L6-v2 (model) | Text embeddings for agent memory |

### MIT (permissive)

| Component | Role |
| --- | --- |
| Gitea (and Gitea MCP server) | In-cluster Git and Gitea Actions |
| Authentik (core) | Identity and sign-in |
| Apprise API | Notification routing |
| Better Auth | Sign-in library in the agent's auth service |
| Bun | Runtime for the auth service (the compiled binary also bundles JavaScriptCore, which is LGPL) |
| ONNX Runtime (Go binding) | Runs the embedding model |
| Wails, go-keyring, Turso Go driver | Installer window, credential store, agent database |
| Task, rclone, GitHub CLI, Gum | Setup tooling |
| React, Radix Themes, TanStack Table, assistant-ui and most other web UI packages | Agent web app (253 of the 268 packages used to build it are MIT) |

### BSD and other permissive

| Component | License | Role |
| --- | --- | --- |
| Tailscale client and operator | BSD-3-Clause | Overlay network |
| Apprise | BSD-2-Clause | Inside the Apprise API image |
| golang.org/x/crypto, golang.org/x/text | BSD-3-Clause | Go libraries |
| PostgreSQL | PostgreSQL License | Gitea and Authentik database |
| ISC and 0BSD web packages | ISC, 0BSD | Agent web app |
| Python (helper containers) | PSF-2.0 | Utility containers |

### MPL-2.0 (weak copyleft)

| Component | Role |
| --- | --- |
| OpenBao | Secrets authority |

Changes to MPL-licensed files must be shared if you distribute them; the rest of
your code is unaffected.

### GPL (strong copyleft)

| Component | License | Role |
| --- | --- | --- |
| Homepage | GPL-3.0 | Portal dashboard |
| Crafty Controller | GPL-3.0 | Game server manager (example workload) |
| Ansible (ansible-core) | GPL-3.0 | Host configuration |
| Rocky Linux | A mix: mostly GPL and LGPL, plus others | Host operating system |
| BusyBox, Alpine Linux base layers | GPL-2.0 and others | Helper container images |

### AGPL (network copyleft)

| Component | Role |
| --- | --- |
| Grafana | Dashboards |

### Source-available or tri-licensed

| Component | License | Role |
| --- | --- | --- |
| Redis 8.2.3 | RSALv2, SSPLv1, or AGPL-3.0 (your choice) | Cache inside Argo CD |
| Authentik enterprise directory | Proprietary (not used) | Not deployed |

### Fonts and data

| Component | License | Role |
| --- | --- | --- |
| Inter and JetBrains Mono (bundled fonts) | OFL-1.1 | Agent web app |
| caniuse-lite | CC-BY-4.0 | Build-time browser data |

## By count

Counting the entries above: 31 are Apache-2.0 and 14 are MIT (plus the many MIT
web UI packages), with a handful each of BSD-style, MPL-2.0, GPL, AGPL and
source-available licenses. The rows that need care are Grafana (AGPL), Redis 8
(tri-licensed) and the GPL applications, all covered above.

## How this list was made and kept honest

The list comes from the Argo CD apps in `pods/`, the packages and charts the
bootstrap playbooks install, the container images the manifests name, and the
agent, installer, embedding and auth dependency manifests. A dependency that
only appears deeper in a package tree is not listed individually. When you add or
change a component, update this page in the same pull request.
