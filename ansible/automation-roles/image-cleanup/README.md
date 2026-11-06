# Image cleanup

## What it does

Installs a systemd timer that runs `adaetum-agent images run` once a day on every
node, configured from `spec.imageCleanup` in `platform.yaml`. The agent plans which
container images no container on the node uses, re-checks each one right before
removing it, never touches images RKE2 itself needs, and records every decision in
`/var/log/adaetum/image-cleanup.log`. Removed images are downloaded again if an app
needs them later. See `agent/docs/image-cleanup.md` for the safety model.

The role owns the unit files and the timer state. It does not build the agent: give
it a binary with `image_cleanup_agent_src`, or install the binary another way.
Disabling the policy stops and disables the timer.

## Defaults

On, `aggressive`, daily at `04:00` (the node's local time, plus up to 15 minutes of
random delay), at most 100 images per run, regardless of disk usage, and real
removal. `careful` also keeps images that rollbacks or other servers might need.
`onlyIfDiskOverPercent` skips the run while the disk is below that percentage.
`checkOnly` records what would be removed without removing anything.

## Example overrides

Edit `platform.yaml`; do not pass a second cleanup policy through `.env`:

```yaml
spec:
  imageCleanup:
    enabled: true
    level: careful
    time: '03:30'
    maxImagesPerRun: 50
    onlyIfDiskOverPercent: 70
    checkOnly: false
```
