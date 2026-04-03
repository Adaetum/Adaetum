# Phase 50: Install the Minimal Argo CD and Gitea Control Pair

## Intent

Phase 50 is the bootstrap install phase for the GitOps control pair.

This is where bootstrap stands up the minimum Argo CD and Gitea footprint
needed to create a usable GitOps control loop.

## Goal State

In the target design, Phase 50 should:

- install Argo CD at the minimum viable bootstrap level
- install Gitea at the minimum viable bootstrap level
- establish the baseline connectivity and access plumbing needed for the pair
  to function
- leave the cluster ready for repo seed and handoff work in Phase 60

The design rule for Phase 50 is:

- install only the minimum control pair needed for GitOps bootstrap

## Inputs

- OpenBao as the authoritative secret store after Phase 40
- cluster baseline from earlier phases
- bootstrap values needed for minimal Argo CD and Gitea install

## Outputs

- Argo CD installed
- Gitea installed
- bootstrap-critical access plumbing for the control pair in place

## Secret Authority

- authoritative secret store: OpenBao
- may write local bootstrap scratch: only as temporary transport or cache
- may write OpenBao: yes, only for install-critical bootstrap values
- may read backup artifacts: no in normal flow

## Design Rules

- keep this phase focused on bootstrap installation
- do not treat this phase as repo handoff or sync-wave verification
- do not expand into late app convergence

## Relationship to Phase 40 and Phase 60

Phase 40 establishes OpenBao as the authority.

Phase 50 installs the minimal Argo CD and Gitea control pair.

Phase 60 then seeds the repo and completes the GitOps handoff.

That means:

- Phase 40 establishes authority
- Phase 50 installs the control pair
- Phase 60 hands control to the seeded GitOps repo

## Acceptance Criteria

- Argo CD and Gitea exist at the minimum viable bootstrap level
- the control pair is installed but not yet considered fully realized
- the cluster is ready for repo seed and handoff in Phase 60
