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

## Bootstrap phases

Phase 10 validates. Phase 20 creates temporary local bootstrap secrets. Phase
30 establishes the cluster. Phase 40 promotes OpenBao to secret authority.
Phases 50 and 60 install then hand off the Argo CD/Gitea control pair. Phase 70
proves GitOps. Phase 90 completes live-state reconciliation, and Phase 99
exports recovery material and removes bootstrap-local authority.

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
