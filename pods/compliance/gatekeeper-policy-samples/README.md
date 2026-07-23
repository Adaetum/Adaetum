# Gatekeeper Policy Samples

These manifests are the default Gatekeeper policy set applied through GitOps.

Current sample:
- `disallow-latest-tags`
  - denies container images that omit a tag or use `:latest`
  - applies to every namespace; bundled workloads use explicit image versions

Argo CD apps:
- `pods/compliance/gatekeeper-policy-templates.app.yaml`
  - syncs `ConstraintTemplate` resources first
- `pods/compliance/gatekeeper-policy-constraints.app.yaml`
  - syncs concrete constraints after the template app
  - avoids first-sync CRD discovery races on fresh clusters

Layout:
- `pods/compliance/gatekeeper-policy-samples/templates`
  - template CRDs such as `ConstraintTemplate`
- `pods/compliance/gatekeeper-policy-samples/constraints`
  - actual constraint instances such as `K8sDisallowLatestTag`
- `pods/compliance/gatekeeper-policy-samples/kustomization.yaml`
  - root that renders templates first, then constraints

Operational note:
- if you add more deny policies here, treat this directory as production-default
  policy, not a scratch area for experiments
