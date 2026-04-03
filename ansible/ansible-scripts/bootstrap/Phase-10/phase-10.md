# Phase 10: Bootstrap Sanity Check

## Intent

Phase 10 should be the fast-fail gate for bootstrap.

Before any expensive, stateful, or cluster-mutating bootstrap work begins,
Phase 10 should run the full suite of validations that can be executed without
depending on a live OpenBao-backed cluster authority. If any of those checks
fail, bootstrap should stop immediately.

The intent is not partial progress. The intent is to prove, as early as
possible, whether the planned bootstrap run is already known to be invalid.

## Goal State

In the target design, Phase 10 becomes the canonical bootstrap sanity check.

That means:

- it runs every non-mutating validator that can predict bootstrap failure early
- it absorbs existing validation surfaces instead of creating a parallel
  validation system
- it stays validation-only and does not become a secret-generation or bootstrap
  state phase

The design rule for Phase 10 is:

- if a validator can run before expensive bootstrap work and can prove the run
  will fail, it belongs in Phase 10

## Current State

Phase 10 now runs as a real first-boot validation gate through `task`.

In the supported bootstrap path:

- the OS install layer provisions Homebrew before first boot
- `bundle-bootstrap` ensures `task` and `kubectl` exist via Homebrew before
  Phase 10 starts
- Phase 10 then runs the bundle-safe Taskfile validation targets instead of a
  direct fallback stack

That removes the old split between "normal task path" and "direct validator
fallback path" for first boot.

## Validation Surfaces Phase 10 Should Own

Phase 10 should eventually absorb the existing validation families that already
exist in the repo and can run before stateful bootstrap work.

### Hook runner validation

- `prek run --all-files`
- `pre-commit run --all-files`

### Kickstart and artifact validation

- `task bootstrap:phase10:check-ks`
- `task bootstrap:phase10:compile-ks`

### Runtime payload validation

- `task bootstrap:phase10:validate-runtime`

### Pods and manifest contract validation

- `task bootstrap:phase10:validate-pods-contract`

### Underlying validators absorbed by the Phase 10 task path

- `render-pods-config.py --check`
- `validate-pods-consistency.py`
- `validate-no-example-placeholders.py`
- `validate-ingress-contract.py`
- `validate-bootstrap-runtime-env.sh`

### General rule

Phase 10 should continue absorbing validators as they are added elsewhere in
the repo, so long as they:

- are non-mutating
- do not require OpenBao as a live authority
- can fail before expensive bootstrap work begins

## What Stays Out of Phase 10

Phase 10 should not:

- generate bootstrap scratch secrets
- create transport files for later phases
- initialize or depend on OpenBao
- mutate cluster state
- consult emergency backup artifacts
- become a mixed validation-plus-bootstrap-state phase

This document does **not** change source-of-truth rules or repo structure. It
only defines the intended validation role of Phase 10.

## Relationship to Phase 20

Phase 10 is the gate.

Phase 20 is the first state-creating bootstrap phase.

That means:

- scratch generation remains in Phase 20
- pre-OpenBao phase-to-phase communication through persisted local scratch
  remains in Phase 20
- the separation exists so validation fails before stateful bootstrap work
  starts

Phase 10 should prove a run is worth attempting. Phase 20 should be the first
phase that actually creates bootstrap-local state.

## Acceptance Criteria

This Phase 10 redesign is successful when:

- a reader immediately understands that Phase 10 is intended to become the full
  fast-fail validation gate
- the current state is described only to explain the implementation gap
- the doc clearly names the existing validators and tasks that should be
  absorbed into Phase 10
- the doc preserves current source-of-truth and repo-structure assumptions
- the doc clearly states that scratch generation and pre-OpenBao cross-phase
  transport remain Phase 20 responsibilities

