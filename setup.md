# Setup Guide

Use this guide for the supported first-time setup path from a fresh checkout.
Run `task init` from repo root and let the setup window prepare provider
accounts, generate `.env`, validate credentials, upload required artifacts, and
trigger the bootstrap workflows.

> [!TIP]
> `task init` is the supported first-time setup path. It collects and reviews
> the public platform profile, finds or downloads verified installer media,
> guides provider authorization, and runs readiness validation before bootstrap
> begins. Use `task initialize` only to rerun setup after first-run.
> After Cloudflare authorization, the window lists the zones visible to that
> token and lets you choose one; it does not require you to type a Cloudflare
> domain blindly.
>
> This workflow is opinionated on purpose. Follow the defaults unless you are
> deliberately testing or recovering a specific step.

## Maintainer cleanup before an upstream commit

After testing with real domains, run `task clean` before proposing the setup
changes to Adaetum upstream. It restores the committed public-safe
`platform.yaml`, regenerates every tracked profile-owned manifest, and removes
rebuildable installer outputs. It does not delete the gitignored `.env` or
OS-protected resume credentials; those remain local and cannot enter the Git
diff.

Verify the handoff with `git diff --check` and the repository hooks before
committing.

## Overview

This setup flow is the supported first-time path for bringing up the bootstrap
artifacts and configuration needed for a new cluster install.

The setup window presents one journey of eight steps, in this order:

1. **Private recovery repository** creates or verifies the standalone private GitHub repository.
2. **Cloudflare and Tailscale access** authorizes both providers, chooses your DNS zone and
   tailnet, and creates the scoped OAuth clients.
3. **Public platform profile** reviews and writes the two public cluster values.
4. **Rocky installer media** finds or downloads verified media and runs the readiness check.
5. **Validate captured inputs** checks that everything the rest of setup needs is present.
6. **Render and publish configuration** writes `.env`, syncs secrets, renders the GitOps files
   and pushes them to your private repository.
7. **Publish bootstrap artifacts** uploads installers and the bundle to R2, triggers the
   workflows and builds the installer ISO.
8. **Finalize installer** confirms the ISO is fresh and offers a copy in Downloads.

The same eight steps run with no window through `task initialize`; see "Terminal
experience" below.

> [!NOTE]
> The goal of this guide is to help you move straight through the first setup
> pass without bouncing between task docs, token references, and workflow
> details.

## What you need

Clone Adaetum and run `task init` from the repository root. The walkthrough
creates or reuses your private recovery repository, so cloning upstream is
supported.

```bash
git clone https://github.com/Adaetum/Adaetum.git
cd Adaetum
```

`task init` checks `origin` before it asks for credentials. If this checkout
points at canonical `github.com/Adaetum/Adaetum`, it installs GitHub CLI,
opens GitHub's browser authentication when needed, and uses that session as the
default setup credential. It then creates or reuses a standalone private
recovery repository and updates this checkout's `origin`. You confirm before
that external action. Setup can then push rendered configuration, sync secrets,
and trigger workflows against that private repository.

Setup reuses the current local checkout rather than cloning another directory.
Canonical Adaetum is retained as the public `upstream` remote and the selected
private repository becomes `origin`. GitHub requires forks of public
repositories to remain public, so setup never uses a public fork as the cluster
recovery store.
Setup always finishes on `main` and publishes only that branch as the recovery
and workflow authority. When an existing recovery repository is selected,
setup merges canonical public `upstream/main` into the private `origin/main`
history, replaces every tracked path from the public tree, restores the private
`platform.yaml`, and re-renders its tracked outputs. This prevents a retired
development branch or stale private file from surviving under the name `main`.
Untracked and ignored recovery material is not changed, and old local branch
refs remain available until the operator deliberately removes them. Setup
refuses the refresh over tracked worktree changes or unpublished local-main
commits.
If an older Adaetum setup already synchronized environment secrets to a public
fork, remove those secrets and rotate the provider credentials after the private
repository is ready; GitHub does not permit reading the old secret values back.
If `owner/Adaetum-cluster` already exists, the walkthrough verifies that it is a
standalone private Adaetum repository or suggests another available name. A
valid existing GitHub CLI login and its token are reused instead of requesting
another device login.
If GitHub explicitly rejects the stored token, setup offers the CLI refresh
flow. A temporary GitHub API failure is retried and reported as an availability
problem; it never causes setup to replace a stored credential.

