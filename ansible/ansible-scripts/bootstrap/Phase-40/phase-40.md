# Phase 40: OpenBao Authority Transition

## Intent

Phase 40 is the OpenBao handoff phase.

This is where the bootstrap process stops treating the Phase 20 local secret
directory as the long-term authority and promotes the system into an
OpenBao-backed model. In practical terms, this is the phase where temporary
bootstrap-local authority goes to die and OpenBao takes over.

## Goal State

In the target design, Phase 40 remains straightforward and authoritative.

It should:

- initialize and unseal OpenBao
- establish the OpenBao bootstrap access surface needed for immediate
  configuration
- write every secret or bootstrap value still needed after this point into
  OpenBao
- make OpenBao the single authoritative mutable secret store for normal
  bootstrap flow after Phase 40 completes

The design rule for Phase 40 is:

- if a secret class is still relevant after OpenBao exists, it must be written
  into OpenBao here and later phases must stop treating local files or env as
  authoritative

## Current State

Today, [`run-phase40.sh`](run-phase40.sh)
already matches this role closely.

It currently:

- waits for cluster and storage readiness needed for OpenBao to come up
- initializes and unseals OpenBao
- creates or verifies bootstrap OpenBao access
- stores Rancher bootstrap material in OpenBao
- stores Argo CD bootstrap and repo material in OpenBao
- stores a broad set of bootstrap-generated platform secrets in
  `secret/bootstrap/platform`
- applies the post-init OpenBao configuration path through Argo or directly

That means the basic behavior is already right. The main thing the design doc
needs to make explicit is the boundary: after this phase, the older secret
handling model should no longer be considered normal authority.

## What Phase 40 Should Own

Phase 40 should own:

- the authority transition from local bootstrap secrets to OpenBao
- the first durable write of bootstrap and platform secrets into OpenBao
- the rule that later phases read OpenBao first for any secret class already
  promoted here
- the finalization of the pre-OpenBao era

Typical outcomes include:

- initialized and unsealed OpenBao
- bootstrap secret paths populated in OpenBao
- later phases no longer needing to guess whether local scratch or OpenBao is
  authoritative

## Inputs

- working cluster baseline from Phase 30
- local bootstrap secret authority from Phase 20
- operator-provided or earlier-selected bootstrap values still needed after
  OpenBao comes online

## Outputs

- initialized and usable OpenBao instance
- OpenBao bootstrap access material
- persisted bootstrap and platform secret paths inside OpenBao
- a completed authority transition for normal bootstrap flow

## Secret Authority

- authoritative secret store before completion: local bootstrap secret
  directory
- authoritative secret store after completion: OpenBao
- may write local bootstrap scratch: yes, but only as transport during the
  handoff
- may write OpenBao: yes
- may read backup artifacts: only in explicit restore mode

The key rule is:

- after Phase 40 completes, env values and local bootstrap files are no longer
  the normal source of truth for any secret class already persisted into
  OpenBao

## Design Rules

- keep Phase 40 as the one clear authority transition point
- persist all still-relevant pre-OpenBao secrets into OpenBao here
- remove ambiguity about whether later phases should read env, local files, or
  OpenBao
- do not defer core secret promotion into later phases if the secret is already
  known and needed beyond this point
- treat local bootstrap files after promotion as transport or recovery residue,
  not normal authority

## What Must Change or Be Enforced

To fully match the intended design, the bootstrap system should enforce these
rules after Phase 40:

- later phases should read OpenBao first for every secret class promoted here
- env should stop acting as a competing authority for already-promoted secrets
- local bootstrap secret files should stop acting as a competing authority for
  already-promoted secrets
- newly added pre-OpenBao secrets should be reviewed to ensure they are also
  promoted into OpenBao if they remain relevant after this phase

The practical standard is simple:

- if a value survives past Phase 40, it belongs in OpenBao

## What Stays Out of Phase 40

Phase 40 should not:

- leave later phases guessing which authority to trust
- keep env and local bootstrap files alive as peer authorities after promotion
- behave like a late runtime reconciliation phase
- use emergency-kit material as a normal first-bootstrap source
- defer the OpenBao authority transition to Phase 60 or Phase 90

## Relationship to Phase 20 and Phase 50

Phase 20 creates the temporary local bootstrap secret authority.

Phase 40 ends that model and promotes authority into OpenBao.

Phase 50 should then operate on the assumption that OpenBao is the normal
secret authority for any class already promoted in Phase 40.

That means:

- Phase 20 creates temporary bootstrap-local authority
- Phase 40 promotes that authority into OpenBao
- Phase 50 and later should move forward with OpenBao-first reads

## Acceptance Criteria

This Phase 40 design is successful when:

- a reader understands that this is the OpenBao authority transition phase
- the doc makes clear that Phase 20 local authority ends here
- the doc makes clear that surviving bootstrap secrets must be written into
  OpenBao here
- the doc makes clear that env and local files should stop competing with
  OpenBao after this phase
- the scope stays tight and does not turn Phase 40 into a later reconciliation
  phase
