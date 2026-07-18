# Adaetum architecture audit

## Purpose

This is the baseline audit for the platform-contract redesign. It identifies the current
system boundaries and the work needed to make Adaetum understandable, safe to
change, and attractive to open-source contributors.

## Architecture map

| Layer | Current authority | Contract direction |
| --- | --- | --- |
| Installer | `ks-src/` templates and manifests | Rocky 10 is the stable target; Ubuntu remains experimental |
| Bootstrap | Ansible playbooks and Phase 10–99 scripts | Explicit phase inputs/outputs and shared libraries |
| Platform configuration | `platform.yaml` | One public, non-secret contract; all env and manifests are generated outputs |
| Recovery repository | User's standalone private repository and break-glass bundle | Out-of-band configuration and recovery copy |
| GitOps | Cluster-seeded Gitea plus Argo CD | Retained as the authoritative steady-state control pair |
| Secrets | Local bootstrap files before Phase 40; OpenBao afterward | Retained; no secret values in profiles |
| Operations | Homepage links to upstream UIs | Retained; Adaetum does not create a replacement console |

## Source-of-truth and generated files

`ks-src/` is the installer source of truth and `dist/ks-templates/` is
generated. `platform.yaml` is the public configuration authority in each
user's private recovery repository. Rendering creates runtime environment values, cluster config, and
manifests from that profile. Bootstrap logs, recovery artifacts, and Kubernetes
Secrets are outputs—not user-owned configuration.

Setup keeps `.env` for runtime-only secrets while regenerating all public
values from `platform.yaml`. The runtime env and cluster-config files are
generated outputs, not alternative configuration inputs.

The target bootstrap repeats profile validation in Phase 10 before stateful
bootstrap phases can run.

## Secret authority audit

The previous statement that Phase 40 "promotes OpenBao to secret authority"
was only partially implemented. Phase 40 copied bootstrap values into OpenBao,
but later phases continued to create and patch workload Kubernetes Secrets
imperatively. Nothing continuously reconciled a changed OpenBao value back to
those delivery copies, and environment-variable consumers did not restart when
a Secret changed.

The steady-state contract is now explicit:

| Class | Authority | Delivery/rotation rule |
| --- | --- | --- |
| Adaetum-managed application credential | OpenBao | External Secrets reconciles a Kubernetes delivery Secret; an opted-in workload rolls when the delivery copy changes. |
| External provider credential | Cloudflare, Tailscale, or GitHub for issuance/revocation; OpenBao for the value selected by the cluster | Replace at the provider first, then update OpenBao. A random KV replacement is not a valid provider credential. |
| Recovery-plane and installer credential | The issuing provider plus the private GitHub environment or an approved OS credential store | Keep it usable while the cluster and OpenBao are unavailable. Do not copy it into workload KV unless an in-cluster consumer has a separate, least-privilege need. |
| Application/database credential | The application or database plus OpenBao | Use an application-aware rotation that changes the backing identity and OpenBao value as one operation before restarting dependents. |
| Encryption key | The owning application | Never blind-rotate. A documented data migration must precede any OpenBao value change. |
| RKE2 cluster token | RKE2; encrypted recovery export for disaster recovery | Rotate with RKE2's native token-rotation procedure and coordinated server state. Replacing its OpenBao recovery copy alone must never be presented as a cluster-token rotation. |
| Kubernetes identity, TLS, and controller state | Kubernetes or the owning controller | Do not copy into OpenBao merely to satisfy a universal-storage slogan. Use short-lived service-account tokens and controller-native certificate rotation. |
| Non-secret runtime configuration | Git/ConfigMap or application state | Do not hide ordinary configuration in a Secret. |

Upstream Helm charts sometimes package non-secret configuration or executable
init scripts in resources named `Secret`. Current exact-chart renders show
Gitea's `gitea-inline-config`, `gitea`, and `gitea-init`, Authentik's
`authentik`, and an empty `argocd-notifications-secret` in that category. Their
credential fields are either absent or overridden by the OpenBao-backed Secret
references above; Adaetum-authored non-secret configuration still belongs in a
ConfigMap.

