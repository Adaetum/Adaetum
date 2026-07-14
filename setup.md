# Setup Guide

Use this guide for the supported first-time setup path. Run `task initialize`
from repo root and let the installer generate `.env`, validate credentials,
upload required artifacts, and trigger the bootstrap workflows.

> [!TIP]
> `task initialize` is the supported first-time setup path. Prefer it over
> manual `.env` editing unless you are debugging a specific setup step.
>
> This workflow is opinionated on purpose. Follow the defaults unless you are
> deliberately testing or recovering a specific step.

## Overview

This setup flow is the supported first-time path for bringing up the bootstrap
artifacts and configuration needed for a new cluster install.

At a high level, `task initialize` does four things:

1. Verifies local prerequisites and collects the runtime credentials it needs.
2. Validates Tailscale OAuth credentials, generates a fresh `.env`, and writes
   the committed `pods/cluster-config/cluster-config.env` manifest config plus the
   rendered Argo/bootstrap files derived from it.
3. Uploads bootstrap artifacts, syncs secrets, and triggers the required
   GitHub workflows.
4. Prints a final summary and waits for any background ISO build to finish.

> [!NOTE]
> The goal of this guide is to help you move straight through the first setup
> pass without bouncing between task docs, token references, and workflow
> details.

## What you need

Before you run setup, make sure these prerequisites are in place:

1. Clone this repository locally and work from the repo root.

```bash
git clone <your-fork-or-repo-url>
cd Cluster
```

Your fork is the out-of-band configuration and recovery copy. `task initialize`
builds the break-glass materials from it; bootstrap then seeds the newly created
cluster's Gitea repository. After that handoff, make routine day-2 changes in
the in-cluster Gitea repository and let Argo CD reconcile them.

| Requirement | What to use |
| --- | --- |
| Task runner | Install `task`: <https://taskfile.dev/docs/installation> |
| Python runtime | Install Python 3 and PyYAML: `python3 -m pip install pyyaml` |
| Docker (optional) | Install Docker if you want to build the install ISO locally instead of downloading or reusing the copy from R2 |
| Installer media | Download Rocky Linux 10 `Minimal`: <https://rockylinux.org/download> and place the ISO in repo root |
| Repo access | GitHub admin access to your fork or target repo |
| Edge/bootstrap access | Cloudflare access for Workers and R2 |
| Tailnet access | Tailscale access to the target tailnet |

