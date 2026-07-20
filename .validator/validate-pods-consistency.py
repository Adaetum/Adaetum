#!/usr/bin/env python3
"""Cross-check rendered pod files so related routing/config values cannot drift."""
from __future__ import annotations

import re
import shutil
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
CONFIG_PATH = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"


def parse_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def load_text(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def expect_equal(failures: list[str], path: Path, label: str, actual: str, expected: str) -> None:
    rel = path.relative_to(REPO_ROOT)
    if actual != expected:
        failures.append(f"{rel}: {label} mismatch (expected {expected!r}, got {actual!r})")


def extract_yaml_value(path: Path, pattern: str, label: str) -> str:
    text = load_text(path)
    match = re.search(pattern, text, flags=re.MULTILINE)
    if not match:
        raise RuntimeError(f"{path.relative_to(REPO_ROOT)}: missing {label}")
    return match.group(1).strip().strip('"').strip("'")


def require_kubectl() -> str:
    kubectl = shutil.which("kubectl")
    if not kubectl:
        raise RuntimeError("kubectl is required to validate rendered Kustomize output")
    return kubectl


def render_kustomize_text(directory: Path) -> str:
    kubectl = require_kubectl()
    result = subprocess.run(
        [kubectl, "kustomize", str(directory), "--load-restrictor=LoadRestrictionsNone"],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        stderr = (result.stderr or result.stdout or "").strip()
        raise RuntimeError(f"{directory.relative_to(REPO_ROOT)}: kubectl kustomize failed: {stderr}")
    return result.stdout


def load_kustomize_resource_text(directory: Path, kind: str, name: str) -> str:
    text = render_kustomize_text(directory)
    for document in re.split(r"(?m)^---\s*$", text):
        if not document.strip():
            continue
        doc_kind = re.search(r"(?m)^\s*kind:\s*(\S+)\s*$", document)
        doc_name = re.search(r"(?m)^\s*name:\s*(\S+)\s*$", document)
        if doc_kind and doc_name and doc_kind.group(1) == kind and doc_name.group(1) == name:
            return document
    raise RuntimeError(f"{directory.relative_to(REPO_ROOT)}: missing rendered {kind}/{name}")


def main() -> int:
    config = parse_env(CONFIG_PATH)
    failures: list[str] = []

    repo_url = f"http://gitea-http.gitea.svc.cluster.local:3000/{config['GITEA_REPO_OWNER']}/{config['GITEA_REPO_NAME']}.git"
    gitea_root = f"http://{config['GITEA_CANONICAL_HOST']}/"

    argocd_paths = [
        REPO_ROOT / "pods" / "argocd" / "bootstrap" / "app-of-apps.yaml",
        REPO_ROOT / "pods" / "argocd" / "bootstrap" / "applicationset.yaml",
        REPO_ROOT / "pods" / "argocd" / "platform" / "pre-openbao" / "openbao.yaml",
        REPO_ROOT / "pods" / "argocd" / "platform" / "post-openbao" / "application.yaml",
        REPO_ROOT / "pods" / "argocd" / "platform" / "post-openbao" / "openbao-config.yaml",
    ]
    for path in argocd_paths:
        if path.name == "applicationset.yaml":
            actual = extract_yaml_value(path, r"(?m)^\s*repoURL:\s*(\S+)\s*$", "generator repoURL")
            expect_equal(failures, path, "generator repoURL", actual, repo_url)
        else:
            actual = extract_yaml_value(path, r"(?m)^\s*repoURL:\s*(\S+)\s*$", "source repoURL")
            expect_equal(failures, path, "source repoURL", actual, repo_url)

    gitea_values = load_text(REPO_ROOT / "pods" / "gitea" / "gitea-values.yaml")
    if f"DOMAIN: {config['GITEA_CANONICAL_HOST']}" not in gitea_values:
        failures.append("pods/gitea/gitea-values.yaml: Gitea DOMAIN does not match GITEA_CANONICAL_HOST")
    if f"ROOT_URL: {gitea_root}" not in gitea_values:
        failures.append("pods/gitea/gitea-values.yaml: Gitea ROOT_URL does not match GITEA_CANONICAL_HOST")

    try:
        authentik_blueprints = render_kustomize_text(REPO_ROOT / "pods" / "authentik" / "blueprints")
    except RuntimeError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    authentik_service_hosts = {
        config[key]
        for key in (
            "ARGOCD_PUBLIC_HOST",
            "ARGOCD_LOCAL_HOST",
            "OPENBAO_PUBLIC_HOST",
            "OPENBAO_LOCAL_HOST",
            "GITEA_PUBLIC_HOST",
            "GITEA_LOCAL_HOST",
            "REGISTRY_PUBLIC_HOST",
            "REGISTRY_LOCAL_HOST",
            "HEADLAMP_PUBLIC_HOST",
            "HEADLAMP_LOCAL_HOST",
            "HOMEPAGE_PUBLIC_HOST",
            "HOMEPAGE_LOCAL_HOST",
            "ALERTMANAGER_PUBLIC_HOST",
            "ALERTMANAGER_LOCAL_HOST",
            "GRAFANA_PUBLIC_HOST",
            "GRAFANA_LOCAL_HOST",
            "PROMETHEUS_PUBLIC_HOST",
            "PROMETHEUS_LOCAL_HOST",
            "AUTHENTIK_PUBLIC_HOST",
        )
    }
    for host in sorted(authentik_service_hosts):
        if host not in authentik_blueprints:
            failures.append(f"pods/authentik/blueprints: missing profile hostname {host}")
    advertised_hosts = set(re.findall(r"https?://([A-Za-z0-9.-]+)", authentik_blueprints))
    unexpected_hosts = sorted(advertised_hosts - authentik_service_hosts)
    if unexpected_hosts:
        failures.append(
            "pods/authentik/blueprints: hostnames outside the platform profile: "
            + ", ".join(unexpected_hosts)
        )
    cookie_domains = set(re.findall(r"cookie_domain:\s+\.([A-Za-z0-9.-]+)", authentik_blueprints))
    expected_cookie_domains = {config["CLUSTER_DOMAIN"], config["CLUSTER_LOCAL_DOMAIN"]}
    if cookie_domains != expected_cookie_domains:
        failures.append(
            "pods/authentik/blueprints: cookie domains do not match the platform profile "
            f"(expected {sorted(expected_cookie_domains)!r}, got {sorted(cookie_domains)!r})"
        )

    try:
        ingress_output = subprocess.run(
            [require_kubectl(), "kustomize", str(REPO_ROOT / "pods" / "ingress"), "--load-restrictor=LoadRestrictionsNone"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            check=False,
        )
    except Exception as exc:  # noqa: BLE001
        print(str(exc), file=sys.stderr)
        return 1
    if ingress_output.returncode != 0:
        stderr = (ingress_output.stderr or ingress_output.stdout or "").strip()
        print(f"pods/ingress: kubectl kustomize failed: {stderr}", file=sys.stderr)
        return 1
    route_text = ingress_output.stdout
    try:
        observability_routing_output = subprocess.run(
            [require_kubectl(), "kustomize", str(REPO_ROOT / "pods" / "ingress" / "observability-routing"), "--load-restrictor=LoadRestrictionsNone"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            check=False,
        )
    except Exception as exc:  # noqa: BLE001
        print(str(exc), file=sys.stderr)
        return 1
    if observability_routing_output.returncode != 0:
        stderr = (observability_routing_output.stderr or observability_routing_output.stdout or "").strip()
        print(f"pods/ingress/observability-routing: kubectl kustomize failed: {stderr}", file=sys.stderr)
        return 1
    route_text += "\n" + observability_routing_output.stdout
    for key in (
        "GITEA_PUBLIC_HOST",
        "GITEA_LOCAL_HOST",
        "REGISTRY_PUBLIC_HOST",
        "REGISTRY_LOCAL_HOST",
        "ARGOCD_PUBLIC_HOST",
        "ARGOCD_LOCAL_HOST",
        "HOMEPAGE_PUBLIC_HOST",
        "HOMEPAGE_LOCAL_HOST",
        "HEADLAMP_PUBLIC_HOST",
        "HEADLAMP_LOCAL_HOST",
        "ALERTMANAGER_PUBLIC_HOST",
        "ALERTMANAGER_LOCAL_HOST",
        "GRAFANA_PUBLIC_HOST",
        "GRAFANA_LOCAL_HOST",
        "PROMETHEUS_PUBLIC_HOST",
        "PROMETHEUS_LOCAL_HOST",
        "AUTHENTIK_PUBLIC_HOST",
        "AUTHENTIK_LOCAL_HOST",
    ):
        if config[key] not in route_text:
            failures.append(f"pods/ingress: missing hostname from {key}")

    try:
        ansible_runner_text = load_kustomize_resource_text(REPO_ROOT / "pods" / "ansible" / "ansible", "Deployment", "ansible-runner")
    except RuntimeError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    image_match = re.search(r'(?m)^\s*image:\s*"?(.*?)"?\s*$', ansible_runner_text)
    tailscale_domain_match = re.search(
        r'(?ms)name:\s*TAILSCALE_DOMAIN\s*\n\s*value:\s*"?(.*?)"?\s*$',
        ansible_runner_text,
    )
    tailscale_cluster_tag_match = re.search(
        r'(?ms)name:\s*TAILSCALE_CLUSTER_TAG\s*\n\s*value:\s*"?(.*?)"?\s*$',
        ansible_runner_text,
    )
    if not image_match or not tailscale_domain_match or not tailscale_cluster_tag_match:
        print("pods/ansible/ansible/ansible-runner-deployment.yaml: failed to parse rendered ansible-runner deployment", file=sys.stderr)
        return 1
    expect_equal(
        failures,
        REPO_ROOT / "pods" / "ansible" / "ansible" / "ansible-runner-deployment.yaml",
        "image",
        image_match.group(1),
        config["ANSIBLE_RUNNER_IMAGE"],
    )
    expect_equal(
        failures,
        REPO_ROOT / "pods" / "ansible" / "ansible" / "ansible-runner-deployment.yaml",
        "TAILSCALE_DOMAIN",
        tailscale_domain_match.group(1),
        config["TAILSCALE_DOMAIN"],
    )
    expect_equal(
        failures,
        REPO_ROOT / "pods" / "ansible" / "ansible" / "ansible-runner-deployment.yaml",
        "TAILSCALE_CLUSTER_TAG",
        tailscale_cluster_tag_match.group(1),
        config["TAILSCALE_CLUSTER_TAG"],
    )

    standalone_runner_path = REPO_ROOT / "ansible" / "ansible-host-config-sync.yaml"
    standalone_runner = load_text(standalone_runner_path)
    standalone_image = extract_yaml_value(
        standalone_runner_path,
        r"(?m)^\s*image:\s*(\S+)\s*$",
        "ansible-host-config-sync image",
    )
    expect_equal(
        failures,
        standalone_runner_path,
        "image",
        standalone_image,
        config["ANSIBLE_RUNNER_IMAGE"],
    )
    for env_name in ("TAILSCALE_DOMAIN", "TAILSCALE_TAILNET"):
        match = re.search(
            rf'(?ms)name:\s*{env_name}\s*\n\s*value:\s*"?(.*?)"?\s*$',
            standalone_runner,
        )
        if not match:
            failures.append(f"ansible/ansible-host-config-sync.yaml: missing {env_name}")
            continue
        expect_equal(
            failures,
            standalone_runner_path,
            env_name,
            match.group(1),
            config["TAILSCALE_DOMAIN"],
        )

    try:
        ext_dns_text = load_kustomize_resource_text(REPO_ROOT / "pods" / "ingress", "Deployment", "external-dns")
    except RuntimeError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    domain_filter_match = re.search(
        r'(?ms)name:\s*EXTERNAL_DNS_DOMAIN_FILTER\s*\n\s*value:\s*"?(.*?)"?\s*$',
        ext_dns_text,
    )
    actual_filter = domain_filter_match.group(1) if domain_filter_match else ""
    expect_equal(
        failures,
        REPO_ROOT / "pods" / "ingress" / "external-dns" / "deployment.yaml",
        "external-dns domain-filter",
        actual_filter,
        config["EXTERNAL_DNS_DOMAIN_FILTER"],
    )

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
