# Host maintenance

## What it does

Configures Rocky Linux `dnf-automatic` from `spec.hostMaintenance` in
`platform.yaml`. The role owns package installation, `/etc/dnf/automatic.conf`,
the systemd timer override, and timer enablement. It never reboots a host;
the GitOps-managed Kured DaemonSet owns Kubernetes-aware reboot coordination.

The day-two play runs this role with `serial: 1`. Each host's systemd timer also
uses the configured randomized delay, while Kured's cluster lock is the strict
one-node-at-a-time boundary for cordon, drain, and reboot.

## Defaults

The public profile defaults to full daily updates. `security` applies only
security advisories, `download-only` stages packages without installing them,
and `disabled` stops and disables the timer. Disabling all host maintenance also
stops the timer.

## Example overrides

Edit `platform.yaml`; do not pass a second maintenance policy through `.env`:

```yaml
spec:
  hostMaintenance:
    enabled: true
    updates:
      policy: security
      onCalendar: "Mon..Fri *-*-* 01:30:00"
      randomizedDelay: 30m
```
