#!/usr/bin/env python3
"""Render profile-derived public configuration into pod manifests and ConfigMaps.

This is an internal renderer: `render-platform-profile.py` validates the public
contract first, then calls this file with a flat generated configuration. The
templates remain readable Kubernetes source while committed non-template files
are deterministic outputs used by the bootstrap bundle.
"""
from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_CONFIG = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"

# Source-template to generated-output pairs. Keep this inventory explicit so a
# new public hostname cannot become an untracked hand-edited manifest.
TEMPLATE_TARGETS = [
    ("pods/argocd/bootstrap/app-of-apps.yaml.tmpl", "pods/argocd/bootstrap/app-of-apps.yaml"),
    ("pods/argocd/bootstrap/applicationset.yaml.tmpl", "pods/argocd/bootstrap/applicationset.yaml"),
    ("pods/argocd/platform/pre-openbao/openbao.yaml.tmpl", "pods/argocd/platform/pre-openbao/openbao.yaml"),
    ("pods/argocd/platform/post-openbao/application.yaml.tmpl", "pods/argocd/platform/post-openbao/application.yaml"),
    ("pods/argocd/platform/post-openbao/openbao-config.yaml.tmpl", "pods/argocd/platform/post-openbao/openbao-config.yaml"),
    ("pods/gitea/gitea-values.yaml.tmpl", "pods/gitea/gitea-values.yaml"),
    ("pods/authentik/blueprints/authentik-blueprints-configmap.yaml.tmpl", "pods/authentik/blueprints/authentik-blueprints-configmap.yaml"),
    ("pods/ansible/ansible/ansible-runner-deployment.yaml.tmpl", "pods/ansible/ansible/ansible-runner-deployment.yaml"),
    ("ansible/ansible-host-config-sync.yaml.tmpl", "ansible/ansible-host-config-sync.yaml"),
    ("pods/ingress/external-dns/deployment.yaml.tmpl", "pods/ingress/external-dns/deployment.yaml"),
    ("pods/ingress/ingress-routing.app.yaml.tmpl", "pods/ingress/ingress-routing.app.yaml"),
    ("pods/ingress/nginx-routing/argocd-ingress.yaml.tmpl", "pods/ingress/nginx-routing/argocd-ingress.yaml"),
    ("pods/ingress/nginx-routing/argocd-public-ingress.yaml.tmpl", "pods/ingress/nginx-routing/argocd-public-ingress.yaml"),
    ("pods/ingress/nginx-routing/authentik-outpost-hosts-ingress.yaml.tmpl", "pods/ingress/nginx-routing/authentik-outpost-hosts-ingress.yaml"),
    ("pods/ingress/nginx-routing/authentik-ingress.yaml.tmpl", "pods/ingress/nginx-routing/authentik-ingress.yaml"),
    ("pods/ingress/nginx-routing/gitea-ingress.yaml.tmpl", "pods/ingress/nginx-routing/gitea-ingress.yaml"),
    ("pods/ingress/nginx-routing/gitea-public-ingress.yaml.tmpl", "pods/ingress/nginx-routing/gitea-public-ingress.yaml"),
    ("pods/ingress/nginx-routing/headlamp-ingress.yaml.tmpl", "pods/ingress/nginx-routing/headlamp-ingress.yaml"),
    ("pods/ingress/nginx-routing/headlamp-public-ingress.yaml.tmpl", "pods/ingress/nginx-routing/headlamp-public-ingress.yaml"),
    ("pods/ingress/nginx-routing/homepage-ingress.yaml.tmpl", "pods/ingress/nginx-routing/homepage-ingress.yaml"),
    ("pods/ingress/nginx-routing/homepage-public-ingress.yaml.tmpl", "pods/ingress/nginx-routing/homepage-public-ingress.yaml"),
    ("pods/ingress/nginx-routing/openbao-ingress.yaml.tmpl", "pods/ingress/nginx-routing/openbao-ingress.yaml"),
    ("pods/ingress/nginx-routing/openbao-public-ingress.yaml.tmpl", "pods/ingress/nginx-routing/openbao-public-ingress.yaml"),
    ("pods/ingress/nginx-routing/registry-ingress.yaml.tmpl", "pods/ingress/nginx-routing/registry-ingress.yaml"),
    ("pods/ingress/nginx-routing/registry-public-ingress.yaml.tmpl", "pods/ingress/nginx-routing/registry-public-ingress.yaml"),
    ("pods/ingress/nginx-routing/rancher-ingress.yaml.tmpl", "pods/ingress/nginx-routing/rancher-ingress.yaml"),
    ("pods/ingress/observability-routing/alertmanager-ingress.yaml.tmpl", "pods/ingress/observability-routing/alertmanager-ingress.yaml"),
    ("pods/ingress/observability-routing/alertmanager-public-ingress.yaml.tmpl", "pods/ingress/observability-routing/alertmanager-public-ingress.yaml"),
    ("pods/ingress/observability-routing/grafana-ingress.yaml.tmpl", "pods/ingress/observability-routing/grafana-ingress.yaml"),
    ("pods/ingress/observability-routing/grafana-public-ingress.yaml.tmpl", "pods/ingress/observability-routing/grafana-public-ingress.yaml"),
    ("pods/ingress/observability-routing/prometheus-ingress.yaml.tmpl", "pods/ingress/observability-routing/prometheus-ingress.yaml"),
    ("pods/ingress/observability-routing/prometheus-public-ingress.yaml.tmpl", "pods/ingress/observability-routing/prometheus-public-ingress.yaml"),
    ("pods/operations/kured.app.yaml.tmpl", "pods/operations/kured.app.yaml"),
]

