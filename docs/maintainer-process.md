# Maintainer triage and release process

This process keeps Adaetum’s public work visible while preserving the security
and recovery boundaries of a bootstrap project.

## Issue triage

Maintainers review new issues at least weekly.

1. Confirm that the issue contains no credentials, recovery artifacts, private
   inventory, or private hostnames. Move security reports to the private path
   in [SECURITY.md](../SECURITY.md).
2. Apply one `kind/*` label and one `area/*` label.
3. Reproduce the issue when practical, using sanitized output and the stated
   profile/setup command.
4. Record the expected behavior, owner, and acceptance criteria before marking
   work ready for contribution.
5. Mark narrowly scoped, well-documented work as `good first issue` or `help
   wanted` when appropriate.

Issues that require a provider account, real cluster, or recovery material
must state that dependency clearly. They are not blocked merely because they
need real-world evidence.

## Pull-request review

Reviewers check that a change preserves these authorities:

- `platform.yaml` owns public fork configuration.
- `.env` and runtime payloads carry secrets only.
- `pods/` owns the in-cluster product definitions.
- The private fork is recovery/out-of-band configuration; in-cluster Gitea and
  Argo CD own day-2 reconciliation.

Changes to bootstrap phases, secret authority, external integrations, recovery,
or a public profile contract require two maintainer approvals and an
architecture note as described in [GOVERNANCE.md](../GOVERNANCE.md).

## Release process

1. Create a release issue from the [release checklist](../.github/RELEASE_TEMPLATE.md).
2. Link the required evidence from [release evidence](release-evidence.md).
3. Verify the support matrix and known limitations against the actual release
   scope; do not promote an experimental path through wording alone.
4. Write release notes that identify public contract changes, fork actions,
   recovery/rebuild guidance, and any deferred validation.
5. Apply the tag specified by the [versioning policy](versioning.md), publish
   the GitHub Release, and link the completed release issue.

Release evidence must be sanitized. Secrets, recovery kits, raw inventories,
and private endpoints never belong in issues, pull requests, logs, or release
notes.
