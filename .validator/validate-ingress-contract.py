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
REALIZATION_PHASES = (
    REPO_ROOT / "ansible" / "ansible-scripts" / "bootstrap" / "Phase-50" / "run-phase50.sh",
    REPO_ROOT / "ansible" / "ansible-scripts" / "bootstrap" / "Phase-60" / "run-phase60.sh",
)


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
        "ntfy internal": require(config, "NTFY_LOCAL_HOST"),
        "ntfy public": require(config, "NTFY_PUBLIC_HOST"),
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

    # The local network is the access boundary for internal routes. Keep
    # Authentik forward-auth on the separate public Ingress objects only.
    local_domain = require(config, "CLUSTER_LOCAL_DOMAIN")
    ingress_documents = re.split(r"(?m)^---\s*$", rendered)
    auth_annotations = (
        "nginx.ingress.kubernetes.io/auth-url:",
        "nginx.ingress.kubernetes.io/auth-snippet:",
        "nginx.ingress.kubernetes.io/auth-signin:",
        "nginx.ingress.kubernetes.io/auth-response-headers:",
    )
    for document in ingress_documents:
        if not re.search(r"(?m)^kind:\s+Ingress\s*$", document):
            continue
        hosts = re.findall(r"(?m)^\s*-?\s*host:\s+([^\s]+)\s*$", document)
        if any(host == local_domain or host.endswith(f".{local_domain}") for host in hosts):
            present = [annotation for annotation in auth_annotations if annotation in document]
            if present:
                name_match = re.search(r"(?m)^\s*name:\s+([^\s]+)\s*$", document)
                name = name_match.group(1) if name_match else "<unknown>"
                failures.append(
                    f"rendered internal Ingress {name} still contains Authentik forward-auth annotations"
                )

    protected_public_ingresses = {
        "alertmanager-ui-public",
        "argocd-ui-public",
        "gitea-ui-public",
        "grafana-ui-public",
        "headlamp-ui-public",
        "homepage-ui-public",
        "openbao-ui-public",
        "prometheus-ui-public",
        "registry-ui-public",
    }
    for name in sorted(protected_public_ingresses):
        document = next(
            (
                item
                for item in ingress_documents
                if re.search(rf"(?m)^\s*name:\s+{re.escape(name)}\s*$", item)
            ),
            "",
        )
        if not document:
            failures.append(f"rendered ingress output is missing protected public Ingress {name}")
            continue
        missing = [annotation for annotation in auth_annotations if annotation not in document]
        if missing:
            failures.append(f"rendered public Ingress {name} is missing Authentik forward-auth annotations")

    # ntfy uses the same API for its web UI, PWA, mobile clients, and
    # publishers. A browser-oriented forward-auth redirect corrupts those API
    # responses, so native deny-all/token authentication owns both routes.
    ntfy_public = next(
        (
            item
            for item in ingress_documents
            if re.search(r"(?m)^\s*name:\s+ntfy-api-public\s*$", item)
        ),
        "",
    )
    if not ntfy_public:
        failures.append("rendered ingress output is missing native-auth public Ingress ntfy-api-public")
    else:
        present = [annotation for annotation in auth_annotations if annotation in ntfy_public]
        if present:
            failures.append("rendered public Ingress ntfy-api-public contains incompatible Authentik forward-auth annotations")

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

    # pods/ingress owns the split internal/public routes. Bootstrap may apply
    # the early-safe internal manifest, but must not recreate the retired
    # combined resources or mutate them under a second name.
    for phase_path in REALIZATION_PHASES:
        phase_text = phase_path.read_text(encoding="utf-8")
        assert_contains(
            phase_text,
            'pods/ingress/nginx-routing/argocd-ingress.yaml',
            f"{phase_path.name} does not apply the intended Argo CD ingress manifest",
            failures,
        )
        for legacy_contract in (
            "name: gitea-ui\n",
            "name: argocd-ui\n",
            "annotate ingress argocd-ui",
            "get ingress argocd-ui ",
        ):
            if legacy_contract in phase_text:
                failures.append(
                    f"{phase_path.name} still owns retired combined ingress contract "
                    f"{legacy_contract.strip()}"
                )

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1

    print("Ingress contract check passed: rendered hosts, external-dns filter, nginx config, and Argo diff rules are consistent.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