# ConfigMap fields that Kustomize copies into manifests after template render.
# These are listed by consumer to make ownership visible during reviews.
APP_CONFIG_TARGETS = [
    (
        "pods/ansible/ansible/ansible-cluster-config.yaml",
        "ansible-cluster-config",
        ("ANSIBLE_RUNNER_IMAGE", "TAILSCALE_DOMAIN", "TAILSCALE_CLUSTER_TAG"),
    ),
    (
        "pods/ingress/ingress-cluster-config.yaml",
        "ingress-cluster-config",
        (
            "EXTERNAL_DNS_DOMAIN_FILTER",
            "GITEA_LOCAL_HOST",
            "GITEA_PUBLIC_HOST",
            "ARGOCD_LOCAL_HOST",
            "ARGOCD_PUBLIC_HOST",
            "OPENBAO_LOCAL_HOST",
            "OPENBAO_PUBLIC_HOST",
            "AUTHENTIK_LOCAL_HOST",
            "AUTHENTIK_PUBLIC_HOST",
            "AUTHENTIK_FORWARD_AUTH_URL",
            "AUTHENTIK_LOCAL_AUTH_SIGNIN",
            "AUTHENTIK_PUBLIC_AUTH_SIGNIN",
            "AUTHENTIK_AUTH_RESPONSE_HEADERS",
            "AUTHENTIK_AUTH_SNIPPET",
            "HOMEPAGE_LOCAL_HOST",
            "HOMEPAGE_PUBLIC_HOST",
            "HEADLAMP_LOCAL_HOST",
            "HEADLAMP_PUBLIC_HOST",
            "REGISTRY_LOCAL_HOST",
            "REGISTRY_PUBLIC_HOST",
            "RANCHER_LOCAL_HOST",
        ),
    ),
    (
        "pods/ingress/observability-routing/observability-routing-cluster-config.yaml",
        "observability-routing-cluster-config",
        (
            "ALERTMANAGER_LOCAL_HOST",
            "ALERTMANAGER_PUBLIC_HOST",
            "GRAFANA_LOCAL_HOST",
            "GRAFANA_PUBLIC_HOST",
            "PROMETHEUS_LOCAL_HOST",
            "PROMETHEUS_PUBLIC_HOST",
            "AUTHENTIK_FORWARD_AUTH_URL",
            "AUTHENTIK_LOCAL_AUTH_SIGNIN",
            "AUTHENTIK_PUBLIC_AUTH_SIGNIN",
            "AUTHENTIK_AUTH_RESPONSE_HEADERS",
            "AUTHENTIK_AUTH_SNIPPET",
        ),
    ),
    (
        "pods/portal/homepage/homepage-cluster-config.yaml",
        "homepage-cluster-config",
        (
            "GITEA_LOCAL_HOST",
            "GITEA_PUBLIC_HOST",
            "ARGOCD_LOCAL_HOST",
            "ARGOCD_PUBLIC_HOST",
            "HOMEPAGE_LOCAL_HOST",
            "HOMEPAGE_PUBLIC_HOST",
            "HOMEPAGE_ALLOWED_HOSTS",
            "OPENBAO_LOCAL_HOST",
            "OPENBAO_PUBLIC_HOST",
            "AUTHENTIK_LOCAL_HOST",
            "AUTHENTIK_PUBLIC_HOST",
            "HEADLAMP_LOCAL_HOST",
            "HEADLAMP_PUBLIC_HOST",
            "ALERTMANAGER_LOCAL_HOST",
            "ALERTMANAGER_PUBLIC_HOST",
            "GRAFANA_LOCAL_HOST",
            "GRAFANA_PUBLIC_HOST",
            "PROMETHEUS_LOCAL_HOST",
            "PROMETHEUS_PUBLIC_HOST",
            "REGISTRY_LOCAL_HOST",
            "REGISTRY_PUBLIC_HOST",
            "RANCHER_LOCAL_HOST",
            "RANCHER_PUBLIC_HOST",
        ),
    ),
]

