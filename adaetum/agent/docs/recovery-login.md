# Recovery login: the milestone

**Status (2026-11-05): decided; the sign-in, the account copy between machines and their
rules are built and tested; packaging and a few features are not (see "Built, and not").** This
is the use case the whole agent design has to serve. If a design choice makes it fail, the
choice is wrong.

## The scenario

1. The application is installed on a host: the Go agent and the auth service (Bun
   running Better Auth). The first person is prompted to sign up and becomes the owner.
   There is no cluster yet, so that account lives on the host.
2. Setup then provisions Kubernetes and the rest of the platform, and the cluster
   agent pod is deployed.
3. People sign in at the pod, the normal front door. The owner adds a user there.
4. **Kubernetes breaks.** For whatever reason, even on a one-host cluster.
5. The person goes to the host's own copy of the application and **signs in with the
   most recent copy of the accounts**, no more than about a dozen seconds old.
6. An owner turns on the "run commands" switch on that host (Settings > Security:
   time-limited, confirmed, recorded) and runs the break-glass playbooks.
7. The host is repaired, Kubernetes recovers, the pods return, and everything syncs.

The point of the product: **a layman with a dead cluster can still sign in with their
own account and fix it.** An access token they would have to remember is not an
answer (the token stays as an admin and command-line path only).

## Second scenario: six hosts, any door

Added 2026-11-05. Five or six hosts have been added over time and nobody has ever
signed in to most of them.

- **A new host needs no manual step.** Once it is enrolled (join token, see
  [auth](auth.md)) it receives the full current account list on its first sync, and
  small changes after that. Accounts go only to enrolled hosts.
- **Any door works.** A person who bookmarked a host's own URL instead of the app URL
  and makes a change there (adds a user, changes a setting) must see it reach the pod
  and then every other host, within seconds. The host does not keep the change to
  itself: it sends it to the cluster, which applies it, and the result flows out to
  all hosts.
- **The tables fall into three classes**, and only the first needs strong care:

| Class | Examples | Behaviour |
|---|---|---|
| **A. Key tables** | accounts, roles, invites, per-person settings | Small and rarely changed. Replicated to every enrolled host within seconds. Must not be lost or diverge. |
| **B. Bulk tables** | host logs, usage history, events | Large. Stay on the host and flow up to the cluster's aggregate, best effort. Losing some is acceptable. Never sent to other hosts. |
| **C. Local only** | sessions, caches | Never leave the host. |

- **Why not strict real-time consensus (Raft and similar).** It needs a majority of
  nodes up to accept any write, so a broken cluster or a lone host could not change
  anything, which defeats the milestone above. Class A uses eventual replication
  instead: it converges in seconds and keeps working offline.
- **Merge rule if a host may accept a change while the cluster is down:** per row,
  the latest change wins (a hybrid logical clock decides), and deletes are kept as
  markers so they cannot be undone by a late copy. Not designed in detail.

## What this requires

- **Every host has the accounts, and signs in locally.** Nothing in the login path may
  depend on the cluster, Kubernetes, or any external identity provider. (Authentik
  lives in Kubernetes, so it cannot be the login.)
- **Fresh within seconds.** A host pulls the accounts about every 10 seconds while it
  can reach the cluster, and keeps its last copy when it cannot. That staleness is
  intended: an offline host must still log people in.
- **Named people.** The "run commands" switch and every launch are attributed to a
  person, not a shared token. Only an owner or admin may flip it. This closes the
  "which person?" gap in [auth](auth.md).
- **No hard-coded fallback.** Emergency access is minted on the machine by someone
  who already has SSH over Tailscale (a one-time code), never a stored default
  password.
- **No split brain.** One owner per piece of data (see below).

## The design

```
cluster:  auth service (own file) -> Go agent --signed host channel--> host Go agent -> host auth service (own file)
```

- **Better Auth** keeps accounts, passkeys and sessions. It is maintained upstream and
  we upgrade by bumping its version; we do not write authentication ourselves.
