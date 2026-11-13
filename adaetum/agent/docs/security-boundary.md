# Security boundary: who may run commands on a server

**The rule:** the agent does not run commands on servers. It changes a server by
**launching an Ansible playbook**, and Ansible does the work. Running a command
directly is an explicit, off-by-default override.

## The three levels

| Level | What it can do to a server | Default |
|---|---|---|
| **Cluster agent** (the pod) | Nothing directly. It has **no host-level access**. It can only launch Ansible playbooks (`internal/ansiblerun`). | the only thing it does |
| **Host agent** (on each server) | Acts only on **its own machine**, through a few narrow built-in actions (today: removing unused container images), each gated by root, a flag, and a token. It never reaches out to other servers. | read-only unless started with `-allow-changes` |
| **Override: direct commands** (`internal/hostaccess`) | Runs one command at a time on a named server over SSH, **skipping the playbook**. | **off** |

Playbooks are the default because they are reviewed, catalogued, rehearsable
(`--check`), and recorded. A direct command is none of those, so it is a deliberate
exception, not a convenience.

## Turning the override on

Two ways, both on the **cluster agent** only:

- **The Security page** (a switch in the web interface). The switch is global: it
  applies to every server at once and takes effect on the next request. Turning it
  **on** needs a signed-in person, a **time limit** (1 hour, 4 hours, 24 hours, or
  until turned off; the default is 1 hour, and it turns itself off), and typing the
  setting's name to confirm. The agent checks that confirmation itself. Every change
  is written to a security record **before** it takes effect; if it cannot be
  written, nothing changes. Turning it off is always allowed and also recorded.
- **A startup flag** (`-allow-host-ssh`), which sets the starting state until
  someone flips the switch. The same goes for launching playbooks (`-allow-ansible`).

Neither can get around the prerequisites, which are checked on every request. If any
fails, the switch is shown as unavailable and cannot be turned on:

- an access token file is set, and the `ssh` program exists;
- `-host-ssh-audit` (for the override) and `-security-audit` are writable;
- the agent is not in `-ssh` development mode.

Then, for every command: the person is signed in; the server is one the cluster
lists (or one named in `-host-ssh-hosts`; if Kubernetes cannot be read, none);
the name is a plain name (no `user@`, no leading `-`, no patterns); one command at a
time; a time limit (default 1 minute, at most 10); output capped at 64 KiB; and the
command is **recorded before it runs**. A command that cannot be recorded is not
run. The record holds who, which server, the command text, the exit code and the
size of the output (not the output). Do not put secrets in commands: the command
text is recorded.

**What a web switch costs.** Anyone holding the access token can flip it, so the
token is what protects it (plus the confirmation, the time limit, and the record).
There is one shared token today, so the record says "operator", not which person.
Per-person accounts are the fix and are not built.

**An AI assistant through MCP can flip the switches too** (by design). Start the MCP
server with the cluster agent's address, its certificate fingerprint and an access
token file, and add `-allow-security-changes` to let it turn switches **on**:

```bash
claude mcp add adaetum -- adaetum-agent mcp \
  -cluster https://cluster-agent:8444 -fingerprint 4B:AA:... \
  -token-file ~/.adaetum/token -allow-security-changes
```

What bounds it, since a model can type a confirmation as easily as a person:

- **Opt-in is in your config, not the model's hands.** Without
  `-allow-security-changes` the assistant can read the switches and turn them
  **off**, never on. Turning off is always allowed.
- **Your client asks first.** The tool is marked as changing things, so a
  well-behaved client (Claude Code, for one) asks you to approve each call. That
  approval is the real gate. A client set to auto-approve removes it; do not
  auto-approve this tool.
- **Stricter than the page.** The agent itself requires a **reason** (recorded) and
  a **time limit of at most 24 hours** for an assistant; "until I turn it off" is
  only for a person at the page. Every other rule (prerequisites, the record before
  the change) still applies.
- **Recorded as MCP.** The security record says the change was made via `mcp`, and
  why, so you can see what the assistant did and what it said it was doing.
- **Only the switch.** The assistant can flip the switch; running a command or
  launching a playbook are separate actions this MCP server does not expose.

**The assistant built into the page** (`-enable-assistant`, off by default) is held to
the same rules: it acts only through the agent's own API with the same token, every
change it proposes waits for your Approve in the page, it cannot turn a switch on (only
off), and its changes are recorded as made through the assistant with a reason. It sends
the conversation to the AI service you choose (Anthropic, or a free OpenAI-compatible one for testing), which is why it is opt-in. See [memory](memory.md).

**Host agents** do not get a remote switch: their levers (launching playbooks,
removing images from the page) are fixed when the agent starts, and their Security
page shows them read-only. The cluster agent cannot flip a host's levers, because
reaching into a host is exactly what this boundary forbids. If you want a central
policy that hosts follow, that is a separate decision (see below).

Not built: interactive SSH sessions (a terminal in the page). The override runs one
command and returns its output.

## Starting runs

A **run** is one go at a *procedure*: a named list of steps the agent knows how to do
(bring a node up, unseal OpenBao), written in Go inside the agent and run by the run
engine (`internal/runs`). Starting one is a lever like launching a playbook, because a
procedure can change the machine it runs on:

- **Off unless asked for.** A host agent starts with it off; `-allow-runs` turns it on
  at startup, and the Security page shows the switch ("Start runs") read-only, like the
  playbook launcher, because a host's levers are not changed from the network.
- **Never without the access token.** Starting needs a valid token file even on a
  loopback agent with no other login. Without one the switch reads as unavailable.
- **Recorded first.** A run's start, and each step before it runs, is written to the
  agent's journal before the work happens; if a record cannot be written, the step does
  not run. The record names who started it.
- **Reading and stopping are never gated.** Switching starting off stops new work
  without hiding or trapping a run in progress.
- **Destructive steps need a named approval** on the run, confirmed by a person. A run
  that reaches one without it stops and asks.
- **Run inputs are recorded in clear**, so a name that looks like a secret is refused;
  a step that needs a secret reads it from the secret authority itself.

Procedures act only through the adapters they are built with. The run engine does not
run commands itself, and it adds no way around the rule above: anything that must run a
command on a server still goes through a playbook, or a reviewed host action.

## How it is enforced, not just documented

`.validator/validate-agent-boundary.py` (a pre-commit hook; run it by hand with
`--self-test` to see it reject violations) fails if:

- any Go package other than the ones it lists imports `os/exec` or an SSH library,
  or starts a process by another route. Adding a package to that list is the review;
- the command package uses anything from `os/exec` but `LookPath`;
- the override is wired anywhere but the cluster agent's setup, or into `serve`
  (the host agent);
- this document goes missing.

So a new way to run commands cannot appear by accident: it needs a visible change to
the list, with a reason.

## Related

[auth](auth.md) (who may use the agent, and how hosts trust each other) and
[cluster](cluster.md) (how the page launches Ansible).