The setup workflow also checks for or installs supporting tools such as `uv`,
`rclone`, and a 7-Zip command (`7z`, `7za`, or macOS Homebrew's `7zz`). Docker is optional and is only needed for the local
`task build-iso` path.

Run the read-only preflight before adding credentials. It validates the public
profile, local tools, and installer ISO, but intentionally does not inspect or
require any secrets:

```bash
task setup:preflight
```

## Have these values ready

Setup will prompt you for these values during the run.

### GitHub

- `GITHUB_SYNC_TOKEN`
  This is the required GitHub credential for the opinionated setup path. It
  must be a normal git-capable token and is reused for GitHub secret sync,
  workflow dispatch, clone/seed, GitHub push mirroring, and related bootstrap
  repo operations. In the opinionated flow, `ARGOCD_GITHUB_TOKEN`,
  `GITEA_SEED_SOURCE_TOKEN`, and `GITEA_PUSH_MIRROR_TOKEN` are derived
  compatibility outputs from `GITHUB_SYNC_TOKEN`, not separate operator inputs.

<details>
<summary>GitHub token guidance</summary>

- `Actions`: Read and write
- `Administration`: Read and write
- `Contents`: Read and write
- `Environments`: Read and write
- `Metadata`: Read-only
- `Secrets`: Read and write

</details>

### Cloudflare

- `CLOUDFLARE_API_TOKEN`
  Used for Cloudflare bootstrap, R2, and worker-related setup.

<details>
<summary>Cloudflare token guidance</summary>

- `Workers Scripts`: Edit
- `Workers R2 Storage`: Edit
- `Cloudflare Tunnel`: Edit
- `Account Settings`: Read
- `Access: Users`: Edit
- `API Tokens`: Edit
- `Zone`: Read
- `DNS`: Edit
- `Workers Routes`: Edit

</details>

### Tailscale

- `TAILSCALE_USER_API_TOKEN`
  Used for bootstrap-time Tailscale API access.
- `TAILSCALE_OAUTH_CLIENT_ID`
  Used for the long-term Tailscale OAuth credential.
- `TAILSCALE_OAUTH_CLIENT_SECRET`
  Used for the long-term Tailscale OAuth credential.

The target tailnet domain is configured in `platform.yaml`; setup does not
prompt for it.

<details>
<summary>Tailscale token guidance</summary>

- Bootstrap token: `TAILSCALE_USER_API_TOKEN`
- Long-term credentials: `TAILSCALE_OAUTH_CLIENT_ID` and `TAILSCALE_OAUTH_CLIENT_SECRET`
- OAuth scopes should include:
  - `devices:core:read`
  - `devices:posture_attributes`
  - `policy_file`
  - `auth_keys`

</details>

### Fork profile

Before running setup, replace the deliberately safe defaults in
[`platform.yaml`](platform.yaml). This file owns the public cluster shape and
delivery settings; setup prompts only for runtime credentials.

- `spec.cluster.domain`: public base domain used for routed services.
- `spec.cluster.localDomain`: local split-DNS suffix.
- `spec.cluster.overlayDomain` and `overlayClusterTag`: Tailscale identity.
- `spec.cluster.repository`: initial in-cluster Gitea repository identity.
- `spec.delivery.bootstrapBaseUrl` and `r2Bucket`: bootstrap artifact delivery.

Terminology used below:

- `Public Domain`
  The public base domain, for example `cluster-duck.cloud`.
- `FQDN`
  A full public host under that domain, for example
  `argocd.<public-domain>`.
- `Local FQDN`
  The internal split-DNS form of that same host, for example
  `argocd.<public-domain>.local`.

Setup derives the public FQDNs used for bootstrap delivery and Cloudflare
tunnel routing from the profile:

```text
KS_BASE_URL=https://bootstrap.<public-domain>
RANCHER_URL=https://rancher.<public-domain>
REGISTRY_URL=https://registry.<public-domain>
HOME_URL=https://home.<public-domain>
ARGOCD_URL=https://argocd.<public-domain>
GITEA_URL=https://gitea.<public-domain>
AUTHENTIK_URL=https://authentik.<public-domain>
HEADLAMP_URL=https://headlamp.<public-domain>
ALERTMANAGER_URL=https://alertmanager.<public-domain>
GRAFANA_URL=https://grafana.<public-domain>
PROMETHEUS_URL=https://prometheus.<public-domain>
```

The routing model is now:

- standard UI apps are exposed through the nginx ingress front door
- public app access defaults to the shared Cloudflare tunnel allowlist
  and is forwarded to the nginx ingress origin over HTTPS
- local access uses Local FQDNs (`<host>.<public-domain>.local`) pointed at the ingress VIP
- Rancher stays separate as an out-of-band management route and is not part of the shared nginx ingress set

<details>
<summary>Routing model</summary>

Adaetum now uses a single standard app-routing pattern for cluster UIs:

- `ingress-nginx` is the in-cluster front door for the normal routed apps
- internal LAN or VPN access uses Local FQDNs that resolve to the ingress VIP
- public app access rides the shared `cloudflared` tunnel by default and is forwarded to the nginx ingress origin over HTTPS
- Rancher stays intentionally separate as an out-of-band management path and is not folded into the standard nginx-routed app set

In practice, the normal operator-facing UI hosts are:

- `home.<public-domain>` and `home.<public-domain>.local`
- `argocd.<public-domain>` and `argocd.<public-domain>.local`
- `gitea.<public-domain>` and `gitea.<public-domain>.local`
- `authentik.<public-domain>` and `authentik.<public-domain>.local`
- `headlamp.<public-domain>` and `headlamp.<public-domain>.local`
- `alertmanager.<public-domain>` and `alertmanager.<public-domain>.local`
- `grafana.<public-domain>` and `grafana.<public-domain>.local`
- `prometheus.<public-domain>` and `prometheus.<public-domain>.local`
- `registry.<public-domain>` and `registry.<public-domain>.local`

Rancher is the exception:

- `rancher.<public-domain>` remains a dedicated external path and is treated as separate management access
- Homepage includes Rancher as a convenience link, but Rancher is not part of the shared nginx ingress route set

</details>

### Local DNS (.local)

Some UI endpoints use Local FQDNs in the form
`<host>.<public-domain>.local`. These
names are not managed in Cloudflare. Add DNS records in whatever DNS your
clients use. This is only needed for services you do not expose as a public
route, but still wish to access on the same network.

- `argocd.<public-domain>.local`
- `gitea.<public-domain>.local`
- `alertmanager.<public-domain>.local`
- `grafana.<public-domain>.local`
- `prometheus.<public-domain>.local`
- `authentik.<public-domain>.local`
- `headlamp.<public-domain>.local`
- `home.<public-domain>.local`
- `registry.<public-domain>.local`

Recommendation: create a wildcard record like
`*.<public-domain>.local` that points to the ingress virtual IP (VIP). By
default, the VIP is obtained from your DHCP server and then pinned in-cluster
so it stays stable.

<details>
<summary>VIP details (DHCP + discover-then-pin)</summary>

This repo supports a "discover then pin" VIP flow for the ingress front door.
By default, kube-vip requests a VIP from your DHCP server (no manual range
management) and then pins it for reuse.

1. Sync these apps in Argo CD: `pods/ingress/kube-vip.app.yaml`,
   `pods/ingress/ingress-vip.app.yaml`.
2. The controller stores the VIP in `ingress/ingress-vip-config` and
   keeps the authoritative ingress front door Service
   `kube-system/rke2-ingress-nginx-controller` pinned to it.

Read the current VIP:

```bash
kubectl -n ingress get secret ingress-vip-config -o jsonpath='{.data.ingress_external_vip}' | base64 -d
```

Override/pin the VIP:

- The VIP is stored in `ingress/ingress-vip-config` as
  `ingress_internal_vip` and `ingress_external_vip`.
- If those keys are set, the `ingress-vip` controller keeps the ingress front
  door Service `kube-system/rke2-ingress-nginx-controller` pointed at that IP.
- If those keys are empty, the controller sets the Service to request a DHCP
  lease (`loadBalancerIP=0.0.0.0`) via kube-vip, then persists the discovered IP
  back into the Secret so it stays stable.

Override/pin the VIP (example):

```bash
VIP="192.168.1.250"
VIP_B64="$(printf '%s' "${VIP}" | base64 | tr -d '\r\n')"
kubectl -n ingress patch secret ingress-vip-config --type merge \
  -p "{\"data\":{\"ingress_internal_vip\":\"${VIP_B64}\",\"ingress_external_vip\":\"${VIP_B64}\"}}"
```

Optional knobs:

- Set a DHCP lease hostname by updating `ingress_dhcp_hostname` in the same Secret.
- Disable the VIP flow entirely by setting `ingress_service_type=NodePort`.

</details>

`task initialize` renders the committed cluster manifest config in
`pods/cluster-config/cluster-config.env` and re-renders the Argo/bootstrap files
that depend on it. If you need to adjust domains or repo ownership later, update
[`platform.yaml`](platform.yaml) and rerun `task platform:render`; do not
hand-edit generated manifest files.

## Run setup

From repo root:

```bash
task initialize
```

> [!NOTE]
> `task initialize` is a four-step wrapper around the opinionated setup flow.
> It can be rerun safely when you need to regenerate `.env`, retry uploads, or
> re-trigger the workflow portion of setup.

### Step 1: Collect setup inputs

Setup first verifies that a local installer ISO exists in repo root. The
expected path is Rocky Linux 10 Minimal. It then prompts for the setup values
listed above and caches the entered answers locally.

The cache file is `.setup-opinionated.cache.env`, which reruns can reuse.

### Step 2: Generate `.env`

Setup validates the Tailscale OAuth credentials you provided, then regenerates
`.env` for the rest of the workflow. During this step, setup preserves existing
values such as:

- `KS_SHARED_TOKEN`
- `KS_UPLOAD_TOKEN`
- `BOOTSTRAP_BACKUP_PASSPHRASE`
- `BOOTSTRAP_BACKUP_PASSPHRASE_B64`

During this same step, setup also writes `pods/cluster-config/cluster-config.env` and
re-renders the small set of Argo/bootstrap files that depend on concrete repo,
domain, and hostname values committed in the fork.

For the opinionated flow, the local cache file `.setup-opinionated.cache.env`
is the supported source of truth for the repeated setup prompts. If the cached
`SETUP_GITHUB_SYNC_TOKEN` is a normal git-capable GitHub token, setup reuses it
as the canonical opinionated GitHub credential and deterministically rehydrates
the repo-seed compatibility fields during `task initialize`, without asking for
separate GitHub App fields.

By default, setup may also start a local `task build-iso` run in the
background so the local install ISO finishes earlier. That local build path is
optional and uses Docker. If you do not need a locally built ISO, you can rely
on the installer artifacts published through the bootstrap path in R2 instead.

### Step 3: Run bootstrap automation flow

The embedded bootstrap flow performs the operational setup work:

- Ensures a backup passphrase exists
- Uploads local repo-root ISOs to Cloudflare R2 as golden ISOs
- Builds and uploads the break-glass ansible bundle
- Builds and uploads the runtime bootstrap env payload
- Syncs non-empty `.env` values to GitHub secrets
- Warns when GitOps config or workflow-relevant tracked files are still
  uncommitted locally, because GitHub Actions and ISO-installed GitOps state use
  committed repo content rather than your working tree
- Triggers the GitHub workflows used for worker/bootstrap publishing:
  - `ks-worker.yml`
  - `ks-publish.yml`
  - `iso-build.yml`

Repository mirroring note:

- the old GitHub workflow that force-pushed into Gitea is no longer part of the
  supported model
- if you want GitHub kept current, configure a Gitea push mirror from the
  in-cluster repo to GitHub
- do not treat GitHub and Gitea as simultaneously writable primaries; bootstrap
  may seed Gitea from GitHub, but steady-state GitOps should write to Gitea
- bootstrap now defaults the outbound mirror settings from the GitHub seed repo
  values:
  - `GITEA_PUSH_MIRROR_ENABLED`
  - `GITEA_PUSH_MIRROR_REPO_URL`
  - `GITEA_PUSH_MIRROR_USERNAME`
  - `GITEA_PUSH_MIRROR_TOKEN`
- auto-mirror setup only proceeds when the target is a GitHub repo and the
  token looks like a stable PAT; short-lived GitHub App tokens are skipped so
  bootstrap does not install a broken persistent mirror

If workflow waiting is disabled, setup still records the triggered runs and
validates them later in the flow.

### Step 4: Finalize

Setup prints a summary and, if a background local ISO build was started,
waits for it to finish and reports the result.

<details>
<summary>Targeted reruns</summary>

If you only need one installer step, rerun a specific step:

```bash
SETUP_STEP=1 task initialize
SETUP_STEP=2 task initialize
SETUP_STEP=3 task initialize
SETUP_STEP=4 task initialize
```

Supported step meanings:

- `1`: collect and cache setup inputs only
- `2`: validate Tailscale OAuth and regenerate `.env`
- `3`: run the embedded bootstrap automation flow
- `4`: finalize and print the installer summary

If you only want the local ISO build substep:

```bash
SETUP_SUBSTEP=2.2 task initialize
```

That runs the local `task build-iso` path only.

> [!IMPORTANT]
> `SETUP_STEP` and `SETUP_SUBSTEP` are for targeted reruns. Do not set both at
> the same time.

</details>

## Outputs

After a successful run, expect these results:

- `.env` created or refreshed for the repo
- `.setup-opinionated.cache.env` updated with cached prompt answers
- Tailscale bootstrap values validated and written into `.env`
- Local repo-root installer ISO uploaded to R2 as a golden ISO
- Break-glass ansible bundle uploaded for bootstrap delivery
- Runtime bootstrap env payload uploaded for first-boot secret fetch
- GitHub secrets synced from non-empty `.env` values
- Bootstrap workflows triggered
- Local install ISO built when enabled and Docker is available

| Output | Result |
| --- | --- |
| `.env` | Generated runtime configuration for setup and follow-on tasks |
| `.setup-opinionated.cache.env` | Cached answers reused by later reruns |
| Golden ISO upload | Local repo-root ISO copied to R2 |
| Break-glass bundle | Bootstrap ansible bundle uploaded for delivery |
| Runtime bootstrap env | First-boot secret payload uploaded to R2/Worker |
| GitHub sync | Non-empty `.env` values pushed to GitHub secrets |
| Workflow dispatch | `ks-worker.yml`, `ks-publish.yml`, and `iso-build.yml` triggered |

Rendered kickstarts are generated during local runs and CI under `dist/ks-templates/`. They are not part of the committed repo contract.

## What to do next

### First node bootstrap

Use the generated local ISO to install the first node.

The generated ISO now carries only the shared bootstrap Worker token. First
boot fetches the runtime secret payload from the bootstrap Worker, so outbound
network access to `KS_BASE_URL` is required before secret-dependent bootstrap
steps can complete.

> [!IMPORTANT]
> The installer menu defaults to `Install Rocky Linux (Server default)`, but
> for the first node you should select `Install Rocky Linux (Breakglass bootstrap)`
> so that node performs the initial cluster bootstrap.

### Validate the UI and routing baseline

After the first node completes bootstrap and Argo CD finishes reconciling, the
fastest validation path is the cluster portal:

- open `https://home.<public-domain>` from a public route, or
- open `https://home.<public-domain>.local` from LAN/VPN split-DNS
- authenticate with the bootstrap-generated Homepage credential when prompted

That portal is meant to be the baseline smoke test for the platform UIs and
route health. It gives you curated internal and public links plus status
checks for the known services.

At minimum, confirm these cards load and show healthy where expected:

- `Portal (internal)` and `Portal (public)`
- `Argo CD`
- `Gitea`
- `Authentik`
- `Headlamp`
- `Alertmanager`
- `Grafana`
- `Prometheus`
- `Registry`
- `Rancher` as a convenience-only external link

If `home.<public-domain>` is up and the service cards are green, you have a quick
end-to-end confirmation that the shared routing layer, public/internal hosts,
and baseline UIs are wired correctly. The Rancher card is useful as a
management-path spot check, but it intentionally follows its own external route
instead of the shared nginx ingress pattern.

Homepage is treated like the other operator-facing routed UIs and is expected
to redirect through Authentik for login rather than using a separate basic-auth
credential.

### Additional nodes

After the first node is up:

- Use `Install Rocky Linux (Server default)` to add another server node that
  participates in cluster failover.
- Use `Install Rocky Linux (Agent join)` to add a capacity node that runs
  workloads but does not participate in cluster failover. This is usually the
  right choice when you are adding a seventh node or addressing other
  high-traffic bottlenecks in internal server communication.

For broader platform behavior and recovery/runbook details, see
[README.md](README.md), the [architecture audit](docs/audit.md), and the
[platform profile](platform.yaml).
