# argocd-install role

Installs Argo CD via Helm and optionally creates a bootstrap Application that
points Argo CD at your GitOps repository.

## What it does

- Installs Helm (if missing) and adds the Argo Helm repository.
- Deploys Argo CD in the `argocd` namespace.
- Optionally creates an Application pointing at your repo and path.

## Defaults

Key settings in `defaults/main.yml`:

- `argocd_enabled`: toggle the role on/off.
- `argocd_namespace`: namespace to install into.
- `argocd_chart_version`: optional chart version pin.
- `argocd_admin_password`: admin password override.
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
