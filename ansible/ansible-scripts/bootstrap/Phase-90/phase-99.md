# Phase 99: Destroy Bootstrap Authority and Export Recovery Kit

## Intent

Phase 99 is the final burn-the-ladder and recovery-export phase.

This is where bootstrap destroys the remaining bootstrap-local authority and
exports the emergency recovery kit after the system has already proven stable
under OpenBao and GitOps.

## Goal State

In the target design, Phase 99 should:

- run as the last bootstrap phase
- destroy bootstrap-local authority on the node
- export a recovery-oriented emergency kit
- leave OpenBao as the surviving normal secret authority

The design rule for Phase 99 is:

- remove temporary bootstrap authority only after it is no longer needed for
  normal operation

## What Phase 99 Should Own

Phase 99 should own:

- emergency recovery export creation
- standalone OpenBao bootstrap backup creation
- destruction of bootstrap-local authority on the node
- the explicit end of the bootstrap ladder

Typical outcomes include:

- exported recovery artifact
- exported `bootstrap-openbao-backup` artifact
- exported OpenBao login token in an obvious recovery path
- exported Headlamp admin token when it exists
- removed local bootstrap secret directory contents
- a system that now expects OpenBao and normal GitOps operation to carry on
  without bootstrap-local authority

## Inputs

- authoritative OpenBao state
- stable post-bootstrap system
- explicit recovery export inputs

## Outputs

- emergency recovery artifact
- destroyed or removed local bootstrap scratch

## Secret Authority

- authoritative secret store: OpenBao
- may write local bootstrap scratch: only as part of packaging the export
- may write OpenBao: no in the normal path
- may read backup artifacts: no

The key rule is:

- Phase 99 exports recovery material and destroys bootstrap-local authority,
  but it does not create a new competing runtime authority

## Design Rules

- the emergency kit should be a disaster-recovery export, not a runtime secret
  store
- the latest bootstrap-owned OpenBao paths should be exported as their own
  recovery artifact, not only embedded inside the emergency kit
- backup contents should remain recovery-oriented
- local bootstrap authority should be destroyed only after export succeeds
- this phase should stay operationally small and final

## What Stays Out of Phase 99

Phase 99 should not:

- act as a GitOps realization gate
- perform late app credential reconciliation
- preserve a full runtime-equivalent local secret tree just because it exists
- create a backup that later phases treat as normal operational input

## Relationship to Phase 90

Phase 90 handles the remaining late live-state reconciliation work.

Phase 99 runs after that to remove bootstrap-local authority and preserve only
recovery material.

That means:

- Phase 70 already proved GitOps is stable enough to continue
- Phase 90 finishes the last real runtime reconciliation work
- Phase 99 closes the loop and removes the bootstrap ladder

## Acceptance Criteria

This Phase 99 design is successful when:

- a reader understands that this is the final authority-destruction step
- the doc makes clear that the emergency kit is for recovery, not runtime
- the doc makes clear that a standalone `bootstrap-openbao-backup` is produced
  alongside the normal emergency kit backups
- the doc makes clear that OpenBao survives as the normal authority
- the scope stays tight and final
