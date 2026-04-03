# Phase 60: Seed the Repo and Complete GitOps Handoff

## Intent

Phase 60 is the GitOps handoff phase.

This is where bootstrap seeds the Gitea repo path, points Argo CD at that
seeded source, and completes the initial control transition from bootstrap
setup into GitOps-managed state.

## Goal State

In the target design, Phase 60 should:

- seed the cluster repo into Gitea
- configure Argo CD to use the seeded Gitea repo
- persist the handoff-critical values OpenBao needs to retain
- leave the GitOps path configured and ready for realization checks in Phase 70

The design rule for Phase 60 is:

- complete the repo handoff cleanly, but do not confuse configured GitOps with
  proven GitOps

## Inputs

- installed Argo CD and Gitea control pair from Phase 50
- OpenBao-backed bootstrap and repo values
- rendered bootstrap repo content

## Outputs

- seeded Gitea repo for cluster GitOps
- Argo CD pointed at the seeded repo
- handoff-critical values persisted in OpenBao as needed

## Secret Authority

- authoritative secret store: OpenBao
- may write local bootstrap scratch: only as temporary transport or cache
- may write OpenBao: yes, for handoff-critical values
- may read backup artifacts: no in normal flow

## Design Rules

- keep this phase focused on seed and handoff
- do not treat this phase as the final GitOps stability proof
- avoid pulling in later realization-gate concerns that belong in Phase 70

## Relationship to Phase 50 and Phase 70

Phase 50 installs the control pair.

Phase 60 seeds the repo and completes the handoff.

Phase 70 then proves that the GitOps path is actually usable.

That means:

- Phase 50 installs the control pair
- Phase 60 hands GitOps over to the seeded repo
- Phase 70 proves that handoff is real in practice

## Acceptance Criteria

- Gitea contains the seeded repo
- Argo CD is configured to use the seeded repo
- the control path is handed off and ready for realization checks in Phase 70