Your private repository is the out-of-band configuration and recovery copy.
`task init` builds the break-glass materials from it; bootstrap then seeds the newly created
cluster's Gitea repository. After that handoff, make routine day-2 changes in
the in-cluster Gitea repository and let Argo CD reconcile them.

| Requirement | What to use |
| --- | --- |
| Task runner | Install `task`: <https://taskfile.dev/docs/installation> |
| Python runtime | Install Python 3 and PyYAML: `python3 -m pip install pyyaml` |
| Docker (optional) | Install Docker if you want to build the install ISO locally instead of downloading or reusing the copy from R2 |
| Installer media | `task init` finds or downloads and verifies Rocky Linux 10 Minimal or DVD media |
| Repo access | GitHub admin access to the private recovery repository |
| Edge/bootstrap access | Cloudflare access for Workers and R2 |
| Tailnet access | Tailscale access to the target tailnet |

The setup workflow also checks for or installs supporting tools such as `uv`,
`rclone`, and a 7-Zip command (`7z`, `7za`, or macOS Homebrew's `7zz`).
`task init` additionally installs the [GitHub CLI](https://cli.github.com/) and
builds the setup window from `installer/`, which needs Go and Node (the Wails build tool
is installed with `go install` if it is missing). Docker is optional and is only needed for the local `task build-iso` path.

Adaetum accepts Rocky 10 Minimal and DVD media at this boundary. `task init`
finds matching media in the checkout and common download locations. A single
supported ISO already in the checkout is verified and reused automatically,
without showing the download questions. Multiple discovered ISOs produce one
selection menu; media outside the checkout requires confirmation before it is
copied. If none is present, setup asks for the release, image type, and target architecture. The
newest supported release, Minimal image, and detected host architecture are
the first/default choices. The menu labels Minimal and DVD as offline
installers; choose DVD when the target needs the complete package repository.
Rocky's separately named Boot ISO is labeled as an online installer and is
excluded because it downloads packages during installation rather than
carrying the local package source required by Adaetum's kickstart. Minimal and
DVD are both bootable installer images.
Previously downloaded media is reused after checksum verification. Interrupted
downloads remain as `.partial` files and resume the next time setup runs.
You can also run this explicitly:

```bash
task iso:download                 # x86_64 (default)
ROCKY_ARCH=aarch64 task iso:download
ROCKY_IMAGE_TYPE=dvd task iso:download
```

### Replace saved setup values

Use `task init:clean` when testing with different provider credentials or when
the saved setup state should be replaced. It follows the normal guided flow but
does not read Adaetum credentials from the OS credential store or runtime values
from the existing `.env`. Newly validated values replace the generated `.env`
and GitHub environment secrets. It deliberately
retains the GitHub CLI login, private recovery repository, and verified Rocky
installer media because those are setup infrastructure rather than saved
provider values.

### Replay saved setup state

After completing `task init` once, `task init:silent` reruns setup with no window and no
questions. It can reuse credentials saved by earlier versions in your operating system's credential store and
the values already in `platform.yaml` and `.env`, and it never creates anything new on its
own: it will not create a repository or start an installer download, because those need an
operator's decision. (`task initialize` runs the same stages without that restriction.)

Adaetum deliberately does not create an answers file. Public configuration remains in
`platform.yaml`; Git owns the recovery destination; Cloudflare and Tailscale credentials
may remain in an existing OS credential store; and `.env` remains a gitignored runtime output. New
credentials are not saved to the OS credential store. If a value
is missing, expired, or no longer authorized, or a question has several choices and no
default (which DNS zone, which tailnet, which ISO), the run stops and tells you what it
needs instead of guessing.

Answer a question with `task initialize -- --answer id=value`, for example
`--answer cloudflare-zone=example.com`. A secret is never accepted that way, because command
lines are visible to other users of the machine: set the question's environment variable
instead, `ADAETUM_SECRET_<ID>` with dashes as underscores, for example
`ADAETUM_SECRET_CLOUDFLARE_TOKEN`. `task init:dryrun` refuses `--apply`.

### Terminal experience

`task init` opens a window, so it needs a display. On a machine without one, run the same
stages with `task initialize`; it asks nothing and stops at the first question that has no
answer, naming the answer it wants. Both read and write the same state.

`task init:dryrun` is the way to see the whole journey before committing local or provider
state. It does not authenticate to GitHub, create a repository, change `origin`, read secrets,
render files, upload artifacts, or trigger workflows. Each step reports whether it still has
work to do and what it would do, and a blocked step (for example, no `gh` installed) is shown
instead of stopping the list.

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

Cloudflare uses **account** for the workspace that owns R2 buckets, Workers,
and Tunnels. A **zone** is one public base domain managed in Cloudflare DNS.
`task init` lists zones together with their owning account and carries that
exact account ID into bootstrap; it does not default to the first account
visible to the token.

<details>
<summary>Cloudflare token guidance</summary>

Use a Cloudflare **Account API token** so the integration is a durable service
principal instead of being tied to one account member. New account tokens start
with `cfat_`. Creating one requires Super Administrator access. In the target
account, open **Manage Account → Account API Tokens**, create `Adaetum
bootstrap`, and add:

The walkthrough opens an account-token template that pre-fills the permissions
below without hardcoding an operator's Cloudflare account ID. Review the
permissions and resource scope before creating the token.

- Account: `Account API Tokens` → `Read` and `Write`
- Account: `Workers R2 Storage` → `Read` and `Write`
- Account: `Cloudflare Tunnel` → `Write`
- Account: `Workers Scripts` → `Read` and `Write`
- Zone: `Zone` → `Read`
- Zone: `DNS` → `Read` and `Write`
- Zone: `Workers Routes` → `Read` and `Write`

In Account Resources, include only the account that will own the cluster. In
Zone Resources, include only its public domain/zone. The `Account API Tokens`
Write permission allows Adaetum to derive the narrower bucket-scoped R2
credential. This baseline has not been verified against a live account. User-owned
tokens remain a compatibility path, but setup warns before
accepting one. First-run inspects the token policy before creating or updating
provider resources.

Setup holds the token in memory for the current run and does not save it to the
OS credential store. A successful real `task init` also syncs `CLOUDFLARE_API_TOKEN` and the other
required runtime credentials to the private repository's `Prod` GitHub environment. Setup
stops if that required secret sync fails; dry-run never performs it.

</details>

### Tailscale

- `TAILSCALE_USER_API_TOKEN`
  A temporary, person-bound setup credential used to discover MagicDNS from
  device DNS names, validate tag policy, and establish the durable enrollment identity. It is
  discarded after bootstrap and is not stored in `.env`, `platform.yaml`, or
  GitHub secrets. Create it with a **1-day expiration**, which covers setup and
  a retry without leaving a durable user credential. New setup runs do not save
  this token in the OS credential store.
- `TAILSCALE_OAUTH_CLIENT_ID`
  Used for the long-term Tailscale OAuth credential.
- `TAILSCALE_OAUTH_CLIENT_SECRET`
  Used for the long-term Tailscale OAuth credential.
- `TAILSCALE_OPERATOR_OAUTH_CLIENT_ID` / `TAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET`
  A separate least-privilege OAuth client used only by the Tailscale Kubernetes
  Operator. Setup creates it automatically with Auth Keys, Devices Core, and
  Services write scopes and the `tag:k8s-operator` tag.

The OAuth client ID and secret are synchronized to the private repository's `Prod` GitHub
environment. Setup validates that identity by minting the first node's tagged,
non-reusable auth key. That key is saved in the gitignored local `.env` for the
installer build and synchronized as `TAILSCALE_AUTHKEY` to the same GitHub
environment. The OAuth client can mint new enrollment credentials later,
avoiding a dependency on one person's continuing tailnet membership.

### OpenBao restart-time auto-unseal

Adaetum supports OpenBao's built-in Transit seal when an independently operated
OpenBao Transit endpoint is available. This is optional because the Transit
service, encryption key, and scoped token must exist outside the cluster being
unsealed. Configure these runtime values in the gitignored `.env`:

- `OPENBAO_TRANSIT_SEAL_ADDRESS`
- `OPENBAO_TRANSIT_SEAL_TOKEN`
- `OPENBAO_TRANSIT_SEAL_KEY_NAME`
- `OPENBAO_TRANSIT_SEAL_MOUNT_PATH` (defaults to `transit/`)
- `OPENBAO_TRANSIT_SEAL_NAMESPACE` (optional)
- `OPENBAO_TRANSIT_SEAL_TLS_SERVER_NAME` (optional)
- `OPENBAO_TRANSIT_SEAL_CA_CERT_B64` (optional private CA)

The token should be an orphan, renewable token restricted to `update` on the
selected Transit key's `encrypt` and `decrypt` endpoints. Platform bootstrap
rejects a partial configuration and stores the complete connection contract in
bootstrap-owned Kubernetes Secrets rather than OpenBao KV. Fresh installations
initialize with recovery keys and automatically unseal after restarts. Existing
Shamir installations require the explicit, snapshot-backed migration described
in `pods/secrets/openbao/init/README.md`.

After preparing tag ownership, `task init` creates the OAuth client through
Tailscale's keys API with the same shape shown in Trust credentials:

- Description: `Adaetum node enrollment`
- Scopes → Auth Keys: Write
- Scopes → Policy File: Write
- Scopes → Devices → Core: Read
- Scopes → Devices → Posture Attributes: Read
- Tags: `tag:rocky10`, `tag:server`, and `tag:cluster`

The temporary API access token therefore also needs OAuth Keys: Write. Tailscale
returns the OAuth client secret only in the creation response; Adaetum captures
it immediately and validates it. No manual
OAuth form or browser automation is involved.

Setup also creates the Kubernetes Operator client in the same API-driven step.
Its credentials are promoted to `secret/apps/tailscale/operator` in OpenBao and
mounted directly into the operator through Secrets Store CSI; they are never
committed to a Kubernetes manifest.

The first node's auth key is not a separate operator input. Setup mints it with
a **1-day expiration** so the local installer can be built and booted without
rushing, while avoiding a durable enrollment secret. Tailscale invalidates the
one-off key after its first successful use.

The target tailnet domain is configured in `platform.yaml`; setup does not
prompt for it.

For a new or emptied tailnet, the API can return no device DNS names. In that
case `task init` opens Tailscale's DNS page and asks for the displayed
`*.ts.net` tailnet name inline. It does not require adding a throwaway device or
leaving the setup workflow.

The temporary API token also resolves the Tailscale trust bootstrap ordering:
before an OAuth client can mint tagged node keys, the corresponding tag owners
must exist in the tailnet policy. After the tailnet DNS name is known,
`task init` uses the API token to merge the missing Adaetum tag ownership into
the existing policy. The OAuth screen can then offer `tag:rocky10`,
`tag:server`, and `tag:cluster`. Existing policy entries are preserved.

<details>
<summary>Tailscale token guidance</summary>

- Bootstrap token: `TAILSCALE_USER_API_TOKEN`
- Bootstrap token expiration: 1 day
- First-node auth key: generated automatically, non-reusable, expires in 1 day,
  saved to the local `.env` and GitHub `Prod` secrets
- Long-term credentials: `TAILSCALE_OAUTH_CLIENT_ID` and `TAILSCALE_OAUTH_CLIENT_SECRET`
- Temporary token permissions:
  - `devices:core:read`, to discover the MagicDNS tailnet domain from devices
  - `devices:posture_attributes:read`, required by Tailscale for policy access
  - `policy_file` (Write), to validate and prepare tag ownership
  - `oauth_keys` (Write), to create the durable OAuth client
- OAuth client permissions use the same `auth_keys` and `policy_file` Write
  access, their required read dependencies, and the Adaetum node tags.
- OAuth scopes should include:
  - `devices:core:read`
  - `devices:posture_attributes`
  - `policy_file`
  - `auth_keys`

</details>

### Recovery repository profile

`task init` collects and reviews only the two values a normal first cluster
needs: the public cluster domain (selected from the authorized Cloudflare
zones) and the Tailscale MagicDNS domain (selected from the authorized
Tailscale account). It also guides the one-time creation of the Tailscale OAuth
client used for future node enrollment. It writes the two public values to
[`platform.yaml`](platform.yaml). The remaining profile values use Adaetum's
standard defaults: `<public-domain>.local`, `tag:cluster`,
`gitea-admin/cluster`, `https://bootstrap.<public-domain>`, and the `iso` R2
bucket. Credentials remain separate runtime inputs.

- `spec.cluster.domain`: public base domain used for routed services.
- `spec.cluster.localDomain`: local split-DNS suffix.
- `spec.cluster.overlayDomain` and `overlayClusterTag`: Tailscale identity.
- `spec.cluster.repository`: initial in-cluster Gitea repository identity.
- `spec.delivery.bootstrapBaseUrl` and `r2Bucket`: bootstrap artifact delivery.

Terminology used below:

- `Public Domain`
  The public base domain, for example `example.services`.
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
NTFY_URL=https://ntfy.<public-domain>
PROMETHEUS_URL=https://prometheus.<public-domain>
CRAFTY_URL=https://crafty.<public-domain>
```

The routing model is now:

- standard UI apps are exposed through the nginx ingress front door
- public app access defaults to the shared Cloudflare tunnel allowlist
  and is forwarded to the nginx ingress origin over HTTPS
- local access uses Local FQDNs (`<host>.<public-domain>.local`) pointed at the ingress VIP
- tailnet access uses operator-managed HTTPS aliases (`<host>.<tailnet>.ts.net`)
  through a shared two-replica ProxyGroup
- Rancher stays separate as an out-of-band management route and is not part of the shared nginx ingress set

<details>
<summary>Routing model</summary>

Adaetum now uses a single standard app-routing pattern for cluster UIs:

- `ingress-nginx` is the in-cluster front door for the normal routed apps
- internal LAN or VPN access uses Local FQDNs that resolve to the ingress VIP
- Tailscale clients can use the matching MagicDNS service name without LAN DNS;
  these trusted-tailnet aliases bypass Authentik forward-auth
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
- `ntfy.<public-domain>` and `ntfy.<public-domain>.local`
- `prometheus.<public-domain>` and `prometheus.<public-domain>.local`
- `registry.<public-domain>` and `registry.<public-domain>.local`
- `crafty.<public-domain>` and `crafty.<public-domain>.local`

Rancher is the exception:

- `rancher.<public-domain>` remains a dedicated external path and is treated as separate management access
- Homepage includes Rancher as a convenience link, but Rancher is not part of the shared nginx ingress route set
- `rancher.<tailnet>.ts.net` is still available through the Tailscale Operator

Every private route also has an additive Tailscale alias using the same service
label: `argocd`, `openbao`, `gitea`, `registry`, `authentik`, `headlamp`, `home`,
`rancher`, `alertmanager`, `grafana`, `ntfy`, and `prometheus` beneath the
`spec.cluster.overlayDomain` suffix. The existing `.local` and public routes
remain unchanged. Gitea keeps its `.local` canonical URL, so links and registry
authentication challenges generated by Gitea may still refer to `.local` when
the tailnet alias is used.

Crafty demonstrates the non-HTTP variant of this model. Its management panel is
available at `crafty.<public-domain>` and `crafty.<public-domain>.local`; both
use Crafty's normal native administrator login with no MFA by default. The
Bedrock game endpoint
is not an HTTP Ingress: ingress-nginx exposes UDP/19132 on the existing LAN VIP
as `minecraft.<public-domain>.local:19132`, while the Tailscale Operator exposes
the same port as `minecraft.<tailnet>.ts.net:19132` through the shared
ProxyGroup. Cloudflare does not publish either game route.

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
- `ntfy.<public-domain>.local`
- `prometheus.<public-domain>.local`
- `authentik.<public-domain>.local`
- `headlamp.<public-domain>.local`
- `home.<public-domain>.local`
- `registry.<public-domain>.local`
- `minecraft.<public-domain>.local` (Bedrock UDP/19132)
- `crafty.<public-domain>.local`

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
kubectl -n ingress get configmap ingress-vip-config -o jsonpath='{.data.ingress_external_vip}'
```

Override/pin the VIP:

- The VIP is stored in `ingress/ingress-vip-config` as
  `ingress_internal_vip` and `ingress_external_vip`.
- If those keys are set, the `ingress-vip` controller keeps the ingress front
  door Service `kube-system/rke2-ingress-nginx-controller` pointed at that IP.
- If those keys are empty, the controller sets the Service to request a DHCP
  lease (`loadBalancerIP=0.0.0.0`) via kube-vip, then persists the discovered IP
  back into the ConfigMap so it stays stable.

Override/pin the VIP (example):

```bash
VIP="192.168.1.250"
kubectl -n ingress patch configmap ingress-vip-config --type merge \
  -p "{\"data\":{\"ingress_internal_vip\":\"${VIP}\",\"ingress_external_vip\":\"${VIP}\"}}"
```

Optional knobs:

- Set a DHCP lease hostname by updating `ingress_dhcp_hostname` in the same ConfigMap.
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
task init
```

> [!NOTE]
> `task init` is the interactive first-run entrypoint. It configures and reviews
> `platform.yaml`, obtains supported Rocky installer media, guides provider
> authorization inline, runs `task setup:preflight`, and carries the work
> through the eight steps above. Rerun `task initialize` when you only need to
> regenerate `.env`, retry uploads, or re-trigger the workflow portion.

### Step 5: Validate captured inputs

Setup collects provider credentials only for the active process, validates
them, and syncs them to the private repository's GitHub environment. It does not create a
persistent local prompt-answer cache.

### Step 6: Render and publish configuration

Setup validates the Tailscale OAuth credentials you provided, then regenerates
`.env` for the rest of the workflow. During this step, setup preserves existing
values such as:

- `KS_SHARED_TOKEN`
- `KS_UPLOAD_TOKEN`
- `BOOTSTRAP_BACKUP_PASSPHRASE`
- `BOOTSTRAP_BACKUP_PASSPHRASE_B64`

During this same step, setup also writes `pods/cluster-config/cluster-config.env` and
re-renders the small set of Argo/bootstrap files that depend on concrete repo,
domain, and hostname values committed in the private repository.

The GitHub credential obtained through the first-run browser sign-in is reused
for this process as the canonical opinionated credential. Generated runtime
values remain in `.env` only as required by the bootstrap workflow; secrets are
synced to the private repository's GitHub environment for remote recovery.

By default, setup may also start a local `task build-iso` run in the
background so the local install ISO finishes earlier. That local build path is
optional and uses Docker. If you do not need a locally built ISO, you can rely
on the installer artifacts published through the bootstrap path in R2 instead.

### Step 7: Publish bootstrap artifacts

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

### Step 8: Finalize the installer

Setup waits for any background ISO build to finish, verifies the result, and
prints the exact path to the automated installer under `dist/`. Attach that ISO
to the target physical host or VM and boot from it once. Rocky Linux installs
unattended; detach or eject the ISO when the installer reboots so the machine
starts from disk. First-boot cluster preparation then continues automatically
and normally takes roughly 30 minutes for the initial node.

<details>
<summary>Targeted reruns</summary>

To rerun only some stages, name them. The stage ids are `repository`, `providers`, `profile`,
`media`, `validate`, `render`, `artifacts` and `finalize`:

```bash
task initialize -- --only render,artifacts
```

A targeted rerun skips the stages before it, so what they would have gathered in memory (the
Cloudflare and Tailscale credentials, the chosen domain) is not available. A stage that needs
it says so and stops; rerun it together with the stage it depends on (`providers` before
`validate`, `render` and `artifacts`). The local ISO build alone is `task build-iso`.

</details>

## Outputs

After a successful run, expect these results:

- `.env` created or refreshed for the repo
- Tailscale bootstrap values validated and written into `.env`
- Local repo-root installer ISO uploaded to R2 as a golden ISO
- Break-glass ansible bundle uploaded for bootstrap delivery
- Runtime bootstrap env payload uploaded for first-boot secret fetch
- GitHub secrets synced from non-empty `.env` values
- Bootstrap workflows triggered
- Local install ISO built when enabled and Docker is available
- First-run handoff offers to copy the generated machine installer into the
  operator's Downloads folder

| Output | Result |
| --- | --- |
| `.env` | Generated runtime configuration for setup and follow-on tasks |
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

The installer menu defaults to
`Install Rocky Linux (Server - auto start/join, default)`. After the node
enrolls in Tailscale, Adaetum checks every other peer for both the configured
cluster tag and `tag:server`. If none exists, this node creates the cluster
primary. If any matching server exists—even when it is offline or
unreachable—this node enters strict join mode and fails rather than creating a
second cluster. `Install Rocky Linux (Breakglass - force new primary)` remains
the manual recovery override for deliberately creating a replacement primary.

### Validate the UI and routing baseline

After the first node completes bootstrap and Argo CD finishes reconciling, the
fastest validation path is the cluster portal:

- open `https://home.<public-domain>` from a public route, or
- open `https://home.<public-domain>.local` from LAN/VPN split-DNS
- open `https://home.<tailnet>.ts.net` from a Tailscale-connected client
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
- `ntfy`
- `Prometheus`
- `Registry`
- `Rancher` as a convenience-only external link
- `Crafty Controller`

If `home.<public-domain>` is up and the service cards are green, you have a quick
end-to-end confirmation that the shared routing layer, public/internal hosts,
and baseline UIs are wired correctly. The Rancher card is useful as a
management-path spot check, but it intentionally follows its own external route
instead of the shared nginx ingress pattern.

Homepage is treated like the other operator-facing routed UIs and is expected
to redirect through Authentik for login rather than using a separate basic-auth
credential.

ntfy is the deliberate routing exception. Its web UI, PWA, mobile clients, and
publish API share one endpoint, so an Authentik browser redirect would break
native clients. Both ntfy routes instead use its generated administrator and
publisher credentials with anonymous access denied.

Crafty is the fixed-size stateful workload example. It runs one `Recreate`
replica on a Longhorn volume and is deliberately excluded from HPA, VPA, and
descheduler ownership. Crafty uses its native `admin` username with no MFA
requirement, while OpenBao owns its desired password at
`secret/apps/games/crafty/admin`. Secrets Store CSI mounts the credential and a
scoped companion applies it through Crafty's API, so the next successful Phase 99
recovery backup contains the credential Crafty actually accepts. The generated
first-start record remains only as the one-time adoption credential:

```bash
kubectl -n games exec deploy/crafty -- cat /crafty/app/config/default-creds.txt
```

Rotate the password in OpenBao rather than changing it only through Crafty's UI.
The companion observes the mounted replacement, applies it through the native
API, and verifies the new login before reporting ready.

Create a Bedrock server in Crafty on UDP port `19132`. A Tailscale-connected
Bedrock client can then join `minecraft.<tailnet>.ts.net` on port `19132`. LAN
clients use `minecraft.<public-domain>.local:19132`; that name resolves through
the existing `.local` wildcard to the ingress VIP, where ingress-nginx forwards
UDP/19132 to Crafty. No Minecraft game port is published through Cloudflare.

### Additional nodes

After the first node is up:

- Use `Install Rocky Linux (Server - auto start/join, default)` to start the
  first server when the cluster is empty or add another server when one is
  detected. Server nodes participate in cluster failover.
- Use `Install Rocky Linux (Agent - join as worker)` to add a capacity node
  that runs workloads but does not participate in cluster failover. This is
  usually the right choice when you are adding a seventh node or addressing
  other high-traffic bottlenecks in internal server communication.

For broader platform behavior and recovery/runbook details, see
[README.md](README.md), the [architecture audit](docs/audit.md), and the
[platform profile](platform.yaml).
