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
- `auth/`: the agent's account sign-in service (Better Auth on Bun). A separate process from the Go agent; see `agent/docs/recovery-login.md`. Tests: `cd auth && bun test`.
- `ks-src/`: kickstart source templates, manifests, and fragments used to generate rendered kickstarts under `dist/ks-templates/`.
- `.github/workflows/ks-worker.yml`: defines the Cloudflare KS/log Worker (worker.mjs is generated inline).
- `installer/`: the setup program (Go; a Wails window plus a headless command). It owns the whole
  first-run journey; see `installer/stages/`. Tests: `cd installer && go test ./...`.
- `tasks/env.yml`: Environment setup tasks including new opinionated workflow

## Setup workflow
Setup is the Go installer in `installer/`. One engine (`installer/engine`) owns the order,
checks, questions and dry run; the window (`installer/app`) and the headless command
(`installer/cmd/adaetum-setup`) only draw its questions and return answers.

1. **First run**: `task init` opens the setup window, building it from `installer/` on first use
   (needs Go and Node; installs the Wails CLI with `go install` if missing). It installs GitHub CLI
   first because the repository stage uses it. The stages, in order:
   - **repository**: signs in to GitHub, creates or reuses the operator's standalone private
     recovery repository, preserves canonical Adaetum as `upstream`, changes the existing
     checkout's `origin`, publishes `main`, and checks that Actions sees the workflows
   - **providers**: authorizes Cloudflare and Tailscale, chooses the DNS zone and tailnet,
     prepares tag ownership and creates the scoped OAuth clients
   - **profile**: writes and publishes `platform.yaml` from the two public choices
   - **media**: finds or downloads and verifies the Rocky ISO, then runs the setup readiness check
   - **validate**, **render**, **artifacts**, **finalize**: check inputs, render `.env` and the
     GitOps files and push them, publish to R2 and GitHub and trigger the workflows, hand over
     the installer ISO
2. `task init:dryrun` shows every step and what it would do, changing nothing, with no window. Pass `-- --window` to show the same read-only engine plan in the guided setup window.
3. `task init:clean` is `task init` ignoring saved credentials and values.
4. `task initialize` and `task init:silent` run the same stages with no window. Questions are
   answered with `-- --answer id=value`, and a question with no answer stops the run instead of
   guessing. Secrets are never accepted as `--answer` (command lines are visible to other
   users); a secret question reads `ADAETUM_SECRET_<ID>` from the environment. `init:silent`
   adds `--replay`: it reuses what exists and refuses to create a repository or start a large
   download on its own. `task init:dryrun` adds `--dry-run` and refuses `--apply`.
5. Resume credentials are saved only in the OS credential store, and only with the operator's
   agreement. There is no file fallback.

## Machine joining process
New nodes join the cluster through a phase-based break-glass process:

### Phase-based bootstrap (20-60):
- **Phase 20**: Local secrets setup
- **Phase 30**: Platform bootstrap
- **Phase 40**: OpenBao initialization and configuration
- **Phase 50**: Argo CD deployment
- **Phase 60**: Security hardening ("burn the ladder")

## Conventions
- **Scope guard (agent mandate):** Act as a skeptical Adaetum maintainer before
  adding repository content, automation, abstractions, dependencies, or process.
  Do not equate more files, checks, or features with progress. Ask whether the
  proposed change has a concrete operator or contributor benefit now, fits the
  private-recovery architecture, has a clear owner and source of truth, and can be
  validated at the appropriate boundary. If any answer is unclear, explain the
  concern and propose the smallest viable alternative (including no change)
  before editing. Reject speculative frameworks, duplicate contracts, generic
  policy, compatibility scaffolding, and documentation that merely restates
  code. Do not add a validator or CI job unless it protects a real contract and
  has a clear failure owner. Keep pull requests focused; separate unrelated
  cleanup from behavior changes.
- Before implementing a non-trivial change, state the problem, the affected
  source of truth, the smallest acceptable scope, and how it will be verified.
  Treat the checks in `CONTRIBUTING.md` and the pull-request template as release
  criteria, not paperwork.
- `pods/` is the source of truth for Adaetum's in-cluster product stack. Do
  not add a parallel module, provider-selection, plugin, or capability layer
  around applications already defined in `pods/` unless the user explicitly
  asks for a real, tested interchangeable implementation.
