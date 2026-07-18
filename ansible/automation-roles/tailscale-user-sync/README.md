# tailscale-user-sync role

Synchronizes local Linux user accounts from a Tailscale ACL group so Tailnet
identity is preserved all the way down to the OS username.

This role is designed to run from the Ansible runner container (cron) so the
Tailscale API credential can live on the runner, not on every node.

## What it does

- Fetches the Tailnet ACL policy via the Tailscale v2 API (read-only).
- Extracts members from a configured ACL group (for example `group:local-admin`).
- Creates/updates a local user per member (locked password; no local secrets).
- Tracks managed users via a dedicated local group and removes users that are no
  longer in the ACL group (optional).
- Optionally grants sudo to the local admin group (typically passwordless, since
  SSO users do not have local passwords).

## Access model (how logins work)

This role is intentionally simple:

- **Tailscale** is the identity provider and the network/SSH gate.
- **Linux** still requires a real local user account to log in as (this role
  creates/removes those accounts).

The default model is:

- Source-of-truth group in Tailscale ACL policy: `group:local-admin`
- Local Linux group used for access: `local-admin`
- Local Linux group used to track managed accounts: `tailscale-managed`

Operationally:

1) Add a person (their SSO email) to `group:local-admin` in your Tailnet ACL
   policy.
2) On the next sync run, the role creates a local user derived from that email
   address (example: `jane.doe@example.com` -> `jane.doe`).
3) That user is placed into the local groups `local-admin` and `tailscale-managed`.
4) When you remove someone from `group:local-admin`, their local account is
   deleted on the next sync run (including home directory if configured).

Sudo:

- By default, this role installs a sudoers rule for `%local-admin` with
  `NOPASSWD:` because SSO users generally do not have local passwords.
- If you want sudo to prompt, set `tailscale_user_sync_sudo_nopasswd: false`,
  but you must also provide a second factor for sudo (for example, a local
  password policy, a directory-backed PAM setup, or another approved method).

Sync delay:

- This role is typically run by the ansible-runner cron container.
- The maximum delay between a Tailnet group change and local account creation/
  removal is approximately your cron interval (`CRON_SCHEDULE`), plus the time
  it takes for the playbook to run.
- The default schedule in `ansible/ansible-host-config-sync.yaml` is `*/10 * * * *`
  (worst case ~10 minutes).
- The recurring runner uses `playbooks/day2.yml`. Full `bootstrap.yml` is an
  explicit install/recovery operation and must not be scheduled because it can
  regenerate bootstrap credentials that OpenBao owns after Phase 40.
- The runner mounts its OAuth client from the OpenBao-backed
  `ansible/tailscale-user-sync` Secret. It does not source the bootstrap `.env`,
  so a stale installer value cannot override a rotation made in OpenBao.

## Defaults

Key settings in `defaults/main.yml`:

- `tailscale_user_sync_enabled`: toggle the role on/off (default false).
- `tailscale_user_sync_source`: `tailscale_acl` or `static`.
- `tailscale_user_sync_acl_group`: ACL group key to read (default `group:local-admin`).
- `tailscale_user_sync_local_admin_group`: local Linux group for access (default `local-admin`).
- `tailscale_user_sync_managed_group`: local Linux group used to track managed users.
- `tailscale_user_sync_remove_absent_users`: delete users not in the ACL group.
- `tailscale_user_sync_remove_home`: remove home directories when deleting users.
- `tailscale_user_sync_shell`: login shell for created users.
- `tailscale_user_sync_sudo_enabled`: install sudoers policy for the admin group.
- `tailscale_user_sync_sudo_nopasswd`: use `NOPASSWD:` (recommended for SSO).

Credentials:

- `tailscale_user_sync_api_token`: API key or OAuth access token (recommended via env).
- Or: `tailscale_user_sync_oauth_client_id` + `tailscale_user_sync_oauth_client_secret`
  to mint a short-lived access token at runtime.

## Example overrides

```yaml
tailscale_user_sync_enabled: true
tailscale_user_sync_acl_group: "group:local-admin"

# Prefer minting a token each run from a trust credential:
tailscale_user_sync_oauth_client_id: "{{ lookup('ansible.builtin.env', 'TAILSCALE_OAUTH_CLIENT_ID') }}"
tailscale_user_sync_oauth_client_secret: "{{ lookup('ansible.builtin.env', 'TAILSCALE_OAUTH_CLIENT_SECRET') }}"
```
