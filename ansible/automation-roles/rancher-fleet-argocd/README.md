# rancher-fleet-argocd role

Registers the repo with Rancher Fleet so Argo CD is installed as a Fleet-managed
Helm release. This keeps Argo CD under Rancher control while letting Argo CD
manage higher-level workloads from Git.

## What it does

- Ensures the Fleet and Argo CD namespaces exist.
- Creates a Fleet GitRepo resource pointing at `pods/argocd`.
- Creates a Fleet repo credentials secret (HTTPS token) when provided.

## Defaults

Key settings in `defaults/main.yml`:

- `fleet_argocd_enabled`: toggle the role on/off.
- `fleet_argocd_repo_url`: Git repo URL (Gitea preferred; falls back to GitHub envs).
- `fleet_argocd_repo_branch`: Git branch for Fleet.
- `fleet_argocd_repo_path`: path to the Fleet bundle (`pods/argocd`).
- `fleet_repo_username`: HTTPS username (default `oauth2`).
- `fleet_repo_token`: HTTPS token/password.
- `fleet_argocd_target_namespace`: namespace where Argo CD is installed.

## Example overrides

```yaml
fleet_argocd_repo_url: "https://github.com/BingelsWorth/Cluster"
fleet_argocd_repo_branch: "master"
```
