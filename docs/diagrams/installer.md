# Diagram source: the installer

Source notes for an animated diagram. Everything here is checked against the code
in `installer/` (the stage list is `installer/stages/stages.go`); where a fact comes
from somewhere else, it says so.

**One sentence:** the installer runs on the operator's computer, before any cluster
exists, and turns two choices (a domain and a tailnet) into a private recovery
repository, scoped cloud credentials, a published first-boot payload, and one
installer ISO.

## Actors

| Actor | What it is | Where it runs |
|---|---|---|
| **Operator** | The person at the keyboard. | their computer |
| **Installer** | A Go program: a window (Wails) or a headless command. One engine owns the order, the checks and the questions; the window and the terminal only draw questions and return answers. | operator's computer |
| **GitHub** | Holds the operator's private recovery repository, its Actions workflows and its secrets. | cloud |
| **Cloudflare** | DNS zone, the R2 bucket, the Worker, the tunnel. | cloud |
| **Tailscale** | The tailnet, its access policy, and the OAuth clients nodes use to join. | cloud |
| **Rocky Linux mirror** | Source of the base ISO. | internet |
| **OS credential store** | Keychain, Secret Service or Windows DPAPI. | operator's computer |

## Order

The stages run in this order. Each one checks before it acts, so a rerun skips what
is already true ("earlier completed steps are kept").

1. **Repository.** Signs in to GitHub through the GitHub CLI. Creates (or reuses)
   a **standalone private** recovery repository, never a fork, in a temporary
   isolated checkout. Publishes its private `main` and confirms that Actions sees
   the workflows. The operator's launch checkout is not changed. The public
   project stays as `upstream`.
2. **Providers.** Authorizes Cloudflare and Tailscale. The operator picks the DNS
   zone and the tailnet. The installer prepares tag ownership in the tailnet policy,
   adds the SSH rule for cluster-tagged hosts, and creates two **scoped OAuth
   clients**: one that lets nodes join, one for the Kubernetes operator.
3. **Profile.** Turns the two public choices into `platform.yaml`, renders the
   files derived from it, validates them, and publishes them to the private
   repository as one commit. `platform.yaml` is public and holds no secrets.
4. **Media.** Finds, or downloads and verifies, the Rocky Linux ISO, then runs a
   readiness check.
5. **Validate.** Checks all inputs before anything is generated or published.
6. **Render.** Writes the git-ignored `.env`, the rendered GitOps files and the
   GitHub environment secrets, validates them, and pushes the rendered state to
   the private repository.
7. **Artifacts.** Publishes what the first server needs from outside this
   machine: the installer payload and break-glass bundle in R2, secrets in the
   GitHub environment. Triggers three workflows (`ks-worker`, `ks-publish`,
   `iso-build`), builds the installer ISO locally, then checks that log delivery
   through the Worker to R2 really works. Saves a recovery copy of the setup
   secrets to R2 and verifies it.
8. **Finalize.** Confirms the ISO exists and is newer than everything it was built
   from, tells the operator what to do with it, and offers a copy in Downloads.

## What the first server will fetch later

This is the handoff that matters for the next diagram. The installer leaves three
things in the cloud and one on disk:

| Output | Where | Used by |
|---|---|---|
| Installer ISO | operator's disk | the operator, to install a machine |
| `ansible-runner-bundle.tar.gz` | R2, key `firstboot-payload/` | first boot, downloaded through the Worker |
| `bootstrap-runtime.env` | R2, key `firstboot-payload/` | first boot, downloaded through the Worker |
| Private recovery repository | GitHub | the cluster's GitOps source, and recovery |

The ISO itself holds only a small set of things (kickstart, the pinned host agent,
the start/join detector and a console helper). The scripts and secrets travel in the
R2 payload.

## Handoff barriers

Points where responsibility passes from one thing to another. These are where an
animation can pause.

- **Interactive to scripted.** The engine owns order and questions. The window and
  the headless command only draw questions and return answers. A headless run with a
  missing answer stops instead of guessing.
- **Installer to existing tools.** Provider work (Tailscale, Cloudflare) and the
  renderer still run in Python and shell. The installer calls them only through
  `installer/sys`, as fixed argument lists, never a shell string.
- **Installer to GitHub Actions.** The installer triggers the workflows, then
  waits and reports each run's result. A workflow can fail while the installer
  succeeded in starting it; the stage collects failures and fails at the end.
- **Installer to R2.** The first-boot payload is uploaded and then verified by
  fetching it back through the Worker. Setup does not finish on an unverified upload.
- **Operator to the new machine.** The ISO is the last thing the installer
  produces. Writing it to media and booting is the operator's step.

## Security barriers

- **No secret in an argument list.** Secrets reach other programs through standard
  input, a 0600 temporary file removed when the stage ends, or the environment.
  Command lines are visible to other users, so none are allowed. A secret question in
  headless mode reads `ADAETUM_SECRET_<ID>` from the environment, never `--answer`.
- **GitHub token to git.** It reaches git through `GIT_CONFIG_*` environment
  variables, not a `-c` argument.
- **Resume credentials.** Saved only in the OS credential store, and only with the
  operator's agreement. There is no file fallback, and never a plaintext cache in
  the repository or home directory.
- **Private by construction.** GitHub does not allow private forks of a public
  repository, so the destination is a standalone private repository.
  Cluster configuration and secrets never go to a public fork.
- **Public versus secret.** `platform.yaml` is the only public configuration
  contract. `.env` and everything derived from secrets stay private.
- **Scoped provider access.** Nodes join with scoped OAuth clients, not a
  full-access key. Deleting those clients later needs a temporary Tailscale access
  token, which the reset task asks for at that moment and does not store.
- **Destructive actions need a list and a yes.** The reset task (`task reset:cloud`)
  lists exactly what it would remove, defaults to "no", and deletes only that list.
  The Python tools re-check each item against a fresh listing before deleting.
- **Rehearsal changes nothing.** `task init:dryrun` shows each step and what it
  would do without calling providers or writing anything.

## Failure and retry

A failed stage shows "Try this step again". Completed stages are kept. A failed
workflow run, a bad credential, or a failed upload is reported at the stage that owns
it; some failures stop the run and some are reported while the run continues
(the artifacts stage lists which).

## Animation notes

Suggested frames, in order: operator opens the window; GitHub (private repo
appears); Tailscale and Cloudflare (two scoped keys appear); profile commit; ISO
download; render (files fan out into three destinations: repo, R2, GitHub
secrets); three workflows run; ISO assembled; operator receives the ISO. Mark the
five handoff barriers above and the three secret paths (stdin, temp file,
environment) as distinct visual elements.
