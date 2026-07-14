# Phase 30: Cluster Establishment

## Intent

Phase 30 is the cluster establishment phase.

This is where the bootstrap process uses the Phase 20 local secret authority to
bring up the actual platform baseline and cluster substrate. Its job is to turn
bootstrap-local inputs into a working first-node cluster with enough platform
readiness to introduce OpenBao in Phase 40.

## Goal State

In the target design, Phase 30 remains a relatively simple phase.

It should:

- consume the local bootstrap secret authority created in Phase 20
- run the platform bootstrap automation
- establish the cluster and core platform baseline
- leave the system ready for OpenBao authority establishment in Phase 40

The design rule for Phase 30 is:

- build the cluster baseline, but do not become a secret-authority transition
  phase or a late runtime convergence phase

## Current State

Today, [`run-phase30.sh`](run-phase30.sh)
already matches that role closely.

It currently:

- requires the local bootstrap secret directory to exist
- uses the repo-local Ansible configuration
- runs [`platform-bootstrap.yml`](../../../playbooks/platform-bootstrap.yml)
  with `bootstrap_secret_dir` and `platform_distribution`

That means Phase 30 is already behaving primarily as the platform-establishment
phase. There is not much to change conceptually here beyond keeping the
boundary clear.

## What Phase 30 Should Own

Phase 30 should own:

- cluster baseline establishment
- platform bootstrap automation
- the first-node substrate needed for later OpenBao introduction

Typical outcomes include:

- RKE2 and cluster bootstrap
- foundational platform services
- enough readiness for Phase 40 to initialize and promote authority into
  OpenBao

## Inputs

- local bootstrap secret authority from Phase 20
- platform bootstrap playbooks and inventory
- local repo checkout and Ansible configuration

## Outputs

- working cluster baseline
- foundational platform components
- platform readiness for Phase 40

## Secret Authority

- authoritative secret store: still local bootstrap secret directory
- may write local bootstrap scratch: yes, but only for transitional needs
- may write OpenBao: no
- may read backup artifacts: no in normal flow

Phase 30 still operates before the OpenBao authority transition, so it relies
on the local bootstrap secret store created in Phase 20.

## Design Rules

- keep Phase 30 focused on cluster and platform establishment
- do not let it become a secret-authority transition phase
- do not let it become a late credential-finalization or runtime reconciliation
  phase
- avoid introducing durable authority semantics before OpenBao exists

## What Stays Out of Phase 30

Phase 30 should not:

- behave like a long-term key vault
- initialize OpenBao
- persist authoritative mutable secrets outside the upcoming OpenBao path
- use emergency-kit data as a normal bootstrap input
- absorb post-burn or late live-app reconciliation work

## Relationship to Phase 20 and Phase 40

Phase 20 creates the temporary bootstrap-local secret authority.

Phase 30 uses that authority to establish the cluster baseline.

Phase 40 then promotes authority into OpenBao.

That means:

- Phase 20 creates the local secret basis
- Phase 30 builds the cluster using that basis
- Phase 40 changes the authority model

## Acceptance Criteria

This Phase 30 design is successful when:

- a reader understands that Phase 30 is primarily cluster establishment
- the doc makes clear that Phase 30 still depends on Phase 20 local secrets
- the doc makes clear that OpenBao authority does not begin here
- the doc keeps the scope tight and does not overload the phase with later
  convergence work