- `platform.yaml` is the single public, non-secret recovery repository configuration contract.
  It describes cluster identity and delivery settings; `.env`,
  `pods/cluster-config/cluster-config.env`, and rendered manifests are outputs.
  External integrations such as Cloudflare, GitHub, and Tailscale belong in
  their existing setup/bootstrap tooling, not in a generic module framework.
  Do not add a path that derives public manifest values from `.env`; only the
  profile renderer may derive public configuration.
- `.validator/` is the repository's home for executable validation and
  regression checks. Extend its command-line validators and their pre-commit/
  CI hooks instead of adding a separate top-level `tests/` layout, unless the
  repository deliberately adopts a new test convention.
- Discovery and preflight commands must be read-only. In particular, `task
  list` must never install tools, modify the worktree, or contact external
  services; keep installation and setup mutations behind explicit commands.
- Prefer running playbooks from `ansible/` so `ansible.cfg` is picked up.
- Keep sensitive hosts and secrets out of git; update `.gitignore` if new
  inventory files are introduced.
- First-run resume credentials may use macOS Keychain, Linux Secret Service,
  or Windows current-user DPAPI, but must never fall back to a plaintext
  repository or home-directory cache.
- `task clean` is the maintainer handoff boundary: it restores the public-safe
  `platform.yaml`, re-renders tracked outputs, and removes derived installers
  before changes are proposed upstream.
- OS install/bootstrap is always single-node at install time. Break-glass may
  initialize a new cluster on that one node; HA comes later when additional
  nodes are added.
- Kickstart assumes the ISO/CD will be ejected after install. If break-glass
  needs the repo, copy the embedded ISO contents to `/opt/ansible-runner` during
  `%post`.
- Install media is handled directly as ISO files; do not add alternate USB media
  packaging workflows or related documentation back into the repo.
- GitHub does not allow private forks of a public repository. Operator setup
  therefore requires a standalone private recovery repository as `origin` and
  preserves `Adaetum/Adaetum` as the public `upstream`. Never reintroduce a
  public GitHub fork as the destination for cluster configuration or secrets.
- Use `task init` for a new private recovery repository and `task initialize`
  for reruns or non-interactive automation. Edit
  `platform.yaml` for public platform changes; provide `.env` values only when
  setup requests runtime secrets. Never treat `.env` as a second public
  configuration contract.
- Gum is an optional terminal presentation layer for the remaining shell tools. Keep those
  workflows as the behavior owners, retain their plain-terminal fallbacks, and honor
  `ADAETUM_GUM_UI=0`; do not put secrets, provider logic, or configuration ownership in the
  presentation helper. First-run setup does not use Gum: the installer owns its own interface.
- Setup logic lives in `installer/`. Do not add a second setup path in shell or Python. The
  installer calls other programs only through `installer/sys` (fixed argument lists, never a
  shell string), passes secrets through standard input or the environment and never as
  arguments, and calls the Taskfile only from `installer/stages/artifacts` for
  `bundle:publish:external` and `build-iso`. Stage ownership of the scripts it still calls
  (`generate-env-files.sh`, the renderer and validators, `bootstrap-cloudflare.py`,
  `bootstrap-tailscale.py`) is named in the stage's package comment.
- When adding new playbooks or roles, place them under `ansible/playbooks/` and `ansible/automation-roles/`
  and keep names descriptive and stable for automation.
- Role README structure: follow the `healthcheck` style and keep sections filled out
  when possible. Preferred sections are "What it does", "Defaults", and "Example overrides". Use best
  practices and industry standards when they conflict with local preferences.
- Favor human readability in both README files and Ansible code. Prefer clarity over brevity, even if it
  makes the code more verbose or marginally less quick.
- Add inline comments and docstrings at ownership boundaries, safety-sensitive
  operations, non-obvious defaults, external side effects, and transformations.
  Explain *why* the code exists and what must remain true; do not add comments
  that merely restate the next line of syntax. Update nearby comments whenever
  a contract or phase boundary changes.
- When using heredocs inside YAML literal blocks (e.g., Ansible `shell` tasks), double-check indentation
  so the heredoc stays inside the block and the YAML remains valid.
- Optional envs: `ansible/.env` can be used for local env overrides. Keep `ansible/.env.template` in git.
