# Diagram source: how the agent interacts with hosts

Source notes for an animated diagram. Checked against `adaetum/agent/docs/`
(`security-boundary.md`, `cluster.md`, `auth.md`), the agent code in
`adaetum/agent/internal/`, the `host-agent` Ansible role, and the kickstart that
installs the host agent. Where something is designed but not built, it is marked
**not built**.

**One sentence:** every host runs its own agent that works alone; the cluster agent,
once there is a cluster, gathers what the host agents report; and neither agent runs
arbitrary commands on a server, because changes to servers go through Ansible
playbooks.

## Actors

| Actor | What it is | Where it runs |
|---|---|---|
| **Operator** | A person using the agent's web page, or an AI assistant acting for them. | browser or MCP client |
| **Host agent** | The `adaetum-agent` Go binary. Works on one node, before a cluster exists, and without one. | systemd service on every server |
| **Cluster agent** | The same binary run as a pod once Kubernetes is up. The cluster's single endpoint. | pod in the cluster |
| **Kubernetes API** | The cluster's source of truth about which nodes exist. | the cluster |
| **Ansible runner** | Does the actual work on servers, over SSH. | a Kubernetes Job from the cluster agent, or `ansible-playbook` on the machine |
| **Auth service** | Optional local sign-in service (Bun) for per-person accounts. | Unix socket on the host or pod |

## Two kinds of agent, deliberately independent

The host agent exists so troubleshooting still works when Kubernetes is down.
So it is never a client of the cluster agent.

- **Host agent.** Installed by the kickstart (`/usr/local/bin/adaetum-agent`, service
  `adaetum-agent.service`) and running from first boot. It listens on
  `0.0.0.0:28443` over HTTPS with a certificate it makes for itself and requires its
  **access token** for every request except `/healthz`. It keeps its own database
  (a local Turso file) of events. Every feature works with the cluster agent
  unreachable. The kickstart starts it without `-allow-ansible` or
  `-allow-changes`, so a fresh host agent can read and report but cannot launch
  playbooks or change anything until someone turns that on.
- **Cluster agent.** Aggregates: it collects events from host agents, keeps the
  cluster view the dashboard reads, and links to each host's own page for
  host-level actions. The host agent stays the authority for anything on its
  machine.

## Flows

### 1. Check-in (host to cluster)

1. At start and every five minutes the host agent records an `agent.checkin` event
   (hostname, version) in its own database.
2. It sends pending events to the cluster agent's endpoint. Delivery is
   best-effort: an unreachable cluster is normal and never blocks anything.
3. The cluster agent stores each event once. An event's identity is `hostID:seq`,
   so sending twice is harmless, and the host advances one cursor per host when the
   cluster acknowledges.
4. The cluster agent compares the hosts that have checked in with the nodes
   Kubernetes lists. The dashboard shows servers **not running the agent**
   and servers running an **older version**.

### 2. Becoming trusted (enrollment)

A name proves nothing, so trust has a source.

**Path A: automatic, for hosts that are cluster nodes.** Kubernetes vouches.

1. The cluster agent publishes its address and certificate fingerprint in the
   ConfigMap `adaetum/adaetum-cluster-agent-endpoint`. Neither is secret.
2. A host agent with no link, on a machine that has RKE2's credentials, reads that
   ConfigMap, and publishes its own public key as the annotation
   `adaetum.io/host-key` on its own Node. Only that node, or a cluster
   administrator, can write it.
3. The host asks to enrol **without a token**.
4. The cluster agent approves it only if the Node it names carries the very key it
   presented. A stranger cannot claim a node name, because they cannot put their key
   on that Node.
5. The host saves the link and exits with status 75 so systemd restarts it and it
   starts sending.

A reinstalled node comes back with a new key under the same name and republishes
it; Kubernetes' word replaces the key Kubernetes approved earlier. A host a person
approved by hand is never replaced this way.

**Path B: with a token, for any other host.** An operator mints a one-time join
token (valid up to 7 days, stored only as a hash). The host runs `adaetum-agent
join` with the cluster's pinned fingerprint. If the machine is a listed node it is
approved at once; otherwise it waits as **pending** for a person. If Kubernetes
cannot be read, nothing is auto-approved.

After enrollment every request from the host is **signed** with its Ed25519 key
(method, path, body, timestamp, one-time nonce). The cluster stores only the public
key, so nothing it holds can impersonate a host. A host can write only its own
events.

> Path A needs an agent release that contains it; hosts pinned to an older release
> use Path B.

### 3. Operator to agent (who may use it)

