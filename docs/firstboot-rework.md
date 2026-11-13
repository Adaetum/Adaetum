# First boot: concepts for the rework

This is the working design for rebuilding first boot from scratch, one phase at a
time, on the `firstboot-rework` branch. It describes **what each phase is for**,
not how today's shell scripts do it. When the new version of a phase replaces the
old one, the old script and its `phase-NN.md` are deleted.

This is a **guide, not a roadmap**. It says what each part is for and what must be
true of it. It does not promise what gets built when, and it does not fix the order
of phases (see "Questions per phase"). It changes as we learn; a decision is recorded
here when it is made, not before.

## What first boot is

The OS is installed and the machine reboots. First boot turns that blank Rocky
Linux host into **a one-node cluster that runs itself from Git**, with its
secrets in OpenBao and a recovery kit exported. It then removes the temporary
authority it needed to get there.

Two invariants hold for every design below:

- **Single node at install time.** A new cluster starts on one node. Extra nodes
  join later and never run the "create the cluster" steps.
- **Recovery is the cardinal use case.** A host must be usable, and its failure
  understandable, with the cluster down.

## The stages

| Stage | Today | Concept |
|---|---|---|
| 0. Install | Kickstart `%post` | Put the minimum on disk: the pinned host agent, the start/join detector and the first-boot service. The bundle is fetched later. |
| 1. Host readiness | `tailscale-firstboot` fragment | Make the machine able to reach the world and the tailnet: clock, network, Tailscale. Decide **start or join**. |
| 2. Intake (Phase 10) | `run-phase10.sh` | Read-only check that the profile and runtime payload are usable. Creates no state. |
| 3. Local secrets (20) | `run-phase20.sh` | Create the temporary, node-local secret authority. |
| 4. Cluster (30) | `platform-bootstrap.yml` | RKE2 and the platform substrate: storage, certificates, Rancher, ingress, tunnel. |
| 5. Authority (40) | `run-phase40.sh` | Initialise OpenBao and move every surviving secret into it. |
| 6. Control pair (50) | `run-phase50.sh` | Install the minimum Argo CD and Gitea. |
| 7. Handoff (60) | `run-phase60.sh` | Seed the cluster repo into Gitea and point Argo CD at it. |
| 8. Proof (70) | `run-phase70.sh` | Prove GitOps actually works before continuing. |
| 9. Late reconcile (90) | `run-phase90.sh` | Finish credentials that only exist once apps are running. |
| 10. Burn (99) | `run-phase99.sh` | Export the recovery kit, then destroy bootstrap authority. |

## Concepts that cut across phases

**Secret authority has a timeline.** Local bootstrap files are authoritative from
Phase 20 until Phase 40. From Phase 40 on, OpenBao is. After Phase 99, the local
files are gone and only OpenBao (plus the exported recovery kit) remains. A new
phase must say which authority it reads and writes.

**Start versus join.** The first node creates the cluster. Later nodes join it.
Today an offline peer with the cluster tags makes a new node choose *join*, and
join fails if no live server exists. The rework should decide this explicitly
and make the failure say why.

**Boundaries.** The agent does not run arbitrary commands on servers; servers are
changed by Ansible playbooks the agent launches
(`adaetum/agent/docs/security-boundary.md`). A first-boot runner in the agent
must work inside that rule or change it deliberately.

**Source of truth.** `platform.yaml` is the public configuration. `.env` and
rendered manifests are outputs. `pods/` is the product stack. First boot reads
these; it does not invent a second configuration.

**What ships where.** The ISO carries only a small set of things: the kickstart,
the start/join detector, the pinned host agent and a console helper. The scripts
and playbooks do not ride on the ISO. First boot downloads them as
`ansible-runner-bundle.tar.gz` (and a runtime environment file) from the
Worker/R2, using the shared token, and unpacks them to `/opt/ansible-runner`. So a
first-boot fix ships by publishing the bundle, not by rebuilding the ISO. The host
agent is a separately released, pinned binary. A first-boot runner that lives in
the agent ties first-boot fixes to agent releases unless the logic it runs ships
in the bundle.

## What the rework must get right

These are the reasons for the rework, so they apply to every phase.

1. **Retry and resume.** A failure must be retryable from the failed step without
   reformatting. Each step has a recorded state (not started, running, done,
   failed) and is safe to run twice.
2. **Telling slow from broken.** Waiting is the main thing first boot does. A wait
   reports what it is waiting on and why. Reasons like an image still pulling are
   patient. Reasons like an unpullable image, a crash loop, an unschedulable pod
   or an unbound volume fail fast and say which. A long overall ceiling remains.
3. **Visible progress.** The current step, its state and its failure reason are
   readable while the node runs, and from the logs afterward (see `task fetch-logs`).
4. **One contract per step.** Every step states what must be true before it
   starts, what it does, and what must be true after. A later step relies only on
   that, never on side effects of an earlier one.
5. **Small phases.** Phases 50 and 60 are each around 3,500 lines of shell and
   overlap heavily. A rebuilt phase does one job.
6. **Tests without a node.** Each step's decisions are testable against fake
   input. Nothing important is only provable on real hardware.
7. **Everything is first-class.** Every unit of work the agent does (a first-boot
   phase, unsealing OpenBao, joining a node, the doctor, a repair) is a
   *procedure* run as a *run*. They all start through the same API, with the same
   authentication, the same record written before work begins, the same events,
   retry and wait behavior, and the same place in the UI. There are no hidden
   scripts and no special cases for first boot: first boot is just the first
   procedure that runs. The engine is `adaetum/agent/internal/runs`.

