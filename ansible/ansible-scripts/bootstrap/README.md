# Bootstrap

This directory holds the scripts and runbooks used to bring up the first server
from bare metal and establish the initial platform stack. All bootstrap secrets
are generated locally on the first server and are disposable.

## Phases (summary)

- Phase 10: Public bootstrap artifacts (no secrets)
- Phase 20: Break-glass first server + local secret generation
- Phase 30: Automation without authority (RKE2, Rancher, Gitea; Argo CD optional and typically deferred)
- Phase 40: Introduce OpenBao (authority layer)
- Phase 50: Install the minimal Argo CD + Gitea control pair
- Phase 60: Seed the repo and complete GitOps handoff
- Phase 70: GitOps realization gate
- Phase 90: Late live-state reconciliation
- Phase 99: Destroy bootstrap authority and export the recovery kit

## Scripts

- `Phase-10/run-phase10.sh`: Validate Phase 10 artifacts and tooling (no secrets).
  The supported first-boot path expects Homebrew to have been provisioned at OS
  install time. `bundle-bootstrap` ensures `task` and `kubectl` are available
  via Homebrew before Phase 10 runs, and Phase 10 then executes the bundle-safe
  `task` validation targets only.
- `Phase-20/run-phase20.sh`: Generate local bootstrap secrets.
- `Phase-30/run-phase30.sh`: Run Ansible to install the platform components.
- `Phase-40/run-phase40.sh`: Initialize OpenBao and apply post-OpenBao config.
- `Phase-50/run-phase50.sh`: Install the minimal Argo CD + Gitea control pair. Optional envs
  `INGRESS_INTERNAL_VIP`, `INGRESS_EXTERNAL_VIP` pin ingress VIP settings and
  persist them for recovery export (otherwise kube-vip can DHCP a VIP later).
  `CLUSTER_LOCAL_DOMAIN` is optional and defaults to a
  `.local` form derived from `CLUSTER_DOMAIN` (for example, `example.services`
  becomes `example.local`).
  Authentik's local bootstrap backup credentials are authoritative: the
  generated `authentik_admin_username` / `authentik_admin_password` files are
  written first, synced into OpenBao and `authentik-secrets`, and then left as
  the desired value. The live `akadmin` password is applied once in Phase 90,
  after Authentik exists, using the local backup files when available and
  falling back to OpenBao `secret/bootstrap/platform` after Phase 99 removes the
  local bootstrap secret directory.
- `Phase-60/run-phase60.sh`: Seed the repo and complete the Gitea -> Argo GitOps handoff.
  For the opinionated setup path, Phase 60 source-repo seeding can reuse the
  setup `GITHUB_SYNC_TOKEN` when that token is a normal git-capable GitHub
  token; separate GitHub App credentials remain optional rather than mandatory
  for that workflow.
  `PHASE60_MODE=handoff` exits after repo seed and Argo handoff,
  `PHASE60_MODE=realize PHASE60_RECONCILE_ONLY=1` runs the warning-only
  realization path used by Phase 70, and `PHASE60_MODE=full` runs both in one
  invocation.
- `Phase-70/run-phase70.sh`: Prove GitOps is usable and stable, including the
  ansible-runner image/pull path and critical realization checks.
- `Phase-90/run-phase90.sh`: Reconcile the late live-state credentials that can
  only be finalized once the applications are actually running. This includes
  Authentik and Grafana admin-password alignment plus Homepage widget token
  mint/validate/writeback to OpenBao and rendered workload secrets.
- `Phase-90/run-phase99.sh`: Export the recovery kit and remove bootstrap-local authority.
- `bundle-bootstrap`: Default first-boot orchestrator for the full bootstrap chain.
  It is the only supported operator entrypoint and expects the OS install layer
  to have already provisioned Homebrew for first-boot tooling.

Per-phase logs:
- `bundle-bootstrap` writes a separate log per step under
  `$(dirname "$BUNDLE_BOOTSTRAP_LOG_FILE")/bootstrap-phases/` by default.
