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
| `src/setup.ts` | The first-run setup code, shown only on the machine. |
| `src/service.ts` | HTTP routes: public (`/api/auth/*`, `/api/setup`, `/api/auth-state`) and internal (`/internal/*`, secret and socket only). |
| `src/server.ts` | Entry point: environment, files, socket. |
| `src/demo.ts` | Opt-in development account. |

## Run it

```bash
cd adaetum/auth
bun install
bun test                       # 41 tests: the rules, the flows, and the edge cases
AUTH_DEMO=1 bun src/server.ts  # development: loopback port 8787, with a demo owner
```

The demo owner (`demo@adaetum.test`, password `demo-password-123`) exists only with
`AUTH_DEMO=1`, only when nobody has an account yet, and is refused under
`NODE_ENV=production`. The values are public on purpose; they protect nothing.

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
| `AUTH_BASE_URL` | Used for cookie flags. Use `https://...` where the agent serves TLS. |
| `AUTH_DEMO` | `1` makes the development demo owner (never in production). |

## Not done yet

Packaging (a systemd unit and Ansible role for hosts, a container and manifest for the pod), a
Linux build of the compiled binary, passkeys, and write-through of account changes made at a
host to the cluster. See "Not built" in the design doc.