The pods secret validator inventories every native `secretKeyRef`, `secretRef`,
Secret volume, projected Secret source, and image-pull Secret in repository
manifests. Each reference must resolve to an ExternalSecret target or one of the
small, named product-aware coordinators above; adding an unmanaged workload
Secret reference is therefore a contract failure rather than an undocumented
new authority.

Bootstrap has the same disclosure boundary: every task that loads, derives, or
registers a credential must use Ansible's `no_log`, and provider diagnostics may
record status and request metadata but not response bodies or proxy URLs. The
secret validator protects those named tasks so a later readability refactor
cannot accidentally turn bootstrap logs into another credential store.

External Secrets Operator and Reloader provide the common mechanism for the
safe first class. OpenBao now reconciles delivery copies for external-dns,
cloudflared, Homepage, Apprise, Argo CD repository and administrator access,
Gitea admin/runner/registry/PostgreSQL values, Authentik, Grafana, and Rancher. Their exact paths
and operator procedure are documented in
[`pods/secrets/openbao/README.md`](../pods/secrets/openbao/README.md).

The authority transition is also rerun-safe. Phase 40 and the shared Phase
50/60 bootstrap helper seed only application fields that do not yet exist;
they never replace an existing `secret/apps/*` value from a local bootstrap
copy. Where a stateful rotation has desired and active values, bootstrap reruns
retain the active delivery Secret until the application-aware coordinator has
promoted the OpenBao version.

Remaining concerns are concrete rather than theoretical:

- Argo CD, Gitea, Grafana, and Authentik administrator passwords now reconcile
  through product-native credential state rather than merely restarting with a
  changed Kubernetes Secret.
- Gitea PostgreSQL rotation now changes both database roles transactionally
  before promoting the active delivery Secret and restarting Gitea.
- Authentik PostgreSQL now uses the same database-first promotion boundary.
  Its pinned post-2023.6 signing key can rotate directly from OpenBao; Reloader
  restarts both components and existing sessions are invalidated without
  changing persisted users. Rancher administrator rotation now uses its native
  `PasswordChangeRequest`, validates the new login, and only then updates the
  bootstrap recovery copy.
- Gitea's global encryption root and Grafana's database key-encryption key are
  now OpenBao-owned, but deliberately use `OnChange` delivery so an accidental
  KV edit cannot corrupt persisted secrets. Gitea's internal and JWT signing
  values are separately and normally rotatable; a restart only invalidates
  transient tokens.
- Argo CD's arbitrary session-signing key is seeded from and continuously
  reconciled by OpenBao; rotation restarts only the server and revokes sessions.
  Its ephemeral Redis cache password is now also OpenBao-owned: the pinned
  chart's random secret-init Job is disabled and all Redis clients reload from
  the synchronized delivery Secret. Generated TLS keys remain controller-owned
  runtime state.
- The ingress VIP was reclassified as a ConfigMap because it is mutable network
  configuration, not a credential.
- Homepage exposes Cloudflare and Tailscale as ordinary links, not API-backed
  widgets. Its shared widget ExternalSecret therefore enumerates only its Argo
  CD and Gitea tokens. Authentik, Cloudflare, and Tailscale are link/status
  cards; their credentials are deliberately excluded rather than converted
  into unnecessary dashboard identities. Grafana has a separate Viewer
  identity with an application-first password coordinator, so its administrator
  password is no longer copied to Homepage. New Gitea widget tokens are
  restricted to the read-only notification, repository, and issue scopes
  Homepage requires. Late reconciliation reads Gitea's token registry rather
  than trusting token validity alone, replaces a broad legacy token, and
  revokes superseded Adaetum-owned widget tokens only after the replacement is
  delivered.
- The GitHub push-mirror credential is stored at an application-owned OpenBao
  path and projected read-only into Gitea. Its repository hook reads the
  projected value for every push instead of persisting a credential on the
  repository PVC. External Secrets refreshes the projection after an OpenBao
  change; bootstrap also removes files created by the legacy hook contract.
  GitHub still has to issue the replacement before OpenBao selects it.
