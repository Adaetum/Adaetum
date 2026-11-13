# Adaetum public roadmap

This roadmap is maintained by maintainers and converted into focused issues.
An item is complete only when its acceptance criteria and required checks pass.

## Needs re-review

- [ ] Re-review every item in the older roadmap below. It was written before
  2026-11-09 and has not been checked against the current repository, so
  completed marks, "Current" notes and evidence claims may be stale. For each
  item: confirm it, update it, or delete it. **Acceptance:** the older roadmap
  is gone from the collapsed block, with each surviving item restated here.

<details>
<summary>Older roadmap (unreviewed, may be stale)</summary>

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

## P1 — supported platform alpha

### Installer — opening the inner sanctum

Owner: installer maintainers. Keep presentation in `installer/app/frontend`;
the Go engine remains the source of truth for setup order and completion.
Use the README's chained operator's book and stone sanctuary as the visual
reference. The scope is the existing setup window and its UI-only preview.

- [x] Restyle the window with charcoal stone, aged brass, oxblood red, and warm
  ivory. Use decorative serif headings with readable form labels and controls.
  **Evidence:** shared styling applied to the live window and UI-only preview;
  frontend and Wails builds pass, and every preview screen fits at 1100 × 760.
- [x] Replace the static book with a dimensional grimoire whose pages visibly
  turn. Establish a convincing cover, spine, page thickness, and turning motion
  in the UI-only preview before connecting it to real setup events.
  **Evidence:** the setup surface is a two-page parchment spread with a leather
  cover, binding, page edges and ribbon; a 3D sheet turns across the spine on
  navigation. Only public chapter headings appear on animated sheets.
- [x] Make the eight setup stages chapters in the sidebar, with a gold marker
  for the current chapter. Keep literal stage names and practical instructions.
- [x] Open the book when setup begins and turn a page when the active stage
  changes. Keep the current spread still while an answer is needed; animate
  transitions without delaying controls or implying that unfinished work passed.
  **Welcome:** a centered closed cover slides to the right before opening into
  the spread, for both real setup and UI-only preview. Reduced motion skips
  this sequence, and leaving preview returns to the closed cover.
- [ ] Illuminate an engraved seal as stages actually complete. Show the final
  opened seal and “Your sanctuary awaits” only on successful completion, beside
  the ISO location and next steps. Distinguish simulated completion in preview.
- [x] Keep the preview entirely local, with sample data, backward navigation,
  chapter selection, and replay so the full animation can be reviewed safely.
- [x] Retain pause and reduced-motion support; reduced motion changes spreads
  immediately. Keep animation decorative and keyboard focus on the active form.
- [x] Verify the welcome screen and preview chapters fit the default 1100 × 760
  window without scrolling. At smaller sizes, preserve readable content and
  accessible actions without horizontal overflow.
- [x] Review page turns, rapid chapter changes, pause/resume, errors,
  cancellation, and completion in the rendered UI. Pass the frontend and Wails
  builds; verify that preview navigation calls no setup or provider operations.
  **Evidence:** browser checks cover preview navigation and completion, forward
  and reverse turns, keyboard focus, pause and reduced motion, and 720 × 520
  layout. Mocked live events exercise stage transitions, token isolation,
  rejected answers and cancellation; real provider execution remains untested.

### Platform readiness

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

</details>

## New work (added 2026-11-09)

### Backup and restore

Not started. **Current (2026-11-09 audit):** no Longhorn backup jobs, Velero, or
etcd snapshot export is configured in `pods/`, `ansible/`, or `platform.yaml`.
The setup already provisions an R2 bucket, so an off-site target exists. The
default should be opinionated and on without configuration.

- [ ] Take nightly off-site backups of cluster state and application volumes
  (Gitea, Authentik, OpenBao, app data) to R2, declared in `platform.yaml` and
  enforced by the existing Ansible/GitOps path. **Acceptance:** a clean install
  produces a first backup with no operator input, and the schedule and
  retention are public, non-secret profile values.
- [ ] Prove restore, not just backup. **Acceptance:** a documented drill
  rebuilds a cluster from the off-site copy and shows the data returned.
- [ ] Prove the OpenBao static seal key survives a rebuild: a new installation
  unseals itself (done, `openbao-static-seal`, copied into the emergency kit by
  Phase 99); the restore drill must reopen restored data with the kit's key.
- [ ] Decide where OpenBao unseal keys and the root of trust are kept so a
  rebuilt cluster can open restored secrets. **Acceptance:** the custody and
  recovery path is documented and exercised in the restore drill.
