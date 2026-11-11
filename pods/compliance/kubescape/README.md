## Kubescape

This cluster installs Kubescape through the upstream `kubescape-operator` Helm
chart via Argo CD.

What it does
- Enables Kubescape's supported operator path instead of the older ad hoc CLI
  CronJob model.
- Turns on continuous scan features so findings are available in-cluster instead
  of only as JSON files on a PVC.
- Enables Kubescape's dedicated Prometheus exporter and its `ServiceMonitor`,
  which are discovered by both Adaetum Prometheus and Rancher Monitoring.

Notes
- The Argo CD app definition lives in [`kubescape.app.yaml`](../kubescape.app.yaml).
- No Kubescape UI is installed. Findings are read from the cluster and from its
  Prometheus metrics; the Adaetum agent is the operator-facing surface.
- Grafana is not the primary Kubescape findings UI; its Prometheus metrics are
  still collected for cluster dashboards and alerts.