ENV_KEYS = (
    "CLUSTER_DOMAIN",
    "CLUSTER_LOCAL_DOMAIN",
    "GITEA_REPO_OWNER",
    "GITEA_REPO_NAME",
    "GITEA_PUBLIC_HOST",
    "GITEA_LOCAL_HOST",
    "GITEA_CANONICAL_HOST",
    "ARGOCD_PUBLIC_HOST",
    "ARGOCD_LOCAL_HOST",
    "OPENBAO_PUBLIC_HOST",
    "OPENBAO_LOCAL_HOST",
    "HOMEPAGE_PUBLIC_HOST",
    "HOMEPAGE_LOCAL_HOST",
    "HOMEPAGE_ALLOWED_HOSTS",
    "HEADLAMP_PUBLIC_HOST",
    "HEADLAMP_LOCAL_HOST",
    "ALERTMANAGER_PUBLIC_HOST",
    "ALERTMANAGER_LOCAL_HOST",
    "GRAFANA_PUBLIC_HOST",
    "GRAFANA_LOCAL_HOST",
    "PROMETHEUS_PUBLIC_HOST",
    "PROMETHEUS_LOCAL_HOST",
    "RANCHER_PUBLIC_HOST",
    "RANCHER_LOCAL_HOST",
    "AUTHENTIK_PUBLIC_HOST",
    "AUTHENTIK_LOCAL_HOST",
    "AUTHENTIK_FORWARD_AUTH_URL",
    "AUTHENTIK_LOCAL_AUTH_SIGNIN",
    "AUTHENTIK_PUBLIC_AUTH_SIGNIN",
    "AUTHENTIK_AUTH_RESPONSE_HEADERS",
    "AUTHENTIK_AUTH_SNIPPET",
    "REGISTRY_LOCAL_HOST",
    "REGISTRY_PUBLIC_HOST",
    "ANSIBLE_RUNNER_IMAGE",
    "TAILSCALE_DOMAIN",
    "TAILSCALE_CLUSTER_TAG",
    "EXTERNAL_DNS_DOMAIN_FILTER",
    "HOST_MAINTENANCE_ENABLED",
    "HOST_MAINTENANCE_REBOOT_ENABLED",
    "HOST_MAINTENANCE_REBOOT_DAYS",
    "HOST_MAINTENANCE_REBOOT_START_TIME",
    "HOST_MAINTENANCE_REBOOT_END_TIME",
    "HOST_MAINTENANCE_TIME_ZONE",
    "HOST_MAINTENANCE_REBOOT_PERIOD",
    "HOST_MAINTENANCE_CONCURRENCY",
    "HOST_MAINTENANCE_DRAIN_TIMEOUT",
    "HOST_MAINTENANCE_DRAIN_GRACE_PERIOD",
    "HOST_MAINTENANCE_FORCE_REBOOT",
    "HOST_MAINTENANCE_LOCK_TTL",
    "HOST_MAINTENANCE_LOCK_RELEASE_DELAY",
    "HOST_MAINTENANCE_REBOOT_DELAY",
    "HOST_MAINTENANCE_BLOCKING_POD_SELECTORS",
    "HOST_MAINTENANCE_PROMETHEUS_URL",
    "HOST_MAINTENANCE_METRICS",
    "HOST_MAINTENANCE_ANNOTATE_NODES",
    "HOST_MAINTENANCE_SENTINEL_COMMAND",
)

# Gitea maps environment variables into app.ini with
# GITEA__SECTION__KEY names. These double underscores are application syntax,
# not Adaetum profile placeholders.
LITERAL_DOUBLE_UNDERSCORE_TOKENS = {"DATABASE"}


