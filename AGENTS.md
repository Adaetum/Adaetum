# AGENTS

This repo packages an Ansible runner container plus playbooks and roles for
cluster bootstrap and RKE2 setup. Keep changes lightweight and aligned with the
Ansible layout below.

## Repo layout
- `ansible/playbooks/`: entrypoint playbooks (e.g., `bootstrap.yml`, `healthcheck.yml`).
- `ansible/automation-roles/`: role implementations used by playbooks.
- `ansible/ansible-scripts/`: helper scripts for cron/manual runs.
- `ansible/node-inventory.yml`: generated host inventory file. Use `break_glass=true` to have
  playbooks detect the local hostname and run only that matched host with
  `ansible_connection=local`.
- `ansible/ansible-host-config-sync.yaml`: Kubernetes manifest for running the container.
- `ks-src/`: kickstart source templates, manifests, and fragments used to generate rendered kickstarts under `dist/ks-templates/`.
- `.github/workflows/ks-worker.yml`: defines the Cloudflare KS/log Worker (worker.mjs is generated inline).
- `tasks/scripts/run-opinionated-setup.sh`: Opinionated end-to-end setup automation
- `tasks/scripts/run-initial-setup.sh`: Comprehensive bootstrap automation flow
- `tasks/env.yml`: Environment setup tasks including new opinionated workflow

## Setup workflow
The repository uses a new opinionated setup process:

1. **Initial Setup**: Run `task initialize` for end-to-end automation
   - Prompts for Cloudflare token, GitHub token, and KS base URL
   - Automatically generates `.env`
   - Validates Tailscale OAuth credentials
   - Uploads golden ISOs to R2
   - Syncs secrets to GitHub environments
   - Triggers required GitHub workflows

## Machine joining process
New nodes join the cluster through a phase-based break-glass process:

### Phase-based bootstrap (20-60):
- **Phase 20**: Local secrets setup
- **Phase 30**: Platform bootstrap
- **Phase 40**: OpenBao initialization and configuration
- **Phase 50**: Argo CD deployment
- **Phase 60**: Security hardening ("burn the ladder")

## Conventions
- Prefer running playbooks from `ansible/` so `ansible.cfg` is picked up.
- Keep sensitive hosts and secrets out of git; update `.gitignore` if new
  inventory files are introduced.
- OS install/bootstrap is always single-node at install time. Break-glass may
  initialize a new cluster on that one node; HA comes later when additional
  nodes are added.
- Kickstart assumes the ISO/CD will be ejected after install. If break-glass
  needs the repo, copy the embedded ISO contents to `/opt/ansible-runner` during
  `%post`.
- Install media is handled directly as ISO files; do not add alternate USB media
  packaging workflows or related documentation back into the repo.
- **New**: Use `task initialize` for opinionated end-to-end setup; avoid manual .env editing when possible
- When adding new playbooks or roles, place them under `ansible/playbooks/` and `ansible/automation-roles/`
  and keep names descriptive and stable for automation.
- Role README structure: follow the `healthcheck` style and keep sections filled out
  when possible. Preferred sections are "What it does", "Defaults", and "Example overrides". Use best
  practices and industry standards when they conflict with local preferences.
- Favor human readability in both README files and Ansible code. Prefer clarity over brevity, even if it
  makes the code more verbose or marginally less quick.
- When using heredocs inside YAML literal blocks (e.g., Ansible `shell` tasks), double-check indentation
  so the heredoc stays inside the block and the YAML remains valid.
- Optional envs: `ansible/.env` can be used for local env overrides. Keep `ansible/.env.template` in git.