- [ ] Say so when OpenBao is sealed. A Shamir-sealed OpenBao stays sealed after
  any restart (by design, see `pods/secrets/openbao/README.md`) and every secret
  consumer then fails in its own confusing way; on 2026-11-11 the Tailscale
  operator crash-looped with "no such file /oauth/client_id" for hours. The
  cluster agent should check the seal status (unauthenticated `bao status`) and
  put "OpenBao is sealed: unseal it" at the top of the attention list, with the
  command. **Acceptance:** a restarted, sealed OpenBao appears in the attention
  list within a minute and clears when unsealed.
- [ ] Show protection in the agent web app: a backups card on Overview with
  last success, age and size, a warning when stale, and a restore test.
  **Acceptance:** a missing or failing backup appears in the attention list
  defined in `agent/web/src/lib/attention.ts`.

### Day-2 operations

Owner: agent maintainers. The agent web app is where an operator sees and decides
things; changes still reach the cluster through reviewed GitOps or the existing
Ansible path, never by the agent running commands itself.

- [ ] Updates for RKE2, the host OS, and applications, on the Cluster page next
  to the existing Adaetum agent updates. Every update class gets the same
  controls: manual approval, notify, or automatic, with a release channel.
  Workloads choose either "latest" or a pinned version; the default is a pinned
  version with a notice when a newer one exists. **Acceptance:** a bad RKE2 or
  host upgrade can be rehearsed first and rolled back; the choice is saved as
  agent or `platform.yaml` policy as appropriate and every change is recorded.
- [ ] Pod-change previews. An optional setting, off by default. When on, the
  agent may only open a pull request for a pod change, never merge it, and Argo
  CD (ApplicationSet with the Pull Request generator, against Gitea) deploys the
  PR branch to a temporary preview environment. The operator tests it and then
  merges. Scope is pod changes only; host and RKE2 changes are not previewed
  (host changes already need an explicit toggle). This is a large item and
  needs its own design before work starts. **Design points to settle:** the
  setting is public cluster policy that renders an ApplicationSet, so it
  belongs in `platform.yaml`; "agent cannot merge" is enforced by Gitea (branch
  protection and a token without merge rights), not by agent code; previews are
  namespaced only, with a resource quota, a cap on concurrent previews and an
  automatic expiry; stateful pods start with empty data and previews never
  receive production credentials; pods that touch cluster-wide resources are
  marked not previewable and fall back to a plain pull request; preview
  addresses are private (Tailscale) by default. **Acceptance:** with the
  setting on, a pod change produces a pull request and a working preview link
  in the agent web app, with the preview's health shown; with it off, behavior
  is unchanged; closing the PR removes the preview. Separately, any self-edit
  gets a diff preview, a validator pass and a one-click revert.
- [ ] Notifications page. Operators add the identities that should receive
  alerts (phone, email, chat); the agent configures Alertmanager, Apprise and
  ntfy behind the scenes so alerts reach a person by default. **Acceptance:** a
  fresh cluster has no alert that points at nobody, a test notification can be
  sent from the page, and a stale or failing route appears in the attention
  list.

- [ ] Observe the cluster agent's own pod. Scrape it with Prometheus (add a
  metrics endpoint if it has none) and alert when it is down, restarting or
  failing its health check. Host agents are out of scope: the host is the thing
  they report on. **Acceptance:** a dead or crash-looping agent pod produces an
  alert through the Notifications page and shows in the attention list.