def parse_env_file(path: Path) -> dict[str, str]:
    """Read the generated flat config without treating it as user input syntax."""
    values: dict[str, str] = {}
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def render_templates(config: dict[str, str], check: bool) -> list[str]:
    """Render tracked templates, or compare them without mutating the checkout."""
    derived = dict(config)
    derived["INTERNAL_GIT_REPO_URL"] = (
        f"http://gitea-http.gitea.svc.cluster.local:3000/"
        f"{config['GITEA_REPO_OWNER']}/{config['GITEA_REPO_NAME']}.git"
    )
    derived["GITEA_ROOT_URL"] = f"http://{config['GITEA_CANONICAL_HOST']}/"
    # Older templates use readable example hostnames instead of opaque tokens.
    # Replace longest strings first so a service hostname is not partially
    # replaced by the later bare-domain replacement.
    literal_replacements = {
        "authentik.example.local": config["AUTHENTIK_LOCAL_HOST"],
        "authentik.example.services": config["AUTHENTIK_PUBLIC_HOST"],
        "argocd.example.local": config["ARGOCD_LOCAL_HOST"],
        "argocd.example.services": config["ARGOCD_PUBLIC_HOST"],
        "openbao.example.local": config["OPENBAO_LOCAL_HOST"],
        "openbao.example.services": config["OPENBAO_PUBLIC_HOST"],
        "gitea.example.local": config["GITEA_LOCAL_HOST"],
        "gitea.example.services": config["GITEA_PUBLIC_HOST"],
        "registry.example.local": config["REGISTRY_LOCAL_HOST"],
        "registry.example.services": config["REGISTRY_PUBLIC_HOST"],
        "headlamp.example.local": config["HEADLAMP_LOCAL_HOST"],
        "headlamp.example.services": config["HEADLAMP_PUBLIC_HOST"],
        "home.example.local": config["HOMEPAGE_LOCAL_HOST"],
        "home.example.services": config["HOMEPAGE_PUBLIC_HOST"],
        "alertmanager.example.local": config["ALERTMANAGER_LOCAL_HOST"],
        "alertmanager.example.services": config["ALERTMANAGER_PUBLIC_HOST"],
        "grafana.example.local": config["GRAFANA_LOCAL_HOST"],
        "grafana.example.services": config["GRAFANA_PUBLIC_HOST"],
        "prometheus.example.local": config["PROMETHEUS_LOCAL_HOST"],
        "prometheus.example.services": config["PROMETHEUS_PUBLIC_HOST"],
        "rancher.example.services": config["RANCHER_PUBLIC_HOST"],
        "rancher.example.local": config["RANCHER_LOCAL_HOST"],
        "example.ts.net": config["TAILSCALE_DOMAIN"],
        "example.local": config["CLUSTER_LOCAL_DOMAIN"],
        "example.services": config["EXTERNAL_DNS_DOMAIN_FILTER"],
    }
    replacement_items = sorted(literal_replacements.items(), key=lambda item: len(item[0]), reverse=True)
    failures: list[str] = []
    token_pattern = re.compile(r"__([A-Z0-9_]+)__")

    for template_rel, target_rel in TEMPLATE_TARGETS:
        template_path = REPO_ROOT / template_rel
        target_path = REPO_ROOT / target_rel
        template_text = template_path.read_text(encoding="utf-8")

        def replace(match: re.Match[str]) -> str:
            key = match.group(1)
            if key in LITERAL_DOUBLE_UNDERSCORE_TOKENS:
                return match.group(0)
            if key not in derived:
                failures.append(f"{template_rel}: missing token value for {key}")
                return match.group(0)
            return derived[key]

        rendered = token_pattern.sub(replace, template_text)
        for old, new in replacement_items:
            rendered = rendered.replace(old, new)
        if check:
            current = target_path.read_text(encoding="utf-8") if target_path.exists() else ""
            if current != rendered:
                failures.append(f"{target_rel}: rendered output is out of sync with {template_rel}")
        else:
            target_path.write_text(rendered, encoding="utf-8")

    return failures


def yaml_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def render_app_configs(config: dict[str, str], check: bool) -> list[str]:
    failures: list[str] = []
    for target_rel, config_name, keys in APP_CONFIG_TARGETS:
        target_path = REPO_ROOT / target_rel
        lines = [
            "apiVersion: v1",
            "kind: ConfigMap",
            "metadata:",
            f"  name: {config_name}",
            "data:",
        ]
        for key in keys:
            lines.append(f"  {key}: {yaml_quote(config[key])}")
        rendered = "\n".join(lines) + "\n"
        if check:
            current = target_path.read_text(encoding="utf-8") if target_path.exists() else ""
            if current != rendered:
                failures.append(f"{target_rel}: rendered output is out of sync with cluster config")
        else:
            target_path.write_text(rendered, encoding="utf-8")
    return failures


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Render pod manifests from the generated cluster config.")
    parser.add_argument("--config-file", default=str(DEFAULT_CONFIG), help="Pods cluster config env file.")
    parser.add_argument("--check", action="store_true", help="Validate rendered files are in sync.")
    args = parser.parse_args(argv[1:])

    config_path = Path(args.config_file)
    config = parse_env_file(config_path)
    missing = [key for key in ENV_KEYS if not config.get(key)]
    if missing:
        for key in missing:
            print(f"{config_path}: missing required key {key}", file=sys.stderr)
        return 1

    failures = render_templates(config, check=args.check)
    failures.extend(render_app_configs(config, check=args.check))
    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