- Override with `BOOTSTRAP_PHASE_LOG_DIR`.

Failure analytics:
- Bootstrap now emits structured JSONL diagnostics alongside human logs by default.
- Default artifact root: `$(dirname "$BUNDLE_BOOTSTRAP_LOG_FILE")/bootstrap-phases/diagnostics/`.
- The JSONL file is keyed by `BOOTSTRAP_RUN_ID` and records fields such as
  `phase`, `step`, `component`, `operation`, `severity`, `exit_code`,
  `duration_seconds`, `failure_kind`, `resource_ref`, `summary`, and linked
  `evidence_paths`.
- Control env vars:
  - `BOOTSTRAP_DIAGNOSTICS_ENABLED=1`
  - `BOOTSTRAP_DIAGNOSTICS_JSON_ENABLED=1`
  - `BOOTSTRAP_DIAGNOSTICS_DIR=/var/log/bootstrap/diagnostics`
  - `BOOTSTRAP_DIAGNOSTICS_STDOUT_EXCERPT_MAX=1200`
  - `BOOTSTRAP_DIAGNOSTICS_STDERR_EXCERPT_MAX=1200`
- Common `failure_kind` values include `chart-fetch`, `image-pull`, `dns`,
  `http`, `rollout`, `kubernetes-api`, `auth`, `secret`, `timeout`, and `config`.
- Large failure captures live under the diagnostics evidence directory rather
  than as standalone component-specific debug logs.

## Phase State (Optimized Re-Runs)

`ansible/ansible-scripts/bundle-bootstrap` creates local
"done" markers so a re-run after a partial failure can skip phases that already completed.

- Default state dir: `/var/lib/bootstrap-phase-state`
- Control env vars:
  - `BOOTSTRAP_SKIP_DONE=1` (default): skip phases with a done marker
  - `BOOTSTRAP_FORCE=1`: re-run even if a done marker exists
  - `BOOTSTRAP_RESET_STATE=1`: delete the state dir before running

## Expected secret directory

By default, secrets are written to `/var/lib/bootstrap-secrets` with `0700`
permissions. Override with `BOOTSTRAP_SECRET_DIR`.

## Usage (break-glass)

1) Run the full bootstrap orchestrator:

```bash
sudo BOOTSTRAP_OPENBAO_AUTO_UNSEAL=1 \
  BOOTSTRAP_BACKUP_TO_R2=1 BOOTSTRAP_BACKUP_URL="https://bootstrap.../logs?token=REDACTED" \
  BOOTSTRAP_BACKUP_PASSPHRASE="REDACTED" \
  ansible/ansible-scripts/bundle-bootstrap
```

Note: `bundle-bootstrap` now runs the full `10 -> 20 -> 30 -> 40 -> 50 -> 60 -> 70 -> 90 -> 99`
chain. Bootstrap completion means validation, GitOps install, handoff, GitOps
realization, late live-state reconciliation, and the final recovery export have run.

The supported first-boot contract is:
- OS install provisions Homebrew in the standard Linux path.
- `bundle-bootstrap` uses Homebrew to ensure `task` and `kubectl` exist before
  Phase 10.
- Phase 10 requires `task`; the old direct validator fallback path is not part
  of the supported workflow.

Phase scripts remain implementation references beside their docs, but the
supported operator path is the bundle orchestrator above.

## Bootstrap endpoints

- Kickstart files and bootstrap logs are served via `https://bootstrap.example.services`.
- Access is token-gated with the shared KS token.
- Example:
  - `https://bootstrap.example.services/ks/rocky10-server.ks?token=REDACTED`
  - `https://bootstrap.example.services/logs?token=REDACTED`

## References

- `wiki/servers/bootstrap-phases.md`
- `wiki/servers/break-glass-rebuild.md`
- `wiki/servers/rotation-and-burn.md`
