# Phase 90: Late Live-State Reconciliation

## Intent

Phase 90 is the late live-state reconciliation phase. It handles only the
small set of tasks that genuinely cannot be finished until the applications
exist and the system is operating primarily from OpenBao plus live cluster
state.

## Goal State

In the target design, Phase 90 should:

- mint or repair live app credentials that can only be created once the apps
  are fully running
- validate those credentials immediately
- persist the validated final values back into OpenBao
- render workload secrets from OpenBao plus live state

The design rule for Phase 90 is:

- only keep work here that truly depends on late live application state

## What Phase 90 Should Own

Phase 90 should own:

- late live-app credential minting
- immediate validation of those late credentials
- post-burn reconciliation tasks that depend on the applications actually
  existing
- writing validated final values back into OpenBao
- rendering or refreshing workload secrets from OpenBao plus live state

Examples that belong here:

- Homepage widget tokens
- Headlamp admin token reconciliation
- post-burn app admin password reconciliation
- late app API tokens that require the application to be fully up
- runtime secret refreshes derived from authoritative OpenBao state

## Inputs

- OpenBao as the authoritative secret store
- live application state
- existing workload secrets only as rendered outputs or continuity aids, never
  as authority

## Outputs

- validated final app credentials in OpenBao
- refreshed workload secrets rendered from vault state plus live state

## Secret Authority

- authoritative secret store: OpenBao
- may write local bootstrap scratch: only if it still exists and only as
  temporary transport while that path has not been fully removed
- may write OpenBao: yes
- may read backup artifacts: only in explicit restore mode, never in ordinary
  post-burn reconciliation

The key rule is:

- Phase 90 should consume OpenBao first and write the final validated result
  back to OpenBao

## Design Rules

- Phase 90 is allowed to mint live credentials
- any credential minted or repaired here must be validated immediately
- the validated final value must be written back to OpenBao
- workload secrets remain outputs, not the authority
- work that belongs to the GitOps realization gate must stay in Phase 70

## What Stays Out of Phase 90

Phase 90 should not:

- use the emergency backup as an ordinary source-of-truth
- treat workload Kubernetes Secrets as canonical for OpenBao-owned values
- act as the first place that GitOps stability is discovered
- carry bootstrap duties that belong to Phase 70 or earlier

## Relationship to Phase 70 and Phase 99

Phase 70 proves GitOps is stable enough to continue. Phase 90 then handles the
late tasks that genuinely require live app state. Phase 99 runs after that to
destroy bootstrap-local authority and export the recovery kit.

## Acceptance Criteria

This Phase 90 design is successful when:

- a reader understands that Phase 90 is a small late-reconciliation phase, not
  a giant repair bucket
- the doc makes clear that OpenBao is the authority here
- the doc makes clear that GitOps stabilization work belongs in Phase 70
- the scope stays tight around late live-state reconciliation
