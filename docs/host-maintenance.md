# Automatic host maintenance

Adaetum installs Rocky Linux updates with `dnf-automatic` and coordinates
required reboots with the upstream Kured DaemonSet. The recovery repository's
`platform.yaml` is the only public policy source: the recurring Ansible runner
configures each host, while Argo CD reconciles Kured and Prometheus safety
rules.

The committed defaults install all available updates daily. Host timers have a
random delay to reduce simultaneous package activity. Kured performs disruptive
maintenance inside the daily 03:00–06:00 America/Chicago window and holds a
cluster-wide lock, with one reboot allowed at a time.

On a one-node cluster, an automatic reboot necessarily interrupts every
in-cluster service. On a multi-node cluster, workload availability still
depends on replica placement, storage availability, and valid
PodDisruptionBudgets.

## Configuration

Edit `spec.hostMaintenance` in `platform.yaml`, then validate and render it:

```bash
task platform:validate
task platform:render
```

`enabled` is the master pause. It stops the DNF timer and makes Kured's reboot
check inert. `updates.policy` supports `full`, `security`, `download-only`, and
`disabled`. `updates.onCalendar` accepts a systemd calendar expression and
`randomizedDelay` accepts a systemd duration.

The `reboots` mapping controls the Kured window, timezone, check period,
concurrency, drain timeout, grace period, forced-drain behavior, lock expiry,
lock-release delay, and pre-reboot delay. Keep `concurrency: 1`,
`forceReboot: false`, and `lockTtl: 0` unless an operator has deliberately
accepted the reduced safety. A nonzero lock TTL can let another node proceed
after the previous lock holder fails.

`safety.prometheusGate` makes Kured refuse new reboots while an
`AdaetumMaintenance*` alert is firing. `blockingPodSelectors` optionally blocks
maintenance whenever pods matching any listed selector exist. Metrics and reboot annotations are
controlled under `observability`.

## Operations

Check pending reboots and current activity:

```bash
kubectl -n kube-system get pods -l app.kubernetes.io/name=kured -o wide
kubectl -n kube-system logs daemonset/kured --tail=200
kubectl get nodes -o custom-columns='NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status,UNSCHEDULABLE:.spec.unschedulable,REBOOTING:.metadata.annotations.weave\.works/kured-reboot-in-progress,LAST-NEEDED:.metadata.annotations.weave\.works/kured-most-recent-reboot-needed'
kubectl -n kube-system get daemonset kured -o jsonpath='{.metadata.annotations.weave\.works/kured-node-lock}{"\n"}'
```

Prometheus exposes `kured_reboot_required` per node. Kubernetes Events and the
JSON Kured logs show drain, reboot, and uncordon progress. The
`AdaetumMaintenance*` alerts explain why the next node is blocked.

For a planned pause, set `spec.hostMaintenance.enabled: false`, render, commit,
and allow both the day-two runner and Argo CD to reconcile. For an emergency
pause, take Kured's lock:

```bash
kubectl -n kube-system annotate daemonset kured \
  weave.works/kured-node-lock='{"nodeID":"manual"}' --overwrite
```

Release that manual lock only after verifying every node is Ready, pressure-free,
schedulable, and required DaemonSets are available:

```bash
kubectl -n kube-system annotate daemonset kured weave.works/kured-node-lock-
```

## Rollback and upgrades

To stop automatic package installation while retaining downloaded metadata,
set `updates.policy: disabled`. To keep patching but prevent reboots, set
`reboots.enabled: false`. A complete rollback sets `enabled: false`; the role
leaves installed packages in place because package downgrades are not a safe
automatic rollback mechanism.

Kured is pinned in `pods/operations/kured.app.yaml.tmpl`. Upgrade that chart only
after reviewing upstream configuration changes, rendering the profile, and
testing drain and reboot recovery on a disposable node. Never remove a live
Kured lock or change concurrency during an active reboot.