- `openbao-bootstrap-token` is a transitional control credential. Steady-state
  controllers authenticate with scoped Kubernetes identities; they must not
  depend on the OpenBao root token. Phase 99 removes its Kubernetes copy after
  the encrypted recovery export succeeds. That export recursively includes all
  `secret/apps/*` leaves and aborts the burn if any application value cannot be
  read.
- The downloaded first-boot payload, kickstart EnvironmentFiles, rendered
  first-boot scripts, and installed Anaconda kickstart copies are temporary
  delivery artifacts, not recovery stores. Phase 99 removes the environment
  files on a successful break-glass finalization, and the outer handoff removes
  every credential-bearing artifact on all successful nodes. Join nodes
  therefore cannot retain the full provider/bootstrap credential set merely
  because they skip Phase 99. Failed runs retain the artifacts only for the
  explicit resume path.
- R2 upload credentials, GitHub App private keys, and CI dispatch credentials
  belong to the out-of-band recovery plane because they are needed to publish
  or retrieve installers and recovery artifacts while the cluster is absent.
  GitHub environment Secrets and the supported OS credential stores remain
  their delivery boundary; copying them into OpenBao would add exposure without
  replacing that recovery authority.
- The RKE2 server token is included in encrypted bootstrap recovery material,
  but its live authority is the RKE2 cluster. Rotation must use RKE2's native
  token-rotation workflow and update the recovery copy only after the cluster
  accepts the new token. A KV edit by itself is unsafe and ineffective.

## Bootstrap phases

Phase 10 validates. Phase 20 creates temporary local bootstrap secrets. Phase
30 establishes the cluster. Phase 40 promotes OpenBao to secret authority.
Phases 50 and 60 install then hand off the Argo CD/Gitea control pair. Phase 70
proves GitOps. Phase 90 completes live-state reconciliation, and Phase 99
exports recovery material and removes bootstrap-local authority.

The recurring Ansible runner executes `playbooks/day2.yml`, which is limited to
health and explicitly enabled host reconciliation. `playbooks/bootstrap.yml`
is never scheduled: replaying the installer path would let generated local
files overwrite values that OpenBao owns after Phase 40.

Every phase must state its input authority, output, mutation boundary, retry
behavior, and owner. A phase may not silently take responsibility for another
phase's concerns.

The normal lifecycle is deliberately asymmetric: a user creates a private recovery repository, builds
the break-glass bundle from that private repository, and uses it to create a new
cluster. Bootstrap clones/seeds the configuration into the newly deployed
Gitea. That in-cluster repository, reconciled by Argo CD, is authoritative for
day-2 changes. The original private repository is retained as the out-of-band copy
needed to reconstruct configuration when the cluster itself is unavailable.

## Lock-in inventory

Cloudflare currently supplies artifact delivery, DNS, tunnel, and edge work;
GitHub supplies source and CI integration; Tailscale supplies overlay access.
These are explicit, supported external integrations in the setup workflow.
They are not a plugin system: the cluster product stack is defined directly in
`pods/`, and Adaetum does not claim provider interchangeability it has not
implemented and tested.

Recovery-repository ownership means the project does not preserve old task names,
environment variables, or internal file shapes when they obscure the contract.

## Complexity hotspots and remediation

- Phase 50 and Phase 60 retain separate install and handoff policy. Their exact
  shared helpers (48 at the time of this audit) now live in
  `control-pair-common.sh`; a structural validator prevents exact duplication
  from returning. Helpers with the same name but different phase policy remain
  local. Continue reducing only behavior that has install-versus-handoff
  regression evidence; do not flatten the deliberately distinct phase policy.
- `platform-bootstrap.yml` and the setup/environment scripts mix several
  responsibilities. Split them by bootstrap concern, retaining stable public
  phase entrypoints during the contract refactor.
- Flat environment configuration duplicates host derivation. Replace it with
  schema-validated `platform.yaml` and generated runtime outputs.
- Existing validation is strong but fragmented. Make profile validation a
  first-class local and CI gate, then add language/config/security
  checks listed in the roadmap.

## Audit review cadence

Maintainers review this document before every minor release and after any
provider, installer, secret-authority, or bootstrap-phase change.
