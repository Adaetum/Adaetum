# External integration boundaries

Adaetum uses established external services during bootstrap. These integrations
are explicit setup dependencies, not pluggable modules and not replacements for
the product stack in `pods/`.

`platform.yaml` supplies public routing and delivery values. Setup collects
runtime credentials into the gitignored `.env` delivery file and synchronizes
the recovery credentials required by GitHub workflows to the private
repository's environment Secrets. Supported OS credential stores may retain
first-run resume credentials. None of these values may be committed.

After Phase 40, application-consumed credentials are selected from OpenBao and
continuously delivered through External Secrets. Recovery-plane credentials
remain outside OpenBao when they must publish or retrieve installers and
recovery artifacts while the cluster is unavailable. This is a deliberate
availability boundary, not a second source for workload configuration.

## Cloudflare

**Purpose:** artifact delivery through R2, bootstrap-worker publishing, public
DNS, and Cloudflare Tunnel routes for Rancher and the shared ingress path.

**Inputs:** `CLOUDFLARE_API_TOKEN`; optionally an existing
`CLOUDFLARE_ACCOUNT_ID`, R2 credentials, or tunnel values. The bucket and
bootstrap URL come from `platform.yaml`.

**State it may change:** R2 buckets and scoped R2 tokens, tunnel resources and
tokens, DNS records, Worker configuration, and tunnel ingress configuration.
The bootstrap logic reuses compatible existing resources where possible.

**Failure/recovery:** verify token scope, account access, the public domain,
and the Cloudflare API response before rerunning setup. Preserve the private
recovery repository and recovery kit; do not paste generated token values into
issues or logs. Rerunning setup is the supported reconciliation path for
incomplete bootstrap resources.

## GitHub

**Purpose:** seed the recovery repository into the cluster, retain an
out-of-band mirror, store workflow delivery copies of recovery credentials, and
dispatch build/publish workflows.

**Inputs:** `GITHUB_SYNC_TOKEN` is the canonical credential for the
opinionated flow. It needs access to the private repository and must be able to read, clone,
and push the configured repository.

**State it may change:** GitHub repository or environment secrets and workflow
runs. During bootstrap, the in-cluster Gitea repository is seeded from the
private repository; after handoff, Gitea is the operational authority and GitHub is the
out-of-band mirror/recovery copy.

**Failure/recovery:** confirm the token has repository and workflow access,
then rerun the affected setup step. Before dispatching a workflow, commit and
push the relevant recovery repository changes: GitHub Actions runs the pushed repository
state, not uncommitted local files. Do not treat GitHub and Gitea as two
independently writable sources of truth.

## Tailscale

**Purpose:** node access, overlay reachability, bootstrap authentication, and
tag policy required by the cluster join path.

**Inputs:** `TAILSCALE_USER_API_TOKEN` for bootstrap-time API access plus
`TAILSCALE_OAUTH_CLIENT_ID` and `TAILSCALE_OAUTH_CLIENT_SECRET` for ongoing
credential validation and auth-key minting. The tailnet and cluster tag come
from `platform.yaml`.

**State it may change:** the tailnet ACL/tag-owner policy and short-lived or
time-bounded auth keys used by bootstrap. The one-day, non-reusable node auth
key is disposable installer state. The longer-lived OAuth client is copied to
`secret/apps/ansible/tailscale` for the day-two runner to mint replacements. No
Tailscale credential is stored in `platform.yaml`.

**Failure/recovery:** confirm the tailnet name, OAuth scopes, user-token
access, and permission to update tag owners. Rerun setup after correcting the
policy or credentials. If a node must be rebuilt, use the recovery-repository-owned
break-glass path rather than manually editing cluster identity on an existing
node.

## Safe validation order

1. Update the recovery repository's `platform.yaml` and run `task setup:preflight`.
2. Commit and push the recovery repository changes that GitHub workflows must consume.
3. Provide runtime credentials only when setup prompts for them.
4. Review provider-side mutations in the relevant service UI before proceeding
   with first-node installation.

The [release evidence](release-evidence.md) checklist requires integration
validation before a supported release claim.
