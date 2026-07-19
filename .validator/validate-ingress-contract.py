#!/usr/bin/env python3
"""Check that ingress rendering stays aligned with profile-derived host values."""
from __future__ import annotations

import subprocess
import sys
import re
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
CLUSTER_CONFIG = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"
INGRESS_APP = REPO_ROOT / "pods" / "ingress" / "ingress-routing.app.yaml"


def parse_env_file(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def require(config: dict[str, str], key: str) -> str:
    value = (config.get(key) or "").strip()
    if not value:
        raise SystemExit(f"{CLUSTER_CONFIG}: missing required key {key}")
    return value


def run_kustomize(path: Path) -> str:
    proc = subprocess.run(
        ["kubectl", "kustomize", str(path)],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        sys.stderr.write(proc.stderr)
        raise SystemExit(proc.returncode)
    return proc.stdout


def assert_contains(haystack: str, needle: str, message: str, failures: list[str]) -> None:
    if needle not in haystack:
        failures.append(message)


def main() -> int:
    config = parse_env_file(CLUSTER_CONFIG)
    rendered = run_kustomize(REPO_ROOT / "pods" / "ingress")
    ingress_app_text = INGRESS_APP.read_text(encoding="utf-8")

    cluster_domain = require(config, "EXTERNAL_DNS_DOMAIN_FILTER")
    expected_hosts = {
        "argocd internal": require(config, "ARGOCD_LOCAL_HOST"),
        "argocd public": require(config, "ARGOCD_PUBLIC_HOST"),
        "authentik internal": require(config, "AUTHENTIK_LOCAL_HOST"),
        "authentik public": require(config, "AUTHENTIK_PUBLIC_HOST"),
        "gitea internal": require(config, "GITEA_LOCAL_HOST"),
        "gitea public": require(config, "GITEA_PUBLIC_HOST"),
        "homepage internal": require(config, "HOMEPAGE_LOCAL_HOST"),
        "homepage public": require(config, "HOMEPAGE_PUBLIC_HOST"),
        "openbao internal": require(config, "OPENBAO_LOCAL_HOST"),
        "openbao public": require(config, "OPENBAO_PUBLIC_HOST"),
        "registry internal": require(config, "REGISTRY_LOCAL_HOST"),
        "registry public": require(config, "REGISTRY_PUBLIC_HOST"),
        "headlamp internal": require(config, "HEADLAMP_LOCAL_HOST"),
        "headlamp public": require(config, "HEADLAMP_PUBLIC_HOST"),
        "alertmanager internal": require(config, "ALERTMANAGER_LOCAL_HOST"),
        "alertmanager public": require(config, "ALERTMANAGER_PUBLIC_HOST"),
        "grafana internal": require(config, "GRAFANA_LOCAL_HOST"),
        "grafana public": require(config, "GRAFANA_PUBLIC_HOST"),
        "prometheus internal": require(config, "PROMETHEUS_LOCAL_HOST"),
        "prometheus public": require(config, "PROMETHEUS_PUBLIC_HOST"),
    }

    failures: list[str] = []

    for bad in ("example.local", "example.services", "example.ts.net"):
        if bad in rendered:
            failures.append(f"rendered ingress kustomize output still contains placeholder {bad}")

    stale_auth_values = (
        ":9000/outpost.goauthentik.io/auth/nginx",
        "auth-snippet: placeholder",
        f"https://{require(config, 'AUTHENTIK_LOCAL_HOST')}/outpost.goauthentik.io/start?rd=$scheme://$http_host$escaped_request_uri",
        f"https://{require(config, 'AUTHENTIK_PUBLIC_HOST')}/outpost.goauthentik.io/start?rd=$scheme://$http_host$escaped_request_uri",
    )
    for bad in stale_auth_values:
        if bad in rendered:
            failures.append(f"rendered ingress kustomize output still contains stale Authentik annotation value: {bad}")

    # external-dns reads the rendered domain from an environment variable so
    # its command remains stable across profile changes. Kubernetes expands
    # that variable in args at container start; verify both halves of the
    # contract rather than requiring the domain to be hard-coded in the arg.
    if not re.search(
        rf"(?ms)name:\s*EXTERNAL_DNS_DOMAIN_FILTER\s*\n\s*value:\s*{re.escape(cluster_domain)}\s*$",
        rendered,
    ):
        failures.append(
            f"rendered external-dns deployment is missing EXTERNAL_DNS_DOMAIN_FILTER={cluster_domain}"
        )
    assert_contains(
        rendered,
        "--domain-filter=$(EXTERNAL_DNS_DOMAIN_FILTER)",
        "rendered external-dns deployment is missing its domain-filter argument",
        failures,
    )
    assert_contains(
        rendered,
        'allow-snippet-annotations: "true"',
        "rendered nginx controller config is missing allow-snippet-annotations=true",
        failures,
    )
    assert_contains(
        rendered,
        "annotations-risk-level: Critical",
        "rendered nginx controller config is missing annotations-risk-level=Critical",
        failures,
    )
    assert_contains(
        rendered,
        require(config, "AUTHENTIK_FORWARD_AUTH_URL"),
        "rendered ingress kustomize output is missing AUTHENTIK_FORWARD_AUTH_URL",
        failures,
    )
    assert_contains(
        rendered,
        require(config, "AUTHENTIK_AUTH_SNIPPET"),
        "rendered ingress kustomize output is missing AUTHENTIK_AUTH_SNIPPET",
        failures,
    )
    assert_contains(
        rendered,
        require(config, "AUTHENTIK_LOCAL_AUTH_SIGNIN"),
        "rendered ingress kustomize output is missing AUTHENTIK_LOCAL_AUTH_SIGNIN",
        failures,
    )
    assert_contains(
        rendered,
        require(config, "AUTHENTIK_PUBLIC_AUTH_SIGNIN"),
        "rendered ingress kustomize output is missing AUTHENTIK_PUBLIC_AUTH_SIGNIN",
        failures,
    )

    for label, host in expected_hosts.items():
        assert_contains(
            rendered,
            f"host: {host}",
            f"rendered ingress kustomize output is missing expected {label} host {host}",
            failures,
        )

    for required_text, message in (
        ("RespectIgnoreDifferences=true", "ingress-routing app is missing RespectIgnoreDifferences=true"),
        ('field.cattle.io/publicEndpoints', "ingress-routing app is missing field.cattle.io/publicEndpoints ignore"),
        ("- /status", "ingress-routing app is missing /status ignore for ingress or deployment drift"),
        ("name: external-dns", "ingress-routing app is missing the external-dns ignore block"),
        ("argocd.argoproj.io~1tracking-id", "ingress-routing app is missing the Argo tracking-id ignore for external-dns managed resources"),
        ("kind: ServiceAccount", "ingress-routing app is missing the external-dns ServiceAccount ignore block"),
        ("- /secrets", "ingress-routing app is missing ServiceAccount /secrets ignore"),
        ("name: rke2-ingress-nginx-controller", "ingress-routing app is missing the nginx controller ConfigMap ignore block"),
        ("- /metadata/labels", "ingress-routing app is missing ConfigMap /metadata/labels ignore"),
        ("- /metadata/annotations", "ingress-routing app is missing ConfigMap /metadata/annotations ignore"),
    ):
        assert_contains(ingress_app_text, required_text, message, failures)

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1

    print("Ingress contract check passed: rendered hosts, external-dns filter, nginx config, and Argo diff rules are consistent.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