## The phases, as concepts

### Stage 1. Host readiness
Make the host able to reach everything it needs: a correct clock, working
network (static or DHCP), Tailscale installed and joined with the right tags. It
ends by deciding start or join. It does not touch Kubernetes.
*Produces:* a reachable node with a known role. *Authority:* none yet.

### Phase 10. Intake
Reject unusable machine input before any state exists: the rendered profile and
the downloaded runtime payload. It is read-only; if it fails, nothing was
created. Repository-level validation already happened at checkout and publish,
so it is not repeated here.

### Phase 20. Local secrets
Create the secrets that must exist before OpenBao does, store them in a
permission-restricted node-local directory, and reuse existing good values on
rerun. This is the first state-creating step and the temporary secret authority.

### Phase 30. Cluster
Turn the local secrets and the bundle into a working one-node cluster: RKE2, the
network plugin, storage, certificates, Rancher, ingress, and the public tunnel.
It stops at "ready for OpenBao". It does not create an authority and does not
reconcile applications.

### Phase 40. Authority
Initialise and unseal OpenBao, set it up to unseal itself on restart, and write
every secret that survives past this point into it. After this phase local files
and environment variables stop being sources of truth. This is the one authority
transition.

### Phase 50. Control pair
Install the smallest Argo CD and Gitea that can form a GitOps loop, with the
access they need to talk to each other. Installed, not yet proven.

### Phase 60. Handoff
Seed the cluster repository into Gitea and point Argo CD at it, so the cluster
now follows Git. Configured, not yet proven.

### Phase 70. Proof
Prove the loop is real: the runner image is in the registry and pullable, the
app-of-apps exists, and the critical sync waves finish and stay healthy. Later
phases do not proceed on configuration alone. This is where most waiting
happens, so it is where the slow-versus-broken logic matters most.

### Phase 90. Late reconcile
Do only the work that needs running applications: mint or repair live
credentials, validate them immediately, write the result back to OpenBao, and
render workload secrets from it.

### Phase 99. Burn
Export the recovery kit and a standalone OpenBao backup, then destroy the
node-local bootstrap authority, only after the export succeeds. OpenBao is the
sole normal authority afterward.

## Questions per phase

To settle as each phase is rebuilt, before writing it:

- **Order.** Does Gitea need to come after OpenBao? Could the control pair and
  handoff (50, 60) be one phase? Does Phase 70's proof belong inside each phase
  rather than as a separate gate?
- **Phase 30.** It is a single 3,000-line playbook. Which of Rancher, ingress,
  the tunnel and cert-manager are needed before OpenBao, and which could move
  into GitOps?
- **Phase 90.** Which late credentials can be made declarative so this phase
  shrinks or disappears?
- **Join nodes.** Which phases run on a joining node, and what does a join need
  to prove?
- **Runner.** Decided: it lives in the agent (`internal/runs`), as a durable engine
  of procedures, steps and runs. Still open: how steps act on the host within the
  agent boundary (a reviewed, typed host-operations adapter is the likely answer),
  and how procedure fixes ship without an agent release (the bundle already ships
  through the Worker/R2).

## Built so far

First boot lives in the agent as **chunks**: each is a procedure that runs and is tested
alone, and `first-boot` runs them in order. A chunk replaces its old phase script when it
has tests and runs through the engine; until then the shell script still runs that phase.

| Chunk | Replaces | State |
|---|---|---|
| `host-readiness` | the first-boot readiness steps (route, clock) | built: network, clock, disk |
| `intake` | Phase 10 | built for the runtime payload. **Not yet:** the platform-profile check (the Python validator) |
| `local-secrets` | Phase 20 | built: same names, sizes and encoding; existing secrets never touched |
| `first-boot` | the chain | the three chunks above, in order |
| cluster, OpenBao, control pair, handoff, proof, late reconcile, burn | Phases 30 to 99 | not started |

Test one chunk by itself, on any host, with no daemon and no database:

```bash
adaetum-agent run -list
adaetum-agent run host-readiness
adaetum-agent run -input path=/var intake
```

Flags go before the procedure name. It prints one `[RUN]` line per record on stderr and a
summary on stdout, and exits 0 done, 1 failed, 3 needs a person, 4 stopped. It changes the
machine exactly as the procedure says, as whoever runs it, so `local-secrets` needs root.
The same procedures run inside the agent service, through the Runs page and the API, with
their records in the agent's journal.

### Trying it on an ISO without a release

`task init:dev` is `task init` for development. The ISO it builds carries an agent built
from this checkout (version `dev-<commit>`, plus `+local` when the agent has uncommitted
changes) instead of the pinned release in `adaetum/host-agent-release.json`, and the
agent is started with `-allow-runs -run-on-start=host-readiness`. Its `[RUN]` lines land
in the journal the host already uploads, so `task fetch-logs` shows them. A production ISO
is unchanged: the extra arguments come from `/etc/adaetum/agent.env`, which only
development media writes. The host never replaces its own binary, so nothing swaps the
bundled agent for a release. Only `host-readiness` starts at boot for now; the old shell
phases still do the rest.

## Using this guide

- Procedures are built as they are picked up, one at a time. There is no schedule
  in this document, and no promise that they come in the order listed above.
- A procedure replaces its old script when it has tests, runs through the engine,
  and can be retried and resumed. The old script and its `phase-NN.md` are deleted
  then, not before.
- Anything that changes how the pieces fit (the boundaries, the step contract, the
  record format) is written down here first, so the decision is not an accident.
