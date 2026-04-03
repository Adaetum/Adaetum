## Kubescape

This cluster installs Kubescape through the upstream `kubescape-operator` Helm
chart via Argo CD.

What it does
- Enables Kubescape's supported operator path instead of the older ad hoc CLI
  CronJob model.
- Turns on continuous scan features so findings are available in-cluster instead
  of only as JSON files on a PVC.
- Enables Prometheus `ServiceMonitor` objects for future observability wiring.

Notes
- The Argo CD app definition lives in [`kubescape.app.yaml`](../kubescape.app.yaml).
- Kubescape's recommended UI path is Headlamp with the Kubescape plugin.
- This repo installs that UI separately via [`../headlamp.app.yaml`](../headlamp.app.yaml).
- Grafana is not the primary Kubescape UI, though Prometheus metrics can still
  be integrated separately.
