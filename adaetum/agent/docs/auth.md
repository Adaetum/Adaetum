# Authentication boundaries

Three separate boundaries, with different mechanisms on purpose, so that one
leaking does not open the others. Code: `internal/identity`, `internal/cluster`,
`cmd/adaetum-agent/exposure.go`, `web/src/lib/auth.ts`.

| Boundary | Who proves what | How |
|---|---|---|
| **User to agent** (host or cluster) | A person | Their own account (email and password, see [recovery login](recovery-login.md)) when the agent is started with `-auth-socket`, or the access token (`token create`) sent as `Authorization: Bearer`. The token always works and never depends on the auth service; it is the emergency way in. The UI keeps it in the browser tab only. Required for every `/api` call whenever the agent listens beyond loopback, always over HTTPS. |
| **Host to cluster agent** (machine to machine) | A specific enrolled host | The host has its own Ed25519 key. Every request is signed (method, path, body, timestamp, one-time nonce). The cluster stores only the public key, so nothing it holds can impersonate a host. |
| **Enrollment** (becoming known) | An operator allowed this host to join | A one-time join token, minted by a signed-in person, valid up to 7 days, stored only as a hash. |

## Trust: who is allowed to join

A name proves nothing (anyone on the network can claim `node-a`). A host is trusted
only because **an operator minted a token** *and then* one of these holds:

1. **The host is already a Kubernetes node** (hostname matches a node the cluster
   lists). The ISO install already joined it to the cluster, so the cluster trusts
   the machine. Approved at once.
2. **It is not a node** (the "add a host with just this tool" path). It waits as
   **pending** until a person approves it. The cluster agent then (in future)
   manages its install.
3. **Kubernetes cannot be read.** Never auto-approve; wait for a person.

Fail-closed everywhere: a bad, used, or expired token is rejected; a host ID
re-enrolling with a different key is rejected; a pending host cannot send events;
a host can only write its own events.

## Host to cluster: how the host trusts the cluster

The cluster agent makes its own certificate, so there is no authority to ask. The
host **pins its fingerprint** once, when joining:

```bash
# on the cluster agent: the fingerprint is printed at startup
adaetum-agent cluster -token-file /etc/adaetum/agent-token ...
# on a new host, with a token minted from the cluster agent (written to a file):
adaetum-agent join -cluster https://cluster-agent:8444 \
  -fingerprint 4B:AA:... -token-file /root/join-token -data-dir /var/lib/adaetum
```

The join token is read from a file or `ADAETUM_JOIN_TOKEN`, never a command-line
argument. If the fingerprint does not match, the token is not sent.

Who may run commands on a server at all is a separate boundary: see
[security boundary](security-boundary.md).

## Launching playbooks is the most powerful lever

Running a playbook (for example `host-agent`, which installs a binary as root on a
server) means whoever can launch it controls the cluster. So the launcher
(`internal/ansiblerun`) is behind the access token even on loopback, runs one
playbook at a time, only from a catalog, with explicit servers, rehearsal-first, and
records every launch before starting it. Remaining gaps: the record shows *that*
someone with the token launched it but not *which person* (one shared token), and
binaries are verified only by SHA-256, not signed.

## Not built yet (decide before building)

- **Cluster to host**: how the cluster agent authenticates when it proxies a
  host-level action to a host agent. Proposed: a per-host credential issued at
  enrollment, separate from the signing key. Until then the cluster agent links to
  hosts instead of acting on them.
- **People**: per-person accounts that still work on a host when the cluster is down, with
  roles and the person named in the record: built and tested, not yet packaged for hosts
  and the pod. See [recovery login](recovery-login.md).
- **Removing or revoking a host**, and rotating a host key or the cluster certificate.
- **ISO installs** carrying a join token so enrollment is automatic.