- A signed-in **account**, when the agent has the optional auth service connected, or
- the **access token** (`Authorization: Bearer`), which always works and does not
  depend on the auth service. It is the emergency way in, and the web page keeps it
  in the browser tab only.

Every `/api` call needs one of these whenever the agent listens beyond loopback, and
always over HTTPS. See `adaetum/agent/docs/recovery-login.md` for signing in with the
cluster down.

### 4. Changing a server (the only path)

The agent does not run commands on servers. It launches an Ansible playbook.

1. A signed-in person opens the Automation page (or the Cluster page's plan, such as
   "install the agent on these servers").
2. They choose a playbook **from a fixed catalog** and name the servers explicitly.
3. They **rehearse first**: the command ends in `--check`.
4. The agent records the launch **before** starting it, then runs one playbook at
   a time. Launching is **off unless the agent was started with `-allow-ansible`**
   (or switched on in Security settings) and can write its record. It has two
   executors:
   - **`kubernetes`**, for the cluster agent: a **Kubernetes Job** in the Ansible
     runner's namespace, reusing the existing runner pod template (no arbitrary
     image or caller-supplied pod);
   - **`local`**, the default: `ansible-playbook` run as a program on the agent's own
     machine, directly with no shell, from a directory and inventory fixed at startup.
5. Ansible connects to the servers over SSH and makes the change. For the
   host-agent role that is one server at a time, with the binary from a checked
   source (a controller file, or a URL with a required SHA-256).

### 5. Host-level actions

A host agent acts only on **its own machine**, through a few narrow built-in actions
(today: removing unused container images). Each needs root, an explicit flag
(`-allow-changes`), and the token. It never reaches out to other servers, and it
never accepts a command string.

## Handoff barriers

- **Host to cluster.** Events flow up; nothing in the host depends on the answer.
- **Unknown to enrolled.** A host is not trusted until Kubernetes vouches for it or a
  person approves it with a token.
- **Cluster to host (not built).** How the cluster agent would authenticate when it
  proxies a host-level action to a host agent is undecided; today it links to hosts
  instead of acting on them.
- **Request to playbook.** A web request becomes a catalog playbook with explicit
  servers, never a free-form command.
- **Agent to Ansible.** The agent decides and records; Ansible acts.

## Security barriers

Three separate authentication boundaries, with different mechanisms so one leaking
does not open the others:

| Boundary | Proves | How |
|---|---|---|
| User to agent | a person | account sign-in, or the access token |
| Host to cluster agent | a specific enrolled host | Ed25519-signed requests |
| Enrollment | an operator allowed this host | a one-time join token, or a key that Kubernetes vouches for |

And the rule about commands, in three levels:

| Level | Can do to a server | Default |
|---|---|---|
| Cluster agent | nothing directly; launches playbooks only | the only thing it does |
| Host agent | acts on its own machine through narrow actions | read-only unless `-allow-changes` |
| Override: direct commands | one command at a time over SSH, skipping the playbook | **off** |

More barriers:

- **Enforced in code.** `.validator/validate-agent-boundary.py` fails the build if a
  Go package outside a short list imports `os/exec` or an SSH library, or starts a
  process another way. Adding to that list is the review.
- **Fail closed.** A bad, used, or expired token is rejected; a host ID
  re-enrolling with a different key is rejected; a pending host cannot send events.
- **The override is deliberate.** It is turned on from the Security page by a
  signed-in person, with a time limit (default 1 hour), a typed confirmation, and a
  security record written **before** the change. The host must be a node the cluster
  lists, one command at a time, output capped at 64 KiB, and every command is
  recorded before it runs.
- **AI assistants.** Through MCP, an assistant can turn the override **off** freely
  but can turn it **on** only if its server was started with
  `-allow-security-changes`, with a recorded reason and at most 24 hours. The
  approval prompt in the person's client is the real gate. It cannot launch
  playbooks or run commands.
- **Launching playbooks is the strongest lever.** It sits behind the access token even
  on loopback, runs one playbook at a time, only from the catalog, and records each
  launch.
- **Not built:** per-person accounts packaged for hosts and the pod, revoking or
  rotating a host key or the cluster certificate, and the cluster-to-host credential.

## Animation notes

Suggested frames: a host agent lights up alone on a node (no cluster); a pulse goes
out every five minutes, and a cluster agent appears later and starts catching the
pulses; the Kubernetes API stamps a host's key (the Path A handshake); an operator
asks for a change and a playbook rather than a command travels to the host through
Ansible, with a "record first" step before it leaves. Show the three authentication
boundaries as three different locks, and the direct-command override as a dimmed,
switched-off lever behind glass.
