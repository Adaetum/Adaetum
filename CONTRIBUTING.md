# Contributing to Adaetum

Adaetum welcomes documentation, validation, installer, integration, and bootstrap
contributions. Fork the repository, create a focused branch, and open or
comment on an issue so changes remain small and reviewable. Contributor forks
are public collaboration branches; operator cluster state belongs in the
standalone private recovery repository created by `task init`. Do not preserve
legacy internals merely for in-place compatibility when a clearer documented
replacement is better.

## Local checks

Install `task`, Python 3, and pre-commit, then run:

```bash
task platform:validate
pre-commit run --all-files
```

Run the narrowest relevant Task validation while developing. Never commit
secrets, real inventory, installer artifacts, recovery files, or generated
runtime environments.

## Pull requests

- Start from a concrete operator or contributor problem, not a preferred
  implementation. Explain why the change belongs in Adaetum now and why the
  proposed scope is the smallest useful one.
- Explain the user-facing behavior and the validation you ran.
- Keep generated files synchronized with their documented sources.
- Add or update validators and docs whenever a contract changes.
- Preserve phase boundaries and secret authority rules.
- For an external-integration change, update its setup documentation,
  validation and recovery guidance together.
- Do not add generic frameworks, a second configuration contract, speculative
  extension points, compatibility scaffolding, or a CI check without a specific
  current contract and a maintainer who can act when it fails.

Maintainers use squash merges and require passing CI. See
[GOVERNANCE.md](GOVERNANCE.md) for review and release decisions.
