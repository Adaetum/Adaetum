# Phase 20: Bootstrap Secret Authority Before OpenBao

## Intent

Phase 20 is the bootstrap secrets phase.

By the time Phase 20 starts, Phase 10 should already have completed the full
fast-fail validation suite. That means Phase 20 should not spend its time
revalidating the world. Its main job is to instantiate the bootstrap-local
secret authority that later pre-OpenBao phases can use.

In practical terms, Phase 20 should:

- generate required bootstrap secrets that do not already exist
- materialize operator-provided or preselected bootstrap values into the local
  secret store when they are part of the bootstrap contract
- persist those values in a stable local location so later phases can read them
- act as the secrets authority until OpenBao is initialized and promoted in
  Phase 40

## Goal State

In the target design, Phase 20 is the first state-creating bootstrap phase and
the temporary authority layer before OpenBao exists.

That means:

- it owns bootstrap-local secret instantiation
- it provides the local persistence mechanism that lets pre-OpenBao phases
  "talk" to each other
- it is deliberately idempotent, so existing non-empty secret files are reused
  instead of being rotated casually
- it creates only the bootstrap-local state genuinely needed before OpenBao can
  take over

The design rule for Phase 20 is:

- if a value must exist before OpenBao is available and later pre-OpenBao
  phases need to read it, Phase 20 should instantiate and persist it

## Current State

Today, [`run-phase20.sh`](run-phase20.sh)
already behaves mostly like this intended design.

It currently:

- creates and permissions [`BOOTSTRAP_SECRET_DIR`](run-phase20.sh)
- generates a fixed set of bootstrap secrets into that directory
- writes fixed literal values such as `authentik_admin_username`
- skips existing non-empty files so reruns are stable
- fails on empty preexisting files unless explicitly told to regenerate them

That is already close to the goal. The documentation gap is mainly that Phase 20
should be described less as generic "scratch generation" and more as the
temporary bootstrap secrets authority before OpenBao.

## What Phase 20 Should Own

Phase 20 should own bootstrap-local secret instantiation, including:

- generated bootstrap credentials required before OpenBao
- fixed bootstrap-local values that need to be materialized for downstream
  phases
- storage conventions and permissions for the local bootstrap secret directory
- idempotent reuse of existing valid bootstrap secret files

This phase is also the correct place for pre-OpenBao cross-phase state that
must be persisted locally so later phases can consume it.

## Inputs

- operator-provided bootstrap values that have already been validated earlier
- local environment and setup context
- any existing bootstrap-local secret files from prior partial runs

## Outputs

- a populated local bootstrap secret directory
- bootstrap-local secret files that later phases can read consistently
- a temporary secret authority that lasts until OpenBao is established

## Secret Authority

- authoritative secret store: local bootstrap secret directory
- may write local bootstrap scratch: yes
- may write OpenBao: no
- may read backup artifacts: no in normal first bootstrap

This is the only phase where the local bootstrap secret directory should be
treated as the authoritative secret store by design.

## Design Rules

- Phase 20 assumes validation has already happened in Phase 10
- it should instantiate secrets, not do broad environment sanity checking
- it should be idempotent and preserve existing good values
- it should fail on obviously bad local secret state such as empty placeholder
  files
- it should create only the secret and transport state genuinely needed before
  OpenBao exists

## What Stays Out of Phase 20

Phase 20 should not:

- become the long-term key vault
- depend on OpenBao
- mint late live-app tokens
- perform post-burn reconciliations
- consume emergency backups during ordinary first bootstrap
- absorb the fast-fail validation role that belongs to Phase 10

## Relationship to Phase 10 and Phase 40

Phase 10 is the gate.

Phase 20 is the first state-creating phase.

Phase 40 is the authority transition into OpenBao.

That means:

- Phase 10 should prove the run is worth attempting
- Phase 20 should instantiate the bootstrap-local secret authority
- Phase 40 should promote authority out of local bootstrap files and into
  OpenBao

The local secret directory created by Phase 20 is therefore temporary by
design, but authoritative until Phase 40 completes.

## Acceptance Criteria

This Phase 20 design is successful when:

- a reader understands that Phase 20 is primarily the bootstrap secrets phase
- the doc makes clear that validation should already have happened before this
  phase starts
- the doc explains that Phase 20 is the temporary pre-OpenBao secret authority
- the doc explains that the local secret directory is how pre-OpenBao phases
  share state
- the doc preserves the later handoff to OpenBao in Phase 40
