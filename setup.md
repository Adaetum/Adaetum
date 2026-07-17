# Setup Guide

Use this guide for the supported first-time setup path from a fresh checkout.
Run `task init` from repo root and let the guided walkthrough prepare
provider accounts, generate `.env`, validate credentials, upload required
artifacts, and trigger the bootstrap workflows.

> [!TIP]
> `task init` is the supported first-time setup path. It collects and reviews
> the public platform profile, finds or downloads verified installer media,
> guides provider authorization, and runs readiness validation before bootstrap
> begins. Use `task initialize` only to rerun setup after first-run.
> After Cloudflare authorization, the walkthrough lists the zones visible to
> that token and lets you select a zone root or a cluster subdomain; it does
> not require you to type a Cloudflare domain blindly.
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

The console presents one five-section journey from beginning to end:

1. **Repository** creates or verifies the standalone private GitHub recovery repository.
2. **Providers** authorizes Cloudflare and Tailscale and discovers their public
   choices.
3. **Profile** reviews and writes the two public cluster values.
4. **Installer** finds or downloads verified Rocky Linux media and runs
   readiness checks.
5. **Bootstrap** validates captured inputs, renders configuration, publishes
   bootstrap artifacts, and finalizes the installer.

The four targeted `task initialize` rerun steps are shown as milestones 5.1
through 5.4 when they run inside first-run. Embedded publishing work is nested
beneath milestone 5.3 instead of introducing another top-level progress model.

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
Setup publishes `main` as the default workflow branch because Adaetum's Actions
triggers target `main`. If setup is launched from another branch, that development
branch is also published and remains checked out locally.
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
`task init` additionally installs [Gum](https://github.com/charmbracelet/gum)
when Homebrew, DNF, Pacman, or Windows winget can provide it. On another
platform, install Gum from its official instructions and rerun `task init`.
Docker is optional and is only needed for the local `task build-iso` path.

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
from the existing `.env`. Newly validated values replace the protected local
entries, generated `.env`, and GitHub environment secrets. It deliberately
retains the GitHub CLI login, private recovery repository, and verified Rocky
installer media because those are setup infrastructure rather than saved
provider values.

### Terminal experience

`task init` requires Gum because its account-readiness walkthrough is the
supported first-run experience. Once Gum is installed, setup prompts, recovery
menus, and password entry use it automatically when attached to a terminal.
Gum does not hold configuration or secrets: `platform.yaml`, `.env`, and the
existing setup scripts keep their respective ownership boundaries. To use the
plain prompts on any supported path, set `ADAETUM_GUM_UI=0`:

```bash
ADAETUM_GUM_UI=0 task initialize
```

To see the complete first-run journey before committing local or provider
state, run `task init:dryrun`. It does not install helpers, authenticate to
GitHub, create a repository, change `origin`, collect secrets, render files, upload
artifacts, or trigger workflows. It remains interactive for every decision and
uses fixture credentials behind the normal hidden prompts. It runs the same
read-only setup preflight as `task init`, so a reported blocker stops both
commands.

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

The walkthrough opens <https://dash.cloudflare.com/profile/api-tokens>, which
provides the account-token link without hardcoding an operator's Cloudflare
account ID. Cloudflare then routes to an account-specific address such as
`https://dash.cloudflare.com/<account-id>/api-tokens/create`.

- Account: `Connectivity Directory` → `Read`, `Bind`, and `Admin`
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
credential. This is the known-working permission set confirmed against the
current Account API Token UI. It is intentionally documented as a validated
baseline rather than audited least privilege until provider regression testing
proves which individual Read and Connectivity Directory permissions can be
removed. User-owned tokens remain a compatibility path, but setup warns before
accepting one. First-run inspects the token policy before creating or updating
provider resources.

After validating the token, first-run offers to save it for interrupted setup
resumption. The default is Yes. Adaetum uses macOS Login Keychain, Linux
Secret Service, or a Windows current-user DPAPI-protected store when available
and never falls back to a plaintext credential file. The saved value is keyed
to the selected private repository and is validated before every reuse; an invalid entry is
removed automatically.

The local protected copy exists only to resume an interrupted walkthrough. A
successful real `task init` also syncs `CLOUDFLARE_API_TOKEN` and the other
required runtime credentials to the private repository's `Prod` GitHub environment. Setup
stops if that required secret sync fails; dry-run never performs it.

</details>

### Tailscale

- `TAILSCALE_USER_API_TOKEN`
  A temporary, person-bound setup credential used to discover MagicDNS from
  device DNS names, validate tag policy, and establish the durable enrollment identity. It is
  discarded after bootstrap and is not stored in `.env`, `platform.yaml`, or
  GitHub secrets. Create it with a **1-day expiration**, which covers setup and
  a retry without leaving a durable user credential. With explicit approval,
  Adaetum may keep it temporarily in the OS credential store so a cancelled
  walkthrough can resume; it is removed automatically when rejected or expired.
- `TAILSCALE_OAUTH_CLIENT_ID`
  Used for the long-term Tailscale OAuth credential.
- `TAILSCALE_OAUTH_CLIENT_SECRET`
  Used for the long-term Tailscale OAuth credential.

The OAuth client ID and secret are synchronized to the private repository's `Prod` GitHub
environment. Setup validates that identity by minting the first node's tagged,
non-reusable auth key. That key is saved in the gitignored local `.env` for the
installer build and synchronized as `TAILSCALE_AUTHKEY` to the same GitHub
environment. The OAuth client can mint new enrollment credentials later,
avoiding a dependency on one person's continuing tailnet membership.

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
it immediately, validates it, and offers OS-protected resume storage. No manual
OAuth form or browser automation is involved.

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
task init
```

> [!NOTE]
> `task init` is the interactive first-run entrypoint. It configures and reviews
> `platform.yaml`, obtains supported Rocky installer media, guides provider
> authorization inline, runs `task setup:preflight`, and carries the work
> through the five console sections above. Rerun `task initialize` when you only
> need to regenerate `.env`, retry uploads, or re-trigger the workflow portion.

### Bootstrap milestone 5.1: Validate captured inputs

Setup collects provider credentials only for the active process, validates
them, and syncs them to the private repository's GitHub environment. It does not create a
persistent local prompt-answer cache.

### Bootstrap milestone 5.2: Render and publish configuration

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

### Bootstrap milestone 5.3: Publish bootstrap artifacts

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

### Bootstrap milestone 5.4: Finalize the installer

Setup waits for any background ISO build to finish, verifies the result, and
prints the exact path to the automated installer under `dist/`. Attach that ISO
to the target physical host or VM and boot from it once. Rocky Linux installs
unattended; detach or eject the ISO when the installer reboots so the machine
starts from disk. First-boot cluster preparation then continues automatically
and normally takes roughly 30 minutes for the initial node.

<details>
<summary>Targeted reruns</summary>

If you only need one bootstrap milestone, rerun its underlying
`task initialize` step number:

```bash
SETUP_STEP=1 task initialize
SETUP_STEP=2 task initialize
SETUP_STEP=3 task initialize
SETUP_STEP=4 task initialize
```

Supported step meanings:

- `1`: collect setup inputs only
- `2`: validate Tailscale OAuth and regenerate `.env`
- `3`: run the embedded bootstrap automation flow
- `4`: finalize the installer and print the host handoff instructions

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
