# Versioning policy

Adaetum uses semantic version tags: `vMAJOR.MINOR.PATCH`, with optional
prerelease identifiers such as `v0.4.0-alpha.1`.

## Before the first stable release

Prereleases may make breaking changes to tasks, profile fields, rendered
artifacts, and bootstrap internals. Release notes must identify the break and
the required recovery repository update or rebuild procedure. Users should validate their
repository with `task setup:preflight` and the relevant recovery path before relying
on a prerelease.

## Stable releases

- A patch release fixes defects without intentionally changing public setup,
  profile, or recovery contracts.
- A minor release adds compatible documented capability or expands supported
  paths.
- A major release may change a public contract or supported workflow.

Adaetum is recovery-repository-first. A release does not promise an in-place upgrade for a
private recovery repository or a running cluster. When a release changes a public contract,
the release notes must state the repository update, recovery/rebuild path, and any
manual review required before adoption.

## Release evidence

Every release must use the release checklist and link the evidence defined in
[release evidence](release-evidence.md). Stable support claims require a clean
install and recovery result for the named supported path.
