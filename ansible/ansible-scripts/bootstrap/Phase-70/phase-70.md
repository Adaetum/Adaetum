# Phase 70: GitOps Realization Gate

## Intent

Phase 70 is the GitOps stability gate.

After the control pair is installed and the repo handoff is complete, this
phase proves that GitOps is actually usable before later phases are allowed to
proceed.

## Goal State

In the target design, Phase 70 should:

- confirm the `ansible-runner` image has been built and uploaded to the expected
  Gitea registry location
- confirm the image pull path needed by later Argo-managed workloads is viable
- trigger or confirm the app-of-apps deployment path
- follow Argo CD sync progress through the critical bootstrap waves
- surface sync and health failures clearly
- block later phases until the bootstrap-critical GitOps path is stable

The design rule for Phase 70 is:

- do not continue until GitOps has moved from "configured" to "proven usable"

## Inputs

- working Argo CD and Gitea bootstrap pair from earlier phases
- OpenBao-backed runtime and bootstrap values as needed
- expected registry image reference and app-of-apps entrypoint

## Outputs

- confirmed `ansible-runner` image availability
- confirmed or initiated app-of-apps deployment
- tracked Argo CD sync-wave results for bootstrap-critical apps
- a stable-enough GitOps state to allow later phases to proceed

## Secret Authority

- authoritative secret store: OpenBao
- may write local bootstrap scratch: no, except for transient logging or cache
- may write OpenBao: only if a Phase 70-owned observation or control value must
  be persisted
- may read backup artifacts: no in normal flow

## Design Rules

- confirm image availability before expecting Argo-managed pulls to work
- treat Argo CD sync-wave tracking as a real gate
- distinguish hard failures from warnings
- keep this phase focused on proving GitOps usability, not late credential
  reconciliation

## Relationship to Phase 60 and Phase 90

Phase 60 completes the GitOps handoff.

Phase 70 proves that the GitOps path is actually usable.

Phase 90 handles the remaining late live-state reconciliation.

That means:

- Phase 60 configures the GitOps path
- Phase 70 proves the path is real and stable
- Phase 90 handles the truly late runtime-dependent work

## Acceptance Criteria

- the runner image exists at the expected registry reference
- app-of-apps is created or confirmed
- critical bootstrap sync waves are observed and stable enough to continue
- later phases do not proceed on GitOps configuration alone
