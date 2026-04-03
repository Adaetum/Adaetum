# Cluster Config

This directory is the committed cluster-specific config surface for `pods/`.

- `cluster-config.env` is edited by `task initialize`.
- `task clean` resets `cluster-config.env` plus the tracked rendered pod config
  files back to safe placeholder values before commit or publication.
- App-local config manifests are rendered from this file for Kustomize apps that
  cannot safely read files outside their own app root under Argo CD.
- File-driven manifests that Argo cannot parameterize directly are rendered from
  the same values during setup and validation.

Forks are expected to own and commit `cluster-config.env`. Upstream maintainers can
enable the optional maintainer guardrail to prevent accidental commits of
private or maintainer-specific values.
