# argocd-install role

Installs Argo CD via Helm and optionally creates a bootstrap Application that
points Argo CD at your GitOps repository.

## What it does

- Installs Helm (if missing) and adds the Argo Helm repository.
- Deploys the pinned Argo CD chart in the `argocd` namespace.
- Seeds the Redis cache password before Helm; Phase 40 promotes it to OpenBao,
  and External Secrets plus Reloader own steady-state rotation.
- Optionally creates an Application pointing at your repo and path.

## Defaults

Key settings in `defaults/main.yml`:

- `argocd_enabled`: toggle the role on/off.
- `argocd_namespace`: namespace to install into.
- `argocd_chart_version`: chart contract pin (`10.1.4`).
- `argocd_admin_password`: admin password override.
- `argocd_redis_password`: required initial value, normally supplied by Phase
  20 through `ARGOCD_REDIS_PASSWORD` rather than set by an operator.
- `argocd_bootstrap_enabled`: create the bootstrap Application.
- `argocd_bootstrap_repo_url`: Git repository URL (Gitea preferred; falls back to GitHub envs).
- `argocd_bootstrap_repo_path`: path in the repo to sync.
- `argocd_bootstrap_repo_revision`: Git revision (default `HEAD`).
- `argocd_bootstrap_app_name`: Application name.
- `argocd_bootstrap_dest_namespace`: destination namespace for the app.
- `argocd_repo_username`: HTTPS repo username (default `oauth2`, from `ARGOCD_REPO_USERNAME` or GitHub fallback).
- `argocd_repo_token`: HTTPS repo token/password (from `ARGOCD_REPO_TOKEN` or GitHub fallback).

## Example overrides

```yaml
argocd_bootstrap_repo_url: "https://git.example.com/platform/infra.git"
argocd_bootstrap_repo_path: "clusters/primary"
argocd_bootstrap_repo_revision: "main"
argocd_bootstrap_app_name: "primary"
argocd_bootstrap_dest_namespace: "argocd"
```
