# Adaetum public roadmap

This roadmap is maintained by maintainers and converted into focused issues.
An item is complete only when its acceptance criteria and required checks pass.

## P0 — platform contract

- [ ] Finish the shared Phase 50/60 bootstrap library. **Current:** exact
  duplicate helpers (48 at the time of this audit) live in
  `control-pair-common.sh`, and a validator prevents their return.
  **Acceptance:** thin phase entrypoints retain distinct install/handoff
  behavior and regression checks.
- [x] Complete `platform.yaml` ownership. **Current:** `task platform:setup`
  renders profile-owned public values into generated outputs while retaining
  `.env` for runtime secrets, carries the contract marker in the break-glass
  runtime payload, and revalidates the bundled profile in Phase 10.
  **Acceptance:** public platform values are derived only from `platform.yaml`;
  the generated cluster config is an internal render input, never a user-owned
  source of truth.
- [x] Add profile schema, rendering, and negative validation checks.
  **Evidence:** the profile validator self-test rejects unsupported installers,
  unknown fields, and secret-shaped fields; the pods round-trip validator checks
  profile-to-manifest rendering.
- [x] Validate repository-owned documentation links locally and in CI.
  **Acceptance:** moved, renamed, or removed local documentation targets fail a
  deterministic check without requiring network access.
- [ ] Add ShellCheck, actionlint, yamllint, ansible-lint, Ruff, Gitleaks, and
  kubeconform to local hooks and CI. **Acceptance:** documented pinned checks
  run on pull requests.
- [ ] Complete the OpenBao workload-secret handoff. **Current:** External
  Secrets now reconciles every identified application delivery Secret, and
  Reloader rolls stateless consumers. Bootstrap still creates temporary copies
  before OpenBao is available. Successful break-glass and join-node bundle
  runs now remove all secret-bearing first-boot environment files, rendered
  scripts, and installed kickstart copies; failed runs retain them only for
  explicit resume. The GitHub push-mirror hook now reads an
  OpenBao-backed projected Secret for every push rather than retaining a token
  on Gitea's repository PVC. **Acceptance:** clean-install evidence proves
  adoption of temporary copies, every credential remains classified as
  OpenBao-, provider-, recovery-plane-, application-, RKE2-, or
  Kubernetes-owned, no workload treats a generated Kubernetes Secret as
  authority, and live recovery-mirror evidence proves credential replacement
  converges without replaying bootstrap. RKE2 token evidence must exercise its
  native rotation procedure rather than treating an OpenBao recovery-copy edit
  as rotation.
- [ ] Add coordinated rotation for stateful application credentials.
  **Dependencies:** the OpenBao workload-secret handoff and stable clean-install
  evidence. **Current:** Argo CD, Gitea, Grafana, and Authentik admin passwords
  reconcile through product-native behavior; Gitea/PostgreSQL uses a two-phase
  database-first rotation for both Gitea and Authentik, and Rancher's native
  password-change request promotes delivery only after login validation.
  Gitea and Grafana encryption roots are OpenBao-owned and migration-gated
  instead of being treated as ordinary restartable passwords. Authentik's
  post-2023.6 signing key now rotates normally and intentionally revokes
  sessions without changing persisted identities.
  Argo CD's ephemeral Redis password is OpenBao-owned and reloads the cache and
  all clients together.
  **Acceptance:** recovery tests prove that encryption keys are never blindly
  replaced and exercise interrupted rotation recovery.
- [x] Remove unused provider credentials from Homepage. Cloudflare and
  Tailscale are configured as links rather than API widgets, and Authentik is
  also link/status-only, so its shared widget Secret now receives only the two
  tokens its Argo CD and Gitea widgets consume. Validation rejects delivery of
  those unused credentials through bootstrap phases or External Secrets; new
  Gitea widget tokens are read-only.
- [ ] Replace Homepage's Grafana administrator password with a dedicated Viewer
  identity and migrate any preexisting broad Gitea widget token. **Acceptance:**
  Homepage can read its configured metrics without holding an administrator
  password or an `all`-scope Gitea token, and both credentials have tested
  OpenBao-driven rotation and provider/application revocation paths.
  **Current:** the dedicated Grafana Viewer and safe desired-to-active
  coordinator are implemented. Gitea scope inspection, replacement, and
  post-promotion revocation are also implemented. Live rotation and migration
  evidence remain.

## P1 — supported platform alpha

- [ ] Finish Rocky 10 clean-install, recovery, and fork-transition evidence.
- [ ] Document and test the Cloudflare, GitHub, and Tailscale bootstrap
  boundaries. **Current:** [integration boundaries](docs/integrations.md)
  documents secret inputs, state mutations, recovery, and failure behavior.
  **Acceptance:** provider-side validation is exercised for each integration.
- [ ] Publish platform alpha release notes, fork recovery instructions, and a
  support matrix.
- [ ] Enable Ubuntu only after installer, validation, documentation, and
  recovery parity are demonstrated.
- [ ] Promote advisory quality checks to required after their current baseline
  is remediated. **Acceptance:** ShellCheck, actionlint, yamllint, ansible-lint,
  Ruff, Gitleaks, and kubeconform are green in CI without exceptions.

## P2 — community scale

- [ ] Publish contributor onboarding and a good-first-issue backlog.
- [ ] Establish maintainer rotation, security response drills, and release
  cadence.
- [ ] Reconsider pluggable providers only if Adaetum supports and validates a real
  second implementation for a documented external integration.

## Issue labels

Use `area/bootstrap`, `area/config`, `area/integrations`, `area/docs`,
`area/community`, `kind/bug`, `kind/feature`, `good first issue`, and
`help wanted` for public work tracking.
