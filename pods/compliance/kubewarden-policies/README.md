# Kubewarden policies

These manifests are Adaetum's explicit cluster admission policies. Kubewarden
runs them on the `default` PolicyServer installed by
`kubewarden-defaults.app.yaml`.

The initial `disallow-latest-tags` policy denies Pods with an omitted tag or an
explicit `:latest` tag. It uses Kubewarden's maintained `trusted-repos` module
and also audits resources that already exist in the cluster.

Treat changes here as production admission-control changes. New policies should
start in `monitor` mode unless their blocking behavior has been validated
against every workload under `pods/`.
