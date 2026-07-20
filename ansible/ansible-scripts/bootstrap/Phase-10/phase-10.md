# Phase 10: First-Boot Intake Gate

## Intent

Phase 10 runs after Rocky Linux has been installed and the embedded Adaetum
bundle has been copied to the host. Its job is to reject unusable machine input
before Phase 20 creates bootstrap-local state.

Repository source has already crossed its validation boundaries by this point:

- maintainer commits run the repository hooks
- pushes and pull requests run the repository validation workflow
- installer publication validates and compiles the Kickstart sources

Repeating those checks on the target host provides no additional source-quality
guarantee. It also incorrectly makes first boot depend on Git metadata, hook
runners, and developer-only tooling that are not part of the installed-host
contract.

## Checks Owned Here

Phase 10 validates only inputs that can differ at the machine boundary:

- the rendered `platform.yaml` profile, when the platform profile contract is
  active
- the downloaded or embedded bootstrap runtime payload required by later phases

These checks are read-only. A failure means the copied profile or runtime
payload is missing, malformed, or inconsistent and bootstrap must stop before
creating state.

## Checks Owned Elsewhere

Phase 10 does not run repository hooks, compile Kickstarts, or validate the
entire `pods/` contract. Those checks remain available through the normal
maintainer tasks and are enforced when a checkout is configured, changed, or
published.

In particular, first boot must not require `.git`, `prek`, `pre-commit`, or a
complete development checkout.

## Relationship to Phase 20

Phase 10 is the last read-only machine-input gate. Phase 20 is the first
state-creating phase and establishes the temporary bootstrap secret authority.
If Phase 10 succeeds, bootstrap may proceed; if it fails, no bootstrap-local
secret state should have been created.
