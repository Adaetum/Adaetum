# Argo CD managed workloads

This tree is intended for workloads managed by Argo CD and installed via Rancher
Fleet. The Argo CD bootstrap manifests live under `pods/argocd/bootstrap/` and
define the ApplicationSet that discovers apps.

Each workload has:
- An app definition at `pods/<namespace>/<appname>.app.yaml` (simple config used by the ApplicationSet).
- A deployable tree under `pods/<namespace>/<appname>/` (kustomize/helm values/manifests).

Shared cluster-specific values live in `pods/cluster-config/cluster-config.env`.
`task initialize` updates that file and re-renders the small set of
file-driven Argo/Gitea manifests plus app-local `*-cluster-config.yaml` files
that Kustomize apps consume inside their own app roots.

Upstream maintainers can enable the optional public-template guard by setting
`MAINTAINER_TEMPLATE_GUARD=1` in an ignored local env source such as `task.env`
or their shell profile. Forks do not need that guard unless they want the same
check locally.

Platform bootstrap apps live under `pods/argocd/platform/`.
The pre-OpenBao app is created by Ansible during Phase 30; the post-OpenBao app
is applied manually from `pods/argocd/platform/post-openbao/application.yaml`.

Secrets management workloads live under `pods/secrets/` (for example OpenBao).
