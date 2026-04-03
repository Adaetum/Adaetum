# Kickstart Source Tree

`ks-src/` is the authoring source of truth for generated installer artifacts in `dist/ks-templates/`.

Layout:

- `templates/`: top-level Jinja2 templates for each generated artifact.
- `manifests/`: compiler metadata describing installer type, OS family, shared fragments, adapters, and ordered sections.
- `fragments/installers/`: installer-specific fragments such as kickstart or future autoinstall wrappers.
- `fragments/shared/`: portable bootstrap fragments that must not hard-code package managers or service managers.
- `fragments/os-family/`: adapter fragments for package install, service control, filesystem, and networking differences.

Workflow:

1. Edit files under `ks-src/`.
2. Run `task ks:compile`.
3. Use the generated `dist/ks-templates/*.ks` artifacts for local builds or workflow publishing.
