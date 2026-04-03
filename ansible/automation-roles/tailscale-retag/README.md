# tailscale-retag role

Retags a node in Tailscale after platform install so it advertises the final
role + platform tags. The bootstrap path now applies only the final tag set and
fails if the node does not converge to those tags.

## What it does

- Reads current Tailscale tags from `tailscale status --json`.
- Builds the desired tag set from role + platform + cluster tags.
- Executes `tailscale up --advertise-tags ...` to apply the final tag set.
- Fails if the node does not converge to exactly the expected tags.

## Defaults

Key settings in `defaults/main.yml`:

- `tailscale_cluster_tag`: cluster tag (from `TAILSCALE_CLUSTER_TAG`).
- `tailscale_oauth_client_id` / `tailscale_oauth_client_secret`: OAuth client creds.
- `tailscale_oauth_ttl`: OAuth auth key TTL (default `1h`).
- `tailscale_retag_enabled`: master toggle (default `true`).
- `tailscale_retag_force`: force retag even if tags appear correct.
- `tailscale_retag_role_tag`: role tag (e.g. `tag:server` or `tag:agent`).
- `tailscale_retag_platform_tag`: platform tag (e.g. `tag:rke2`).
- `tailscale_key_expiry_fail_on_error`: fail if key-expiry disable is skipped/fails (default `false`).

## Example overrides

```yaml
tailscale_retag_enabled: true
tailscale_retag_role_tag: "tag:server"
tailscale_retag_platform_tag: "tag:rke2"
tailscale_cluster_tag: "tag:cluster-main"
```
