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
