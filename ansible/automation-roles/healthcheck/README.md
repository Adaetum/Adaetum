# healthcheck role

Lightweight host checks for connectivity and basic system health. The role is
safe to run on a single node or across a fleet and is intended to be fast,
useful for troubleshooting, and low on side effects.

## What it checks

- Basic Ansible connectivity.
- Hostname, uptime, load averages, and CPU core count.
- Disk usage, inode usage, and free space threshold for `/`.
- Memory usage.
- Time sync status.
- DNS resolution and outbound ping.
- Tailscale status (non-fatal).
- Required services are running.
- Optional kube API `/healthz` check if `kubectl_cmd` is set.
- Recent logs for key services with error pattern matching.
- Writes a report to disk (optional).

## Defaults

These defaults live in `defaults/main.yml` and are applied automatically when
the role runs:

- `healthcheck_report_path`: where the report is written.
- `healthcheck_check_services`: services expected to be running.
- `healthcheck_disk_min_free_gb`: minimum free disk space for `/`.
- `healthcheck_write_report`: enable or disable report output.
- `healthcheck_log_services`: services to scan with `journalctl`.
- `healthcheck_log_lines`: number of log lines to fetch per service.
- `healthcheck_log_error_patterns`: patterns to flag in logs.
- `healthcheck_dns_name`: DNS name to resolve.
- `healthcheck_ping_target`: IP/host to ping.
- `healthcheck_cpu_load_max_per_core`: 1m load threshold per core.
- `healthcheck_check_kube_api`: enable kube API `/healthz` check.

## Example overrides

```yaml
healthcheck_check_services:
  - sshd
  - tailscaled
healthcheck_disk_min_free_gb: 5
healthcheck_check_kube_api: false
healthcheck_log_lines: 100
```
