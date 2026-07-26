#!/usr/bin/env python3
"""Protect metrics discovery and the Alertmanager -> Apprise -> ntfy contract."""
from __future__ import annotations

import re
import sys
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]


def helm_values(relative_path: str) -> dict:
    app = yaml.safe_load((ROOT / relative_path).read_text(encoding="utf-8"))
    return yaml.safe_load(app["source_helm_values"])


def nested(mapping: dict, *path: str):
    value = mapping
    for key in path:
        if not isinstance(value, dict) or key not in value:
            return None
        value = value[key]
    return value


def main() -> int:
    failures: list[str] = []

    prometheus = helm_values("pods/observability/prometheus.app.yaml")["prometheus"]["prometheusSpec"]
    for kind in ("serviceMonitor", "podMonitor", "rule"):
        if prometheus.get(f"{kind}SelectorNilUsesHelmValues") is not False:
            failures.append(f"Prometheus must disable the Helm-label fallback for {kind} discovery")
        if prometheus.get(f"{kind}Selector") != {}:
            failures.append(f"Prometheus must select all {kind} resources")
        if prometheus.get(f"{kind}NamespaceSelector") != {}:
            failures.append(f"Prometheus must discover {kind} resources across namespaces")

    alertmanager = helm_values("pods/observability/alertmanager.app.yaml")
    if nested(alertmanager, "serviceMonitor", "additionalLabels", "release") != "rancher-monitoring":
        failures.append("Alertmanager's ServiceMonitor is not discoverable by Rancher monitoring")
    config = alertmanager["config"]
    routes = config["route"].get("routes", [])
    actionable = next((route for route in routes if route.get("receiver") == "apprise"), {})
    if 'severity=~"warning|critical"' not in actionable.get("matchers", []):
        failures.append("warning and critical alerts must route to Apprise")
    apprise = next((receiver for receiver in config["receivers"] if receiver.get("name") == "apprise"), {})
    webhooks = apprise.get("webhook_configs", [])
    expected_url = "http://apprise.observability.svc.cluster.local:8000/notify/apprise"
    if not webhooks or webhooks[0].get("url") != expected_url or not webhooks[0].get("send_resolved"):
        failures.append("Alertmanager must send firing and resolved alerts to the Apprise endpoint")
    if not config.get("inhibit_rules"):
        failures.append("Alertmanager must inhibit duplicate warning alerts after critical escalation")

    phase40 = (
        ROOT / "ansible/ansible-scripts/bootstrap/Phase-40/run-phase40.sh"
    ).read_text(encoding="utf-8")
    expected_apprise_target = (
        "ntfy://apprise:%s@ntfy.observability.svc.cluster.local:80/adaetum-alerts"
    )
    if expected_apprise_target not in phase40:
        failures.append("OpenBao must seed Apprise with the authenticated ntfy publisher URL")
    ntfy = (ROOT / "pods/observability/ntfy/ntfy.yaml").read_text(encoding="utf-8")
    if "ntfy access --config=/etc/ntfy/server.yml apprise adaetum-alerts write-only" not in ntfy:
        failures.append("ntfy must retain a write-only publisher for the Apprise alert topic")

    chart_monitors = [
        ("pods/observability/grafana.app.yaml", ("serviceMonitor", "labels")),
        ("pods/secrets/external-secrets.app.yaml", ("serviceMonitor", "additionalLabels")),
        ("pods/secrets/reloader.app.yaml", ("reloader", "podMonitor", "labels")),
        ("pods/compliance/kubescape.app.yaml", ("kubescape", "serviceMonitor", "additionalLabels")),
        ("pods/compliance/kubescape.app.yaml", ("nodeAgent", "serviceMonitor", "additionalLabels")),
        ("pods/authentik/authentik.app.yaml", ("server", "metrics", "serviceMonitor", "labels")),
        ("pods/authentik/authentik.app.yaml", ("worker", "metrics", "serviceMonitor", "labels")),
    ]
    for path, value_path in chart_monitors:
        values = helm_values(path)
        monitor = nested(values, *value_path[:-1])
        labels = nested(values, *value_path)
        if not isinstance(monitor, dict) or monitor.get("enabled") is not True:
            failures.append(f"{path} must enable its chart-native monitor")
        if not isinstance(labels, dict) or labels.get("release") != "rancher-monitoring":
            failures.append(f"{path} monitor is not discoverable by Rancher monitoring")

    external_secrets = helm_values("pods/secrets/external-secrets.app.yaml")
    if nested(external_secrets, "serviceMonitor", "renderMode") != "skipIfMissing":
        failures.append(
            "External Secrets must defer ServiceMonitors until the monitoring CRD exists"
        )

    authentik = helm_values("pods/authentik/authentik.app.yaml")
    if nested(authentik, "prometheus", "rules", "enabled") is not True:
        failures.append("Authentik's maintained Prometheus rules must remain enabled")
    if nested(authentik, "prometheus", "rules", "labels", "release") != "rancher-monitoring":
        failures.append("Authentik alert rules must be visible to Rancher monitoring")

    kured = helm_values("pods/operations/kured.app.yaml")
    if nested(kured, "metrics", "create") is not True:
        failures.append("Kured must create its chart-native ServiceMonitor")
    if nested(kured, "metrics", "labels", "release") != "rancher-monitoring":
        failures.append("Kured's ServiceMonitor is not discoverable by Rancher monitoring")

    gitea = yaml.safe_load((ROOT / "pods/gitea/gitea-values.yaml").read_text(encoding="utf-8"))
    if nested(gitea, "gitea", "metrics", "serviceMonitor", "additionalLabels", "release") != "rancher-monitoring":
        failures.append("Gitea metrics must be exposed through a Rancher-discoverable ServiceMonitor")

    monitor_docs = list(
        yaml.safe_load_all(
            (ROOT / "pods/observability/monitoring-integrations/service-monitors.yaml").read_text(
                encoding="utf-8"
            )
        )
    )
    monitor_names = {doc["metadata"]["name"] for doc in monitor_docs}
    expected_monitors = {
        "argocd-application-controller",
        "argocd-applicationset-controller",
        "argocd-notifications-controller",
        "argocd-repo-server",
        "argocd-server",
        "external-dns",
        "cloudflared",
        "kube-vip",
    }
    if monitor_names != expected_monitors:
        failures.append("monitoring integrations must cover: " + ", ".join(sorted(expected_monitors)))
    for doc in monitor_docs:
        if nested(doc, "metadata", "labels", "release") != "rancher-monitoring":
            failures.append(f"{doc['metadata']['name']} is not discoverable by Rancher monitoring")

    healthcheck_defaults = yaml.safe_load(
        (ROOT / "ansible/automation-roles/healthcheck/defaults/main.yml").read_text(encoding="utf-8")
    )
    healthcheck_tasks = (
        ROOT / "ansible/automation-roles/healthcheck/tasks/main.yml"
    ).read_text(encoding="utf-8")
    if "(health_monitor_resources.stdout | from_json)['items']" not in healthcheck_tasks:
        failures.append(
            "healthcheck must access the monitor JSON items key without resolving dict.items"
        )
    required_live_monitors = set(healthcheck_defaults.get("healthcheck_required_monitor_names", []))
    chart_monitor_names = {
        "alertmanager",
        "authentik-server",
        "authentik-worker",
        "external-secrets-cert-controller-metrics",
        "external-secrets-metrics",
        "external-secrets-webhook-metrics",
        "gitea",
        "grafana",
        "kubescape-monitor",
        "kured",
        "reloader-reloader",
        "runtime-monitor",
    }
    if required_live_monitors != expected_monitors | chart_monitor_names:
        failures.append("the live healthcheck monitor inventory is out of sync with desired state")
    for setting in (
        "healthcheck_observability_target_retries",
        "healthcheck_observability_target_delay",
    ):
        value = healthcheck_defaults.get(setting)
        if not isinstance(value, int) or value < 1:
            failures.append(f"{setting} must define a positive bounded convergence value")
    for marker in (
        'retries: "{{ healthcheck_observability_target_retries | int }}"',
        'delay: "{{ healthcheck_observability_target_delay | int }}"',
    ):
        if healthcheck_tasks.count(marker) != 2:
            failures.append(f"both Prometheus target checks must use {marker}")
    for marker in (
        "--selector=release=rancher-monitoring",
        "services/http:prometheus-operated:9090/proxy/api/v1/targets?state=active",
        "services/http:rancher-monitoring-prometheus:9090/proxy/api/v1/targets?state=active",
    ):
        if marker not in healthcheck_tasks:
            failures.append(f"live observability verification is missing {marker}")

    raw_metrics_contracts = {
        "pods/ingress/external-dns/deployment.yaml": ("name: external-dns-metrics", "containerPort: 7979"),
        "pods/cloudflared/cloudflared/deployment.yaml": (
            "name: cloudflared-metrics",
            'args: ["tunnel", "--no-autoupdate", "--metrics", "0.0.0.0:2000", "run"]',
        ),
        "pods/ingress/kube-vip/daemonset.yaml": ("name: metrics", "containerPort: 2112"),
    }
    for path, required in raw_metrics_contracts.items():
        text = (ROOT / path).read_text(encoding="utf-8")
        for marker in required:
            if marker not in text:
                failures.append(f"{path} is missing its native metrics contract: {marker}")

    kubewarden = helm_values("pods/compliance/kubewarden-controller.app.yaml")
    if nested(kubewarden, "telemetry", "metrics") is not False:
        failures.append(
            "Kubewarden telemetry must remain disabled without an owned OpenTelemetry Operator"
        )

    kubescape = helm_values("pods/compliance/kubescape.app.yaml")
    if nested(kubescape, "kubescape", "serviceMonitor", "interval") != "200s":
        failures.append("Kubescape scan metrics must retain their 200s scrape interval")
    if nested(kubescape, "kubescape", "serviceMonitor", "scrapeTimeout") != "150s":
        failures.append("Kubescape scan metrics must retain their 150s scrape timeout")
    if nested(kubescape, "nodeAgent", "config", "prometheusExporter") != "enable":
        failures.append("Kubescape's node-agent monitor requires its Prometheus listener")

    argocd_values = (ROOT / "ansible/automation-roles/argocd-install/templates/argocd-values.yaml.j2").read_text(
        encoding="utf-8"
    )
    for component in ("controller", "server", "repoServer", "applicationSet", "notifications"):
        parts = argocd_values.split(f"{component}:\n", 1)
        if len(parts) != 2:
            failures.append(f"Argo CD {component} values section is missing")
            continue
        section = parts[1]
        section = re.split(r"(?m)^[A-Za-z][A-Za-z0-9_-]*:\n", section, maxsplit=1)[0]
        if "\n  metrics:\n    enabled: true" not in "\n" + section:
            failures.append(f"Argo CD {component} metrics must remain enabled")
    rules = yaml.safe_load(
        (ROOT / "pods/observability/monitoring-integrations/prometheus-rules.yaml").read_text(
            encoding="utf-8"
        )
    )
    alert_names = {
        rule["alert"]
        for group in rules["spec"]["groups"]
        for rule in group.get("rules", [])
        if "alert" in rule
    }
    required_alerts = {"AdaetumAlertmanagerConfigReloadFailed", "AdaetumNotificationDeliveryFailed"}
    if not required_alerts.issubset(alert_names):
        failures.append("notification-pipeline failure alerts are incomplete")
    for alert in (
        "ArgoCDApplicationDataMissing",
        "ArgoCDApplicationNotSynced",
        "ArgoCDApplicationUnhealthy",
    ):
        if alert not in alert_names:
            failures.append(f"Argo CD alert rule is missing: {alert}")
    if nested(rules, "metadata", "labels", "release") != "rancher-monitoring":
        failures.append("Adaetum alert rules must be visible to Rancher monitoring")

    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print("metrics discovery and Alertmanager -> Apprise -> ntfy contracts passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
