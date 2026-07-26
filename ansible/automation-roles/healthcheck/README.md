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
- Kube API `/healthz` check using an explicitly supplied or locally detected
  RKE2 `kubectl`.
- Live verification that every required ServiceMonitor or PodMonitor exists,
  Adaetum Prometheus has an active healthy scrape pool for each one, and an
  installed Rancher Monitoring Prometheus has discovered the same resources.
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
- `healthcheck_check_observability`: verify monitor resources and Prometheus
  target discovery.
- `healthcheck_observability_target_retries` and
  `healthcheck_observability_target_delay`: bound how long the role waits for
  Prometheus target discovery to converge after GitOps reconciliation.
- `healthcheck_required_monitor_names`: stable monitor names required after
  GitOps reconciliation.

## Example overrides

```yaml
healthcheck_check_services:
  - sshd
  - tailscaled
healthcheck_disk_min_free_gb: 5
healthcheck_check_kube_api: false
healthcheck_log_lines: 100
```
