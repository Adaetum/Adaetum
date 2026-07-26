# Monitoring integrations

This directory owns monitor resources for workloads that expose native metrics
but whose deployment chart or raw manifest does not create a monitor. Helm apps
keep their chart-native ServiceMonitor or PodMonitor configuration beside the
app instead. Argo CD is the intentional exception: its Helm release precedes
the Prometheus Operator CRDs during bootstrap, so this post-CRD app owns Argo's
monitors and rules while the Argo chart owns only the metrics Services.

Adaetum Prometheus discovers monitors and rules across all namespaces. Monitor
resources also carry `release: rancher-monitoring`, allowing Rancher's cluster
Prometheus to discover the same scrape targets without creating a second set of
resources. Product-maintained and Adaetum alert rules carry the same label so
they are visible in Rancher's alert views. Adaetum's standalone Alertmanager
remains the sole notification owner and routes actionable alerts through
Apprise to the authenticated ntfy publisher; configuring Rancher's Alertmanager
with the same receiver would generate duplicate notifications because both
Prometheus instances evaluate the shared rules.

Workloads without a native metrics endpoint, including Apprise, ntfy, Homepage,
and ansible-runner, are covered by kube-state-metrics and the standard
kube-prometheus workload rules. Do not create ServiceMonitors for ordinary HTTP
health endpoints; a monitor must scrape Prometheus-format metrics.

## Coverage ownership

| Workload group | Metrics coverage | Alert coverage |
| --- | --- | --- |
| Prometheus Operator, Prometheus, kube-state-metrics, and node-exporter | kube-prometheus-stack monitors | Upstream Kubernetes, node, storage, and target rules |
| Argo CD controller, API, repo server, ApplicationSet, and notifications | Metrics Services from the bootstrap chart; ServiceMonitors in this app | Adaetum GitOps state rules |
| Authentik server and worker | Chart-native ServiceMonitors | Authentik-maintained recording and alert rules |
| Gitea, Grafana, Alertmanager, External Secrets, Kured, and Kubescape | Chart-native ServiceMonitors | Product metrics plus upstream workload and target rules |
| Reloader | Chart-native PodMonitor | Upstream workload and target rules |
| Kubewarden controller and default PolicyServer | ServiceMonitors in this app | Upstream workload and target rules; policy decisions remain audit data |
| external-dns, cloudflared, and kube-vip | Native metrics ports plus monitors in this app | Upstream workload and target rules |
| Apprise, ntfy, Homepage, Headlamp, ansible-runner, CSI providers, and secret-sync resources | kube-state-metrics; no fake metrics endpoints | Upstream Deployment, StatefulSet, DaemonSet, Job, pod, and PVC rules |

OpenBao's metrics endpoint requires an authenticated token unless explicitly
made public. Adaetum does not weaken that boundary or persist a monitoring token
in Kubernetes; its StatefulSet and storage remain covered by kube-state-metrics
and the standard workload/PVC alerts.