- [ ] SSH access to hosts over Tailscale, set up from an agent page. Today
  `ssh ansible@<host>` is refused by Tailscale SSH ("tailnet policy does not
  permit you to SSH to this node"), so even the operator cannot read a host's
  `/etc/adaetum/agent-token` to first sign in. The page lets an owner choose who
  may SSH to which hosts and as which user (for example `ansible`), shows the
  rule that results, and applies it through the existing Tailscale bootstrap
  tooling rather than by agent code editing the tailnet policy on its own.
  **Starting rule** (an SSH entry in the tailnet policy: the
  tailnet owner can connect as `ansible`, and only `ansible`, to any host tagged
  `tag:cluster`, nobody else):
  `{"action": "accept", "src": ["autogroup:owner"], "dst": ["tag:cluster"],
  "users": ["ansible"]}`. Setup writes this by default (done: the Tailscale policy step in
  `bootstrap-tailscale.py`, removed again by `task reset:cloud`), and the page
  builds on it by adding named people and users. Tailscale SSH checks the login
  name too, so each user must be listed ("does not permit you to SSH as user"
  means the host matched but the user did not). `root` is deliberately left out:
  `ansible` is the account the automation already uses.
  **Design points to settle:** whether the `ansible` user is reached through
  Tailscale SSH or the host's own sshd over the tailnet; that the rule is
  narrow (named people, tagged hosts, `check` mode) and never a wildcard; that
  recovery does not depend on it (the console and the agent token still work).
  **Acceptance:** an owner grants one person SSH to the hosts from the page, that
  person can connect and read the token, revoking removes access, and every
  change is recorded with who made it.

### Quality and release

- [ ] Run the full agent test suite on every pull request. **Current:**
  `.github/workflows/adaetum-tests.yml` is the single suite (agent Go tests and
  web type-check and build, auth Bun tests, embed and installer Go tests, and the
  agent boundary and release validators). `tests.yml` runs it on pull requests
  and main, and the release workflow requires it before building. **Still to do:**
  confirm a first green run on a real pull request, and make the Tests check
  required in branch protection. **Acceptance:** a pull request that breaks any
  of those suites cannot merge, and a release cannot test less than a pull
  request did.
- [ ] Add tests for the web UI. Start with the rules operators rely on:
  `lib/status.ts` and `lib/attention.ts` (what counts as unhealthy), the storage
  math in `lib/storage.ts`, and the update and sign-in flows. **Acceptance:** a
  test runner in `agent/web`, run in pull-request CI, with those rules covered.
- [ ] Protect the supply chain of released artifacts. Hosts and the cluster pull
  agent binaries and images that update automatically, and the checksum they
  verify comes from the same release. **Acceptance:** release binaries and
  images are signed (for example with cosign) and the agent, installer and host
  playbook verify the signature before use; releases publish an SBOM and a
  vulnerability scan; the build provenance is recorded. Include a rehearsed
  failure where a tampered artifact is refused.

### Documentation

- [ ] Document operator-loss recovery in the README. Setup uploads the
  generated `.env` to R2 under `iso/recovery/` (the artifacts stage reads it back
  to verify), so a replacement operator can recover it. State what the file
  contains, how it is protected in R2, who can read it, and the steps to take
  over from a lost laptop or hand the cluster to a second person.
- [ ] Write a "what to do when" set of runbooks under `docs/` (failed disk,
  forgotten password, a node that will not join, a stuck update). Each is
  tested on a real or rehearsed host so the in-app assistant can link to it
  instead of improvising.

## Version 3 — roadmap idea (not scheduled)

Direction only: nothing here should be built or designed in detail until the
production path above is proven. Much of it will be decided later, on the fly.
Per the scope guard, do not add a mode framework or abstraction ahead of a
concrete first use.

- [ ] Adaetum as a development platform. Today's opinionated setup is for people
  who are not technical: stable production workloads with safe defaults. Version
  3 would add a second opinionated setup for developers, where the cluster
  serves a development workload and deliberately relaxes some security patterns
  for speed. For example, preview environments could share test credentials,
  test secrets and test resources instead of being isolated from them (the
  production-oriented pod-change previews item keeps previews away from
  production credentials; development use would point them at shared test ones).
  **Shape to explore:** one cluster-wide setting in the agent, "this is a
  development workload cluster", which switches a bundle of defaults together.
  It stays opinionated: a different default for a different audience, not a
  pile of individual switches. **Questions to answer first:** whether this is a
  `platform.yaml` setting or a different installation profile; which
  protections are relaxed and which are never relaxed (for example, real
  provider credentials and the recovery repository); how a development cluster
  is made unmistakable in the web app so it cannot be confused with a production
  one; and how the shared test credentials are kept apart from real secrets.
  **Acceptance:** a written design that answers those questions, reviewed before
  any implementation.
- [ ] Possible later step (version 3.5 or after): further profiles for other
  specific workloads, such as a more advanced or more minimal setup. One idea is
  a single configuration that people turn parts of on or off, similar to how one
  kickstart file is accepted by many operating systems. If it works, it could
  make Adaetum a baseline platform that others build on. This is an idea, not a
  commitment, and `AGENTS.md` rules out a provider or capability layer until a
  real, tested second implementation exists. **Acceptance:** a design that
  names the first concrete second profile before any shared mechanism is built.

## Issue labels

Use `area/bootstrap`, `area/config`, `area/integrations`, `area/docs`,
`area/community`, `kind/bug`, `kind/feature`, `good first issue`, and
`help wanted` for public work tracking.
