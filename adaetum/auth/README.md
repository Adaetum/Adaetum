# Auth service

Account sign-in for the Adaetum agent. It is [Better Auth](https://www.better-auth.com)
(maintained upstream: we upgrade by bumping the pinned version, we do not write
authentication) running on Bun with plain SQLite, plus the small set of rules that let every
host sign people in without the cluster. The design and the reasons are in
[`agent/docs/recovery-login.md`](../agent/docs/recovery-login.md); read that first.

It is a separate process from the Go agent on purpose: two processes, two database files,
one owner each. The agent talks to it over a Unix socket only it can open, and proxies just
the sign-in routes to browsers.

## What is here

| File | What it does |
|---|---|
| `src/accounts.ts` | **The replication contract**: version counter, epoch, snapshot, and every rule for accepting or refusing a copy. No network code. |
| `src/auth.ts` | Better Auth configured (email and password, no open sign-up, roles) and the few direct actions on people. |
| `src/breakglass.ts` | The always-active break-glass owner, kept in step with files from OpenBao. |
| `src/setup.ts` | The first-run setup code, shown only on the machine. |
| `src/service.ts` | HTTP routes: public (`/api/auth/*`, `/api/setup`, `/api/auth-state`) and internal (`/internal/*`, secret and socket only). |
| `src/server.ts` | Entry point: environment, files, socket. |

## Run it

```bash
cd adaetum/auth
bun install
bun test                       # 44 tests: the rules, the flows, and the edge cases
bun src/server.ts              # development: loopback port 8787
```

There is no default account, in development or anywhere else. While nobody has an account
the service prints a first-run setup code (and keeps it in `AUTH_STATE_DIR/setup-code`);
entering it on the sign-in page creates the first owner.

With the agent: start this with `AUTH_SOCKET=/path/a.sock AUTH_STATE_DIR=/path`, then start the
agent with `-auth-socket /path/a.sock -auth-secret-file /path/internal-secret` (the service
creates that file, mode 600, on first start). Without those flags the agent behaves exactly as
before: the access token alone.

## Settings (environment)

| Variable | Meaning |
|---|---|
| `AUTH_SOCKET` | Unix socket to listen on (production). Without it, a loopback port for development. |
| `AUTH_STATE_DIR` | Where the database, secrets and setup code live. Everything is created private (mode 600). |
| `AUTH_MODE` | `primary` (owns the accounts: the cluster, or a host with no cluster) or `replica`. The agent sets `replica` itself when a host joins a cluster, and the service remembers it. |
| `AUTH_BREAKGLASS_DIR` | A directory holding two files, `email` and `password` (OpenBao delivers them in the cluster). The service keeps one always-active owner account matching them: it creates it, follows the password when it changes, and refuses to remove or demote it. It does not count as a person, so the first-run setup code still works. Unset, there is no such account. |
| `AUTH_BASE_URL` | Used for cookie flags. Use `https://...` where the agent serves TLS. |

## Not done yet

Packaging (a systemd unit and Ansible role for hosts, a container and manifest for the pod), a
Linux build of the compiled binary, passkeys, and write-through of account changes made at a
host to the cluster. See "Not built" in the design doc.