- **Auth uses plain SQLite (Bun's built-in), in its own file. Go uses Turso for
  events and logs.** They never share a file: Turso refuses a second process on a
  file (tested), and Better Auth cannot yet be compiled to one binary on the Turso
  engine (tested). Turso has nothing to do with auth.
- **Accounts are owned by the cluster** (the pod) and copied down one way: only the
  user and password rows, replaced in one transaction. Sessions are never copied, so
  each host has its own. The host's Go agent does the carrying over the existing
  signed channel; only the auth service writes its own file.
- **Two-way only where it is cheap:** a person's own settings (theme and similar) are
  small and last-write-wins. Creating accounts, changing passwords and removing
  people stay owned by the cluster. Doing them on a host while the cluster is down is
  an open question below.
- **Hosts and the pod run the same bundle.**

## The cluster is always the master (decided 2026-11-05)

Hosts are deleted and rejoined, not restored, and accounts are only logins and a few
per-person settings, so a simple rule beats an election:

- **Before the first handover** a host owns its accounts. Add people and change settings freely.
- **The handover:** the first time the cluster comes up with nobody in it, it takes the host's
  accounts, and the host becomes a replica from then on.
- **After that the cluster always wins.** A replica never offers its list to a cluster that has
  people. If its list has a different history, or is newer (the cluster was restored from an old
  backup), the cluster's list **replaces** it. This is what makes a rebuilt, rejoined or
  previously standalone host come back correct with no manual step.
- **Hosts are read-only for accounts** after the handover. There is nothing to merge and nothing
  to lose, so no election, term numbers or write queue.
- **Accepted cost:** if the cluster's own volume is restored from an old backup, people added after
  that backup disappear from every host, and removed people return. The sign-in itself keeps
  working, because a copy with nobody in it or with no owner is still refused.
- **Edge to know:** if someone completes first-run setup on the cluster before the handover, the
  cluster has people and the host's first owner is replaced by that cluster owner.
- **Hosts are Kubernetes' to track, not ours.** The cluster agent reads the node list every
  5 minutes and removes a host (key and stored events) that was approved because Kubernetes
  listed it and is gone from the list on two reads in a row. It never acts on an empty or
  unreadable list, and never touches a host a person approved by hand. A removed machine
  has to enrol again with a new join token.

## Rules that keep it safe

- **The first account must reach the pod.** The owner is created on the host before
  the cluster exists. The pod's accounts are seeded from the host that deployed it.
  Otherwise a fresh, empty pod would sync down over the host's only owner and lock
  everyone out.
- **A host refuses a snapshot that is empty or has no owner,** even when the cluster is the master.
- **A removed person stays able to sign in on an offline host until it reconnects.**
  Accepted: this is the cost of the scenario above.
- **Every host holds password hashes for every account.** A compromised host exposes
  them to offline cracking, so the sync is only accepted from the cluster, over the
  signed channel, and hashes are strong.
- **The auth service listens only on a local socket or loopback.** Go reaches it with
  a shared secret; otherwise login could be bypassed.

## Built, and not

**Built and tested** (Bun tests in `adaetum/auth/`, Go tests in `internal/accounts`, both run against the
real service; the milestone scenario above is `TestTheRecoveryScenario`):

- The auth service (`adaetum/auth/`): Better Auth on SQLite, roles, first-owner setup code, the version
  counter and every rule for accepting a copy (empty, ownerless, other history, older, other
  layout, malformed, or sent to a primary that has people are all refused).
- The agent: sign in with an account or the access token (`-auth-socket`,
  `-auth-secret-file`); the token never depends on the auth service; roles gate every change by
  HTTP method; a member can look, an admin can change, only an owner manages people; a
  signed-in person is named in the Security record and the Ansible launch record.
- Copying: a joined host runs a replica, pulls about every 10 seconds, keeps its last copy when the
  cluster is unreachable, seeds an empty cluster from its own accounts, and lets the cluster's list
  replace its own when the history differs or the cluster is older. It only warns for a different
  account layout or a host list with no owner.
- Removing a host (`DELETE /api/v1/hosts/{id}`) revokes its key at once. Hosts reach the cluster's account routes with the same signed requests as
  event delivery.
- The UI: sign-in, first-run setup, Settings > Profile (who you are, the theme) and People.

**Not built:**

- **Running the service**: a systemd unit and Ansible role for hosts, a container and manifest for
  the pod, and a Linux build of the compiled binary. Today it is started by hand (`adaetum/auth/README.md`).
- **"Run commands" on a host at run time.** Step 6 of the scenario needs a host to let an owner turn
  the launcher on while the cluster is down. Today a host's switches are fixed when it starts, and
  `security-boundary.md` says so deliberately. Changing that is a security-policy decision, not
  plumbing, so it is left for an explicit yes. The attribution it needs (the person's name in the
  record) is done.
- **Account changes made at a host's own address** are refused there (it is a replica) instead of
  being forwarded to the cluster. People use the cluster address to change accounts.
- Passkeys, the "setup code on a console" for hosts installed by Ansible, and password reset by email.

## What was checked first (2026-11-05, scratch folders)

- Better Auth 1.7 on Bun's SQLite: sign-up, sign-in, session, wrong password rejected.
  Compiled with `bun build --compile`: 61 MB, about 82 MB of memory.
- Copying only user and password rows into a second database let that "host" sign in
  with no contact with the cluster. Deleting the user and syncing again blocked the
  sign-in and invalidated the host's session. Orphaned session rows must be cleaned up.
- Turso (0.8.3, macOS): two processes on one file: the second is refused with "File is
  locked by another process", the first finishes, integrity check ok.
- Better Auth on a Turso file via Kysely's SQLite dialect works from source, but the
  compiled binary fails with "Cannot find native binding".
- Turso under a log flood (macOS): about 118,000 rows a second batched, reads during
  writes at most 11 ms, pruning 150,000 rows in 0.18 s. Pruning does not shrink the
  file; plan retention by dropping old files.

## Not verified

- **Turso on Rocky Linux 10.** Only macOS was run. The agent is not installed on the
  production host, so there is no evidence either way. Must be run before anything
  depends on it (see [cluster](cluster.md)).
- **SELinux** with the auth service and its socket.
- **The repair path end to end:** break-glass playbooks launched from a host agent with
  the "run commands" switch on. That switch is only available if the agent was
  started allowing it.
- **The compiled binary on Linux, and the whole thing on Rocky Linux 10.** All tests ran on macOS.

## Ways this could break (devil's advocate, 2026-11-05)

Ranked. Items 1 to 3 must be decided before building; the rest are build-time rules.

1. **The pod loses or rolls back its data.** Its volume is lost or restored from an old
   backup, so its snapshot is older than the hosts' copies. *Decision (2026-11-05):* the
   cluster wins anyway (see "The cluster is always the master"). Hosts follow it back, new
   people are lost, removed people return, and nobody is locked out. Hosts are rebuilt, not
   restored, so the cluster's own volume is the only place this can happen.
2. **A full disk blocks sign-in exactly when it is needed.** Disk pressure is a common
   cause of node outages (these nodes were at 68% and 83% before cleanup), and a log
   flood fills disks. *Fix:* a hard size cap on the bulk (class B) files, auth data in
   its own small file, and the access token stays as the last resort.
3. **The first sign-up on a fresh host can be claimed by anyone who reaches it first.**
   *Fix:* creating the first owner needs a setup code shown on the machine (or is only
   allowed from loopback); later signup is invite-only.
4. **Passkeys do not follow the person between doors.** A passkey is tied to one
   hostname, and each host has its own name, probably with a self-signed certificate
   that browsers warn about. *Fix:* passwords first. Valid certificates for host names
   (for example from Tailscale) are worth checking before passkeys.
5. **Signing someone out or resetting their password cannot reach other hosts'
   sessions,** since sessions are local; a stolen session lives until it expires
   (7 days by default). *Fix:* replicate a per-person "sessions valid after" time as
   class A data, and have every host check it.
6. **Version skew.** Hosts upgrade at different times; plugins add columns. The scratch
   copy used `SELECT *`, which breaks on any difference. *Fix:* explicit column lists,
   a schema version in every snapshot, and a host refuses a snapshot it cannot read.
7. **Offline changes on two hosts collide.** Both add the same email with different
   ids. *Fix:* keep accounts read-only on a host while the cluster is down (the
   default above).
8. **The auth service is a new single point of failure on each host.** *Fix:* run it
   supervised with restart, and keep the token path working without it.
9. **Clock drift** breaks last-write-wins and session expiry on a host with no time
   sync. *Fix:* require time sync, and use a logical clock for ordering.
10. **Scope creep:** each extra replicated table turns this into a distributed database.
    *Fix:* a short list of class A tables, each with one owner; anything not on it is
    class B or C.

## Open questions

- Must a host be able to create users or reset passwords while the cluster is down?
  Default: no; use a recovery command run over SSH on that host.
- Roles: which actions an owner, admin and member may take.
- How the account sync is triggered (timer only, as built, or also pushed on change).
- With the pod down, may a host accept an account change (queued and merged later
  by the rule above), or stay read-only for accounts? Default: read-only for accounts,
  and per-person settings may still change.
