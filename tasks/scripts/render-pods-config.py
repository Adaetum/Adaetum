#!/usr/bin/env python3
from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_CONFIG = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"

TEMPLATE_TARGETS = [
    ("pods/argocd/bootstrap/app-of-apps.yaml.tmpl", "pods/argocd/bootstrap/app-of-apps.yaml"),
    ("pods/argocd/bootstrap/applicationset.yaml.tmpl", "pods/argocd/bootstrap/applicationset.yaml"),
    ("pods/argocd/platform/pre-openbao/openbao.yaml.tmpl", "pods/argocd/platform/pre-openbao/openbao.yaml"),
    ("pods/argocd/platform/post-openbao/application.yaml.tmpl", "pods/argocd/platform/post-openbao/application.yaml"),
    ("pods/argocd/platform/post-openbao/openbao-config.yaml.tmpl", "pods/argocd/platform/post-openbao/openbao-config.yaml"),
    ("pods/gitea/gitea-values.yaml.tmpl", "pods/gitea/gitea-values.yaml"),
    ("pods/ansible/ansible/ansible-runner-deployment.yaml.tmpl", "pods/ansible/ansible/ansible-runner-deployment.yaml"),
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
    ("pods/ingress/observability-routing/alertmanager-ingress.yaml.tmpl", "pods/ingress/observability-routing/alertmanager-ingress.yaml"),
    ("pods/ingress/observability-routing/alertmanager-public-ingress.yaml.tmpl", "pods/ingress/observability-routing/alertmanager-public-ingress.yaml"),
    ("pods/ingress/observability-routing/grafana-ingress.yaml.tmpl", "pods/ingress/observability-routing/grafana-ingress.yaml"),
    ("pods/ingress/observability-routing/grafana-public-ingress.yaml.tmpl", "pods/ingress/observability-routing/grafana-public-ingress.yaml"),
    ("pods/ingress/observability-routing/prometheus-ingress.yaml.tmpl", "pods/ingress/observability-routing/prometheus-ingress.yaml"),
    ("pods/ingress/observability-routing/prometheus-public-ingress.yaml.tmpl", "pods/ingress/observability-routing/prometheus-public-ingress.yaml"),
]

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
            "AUTHENTIK_LEGACY_LOCAL_HOST",
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
            "ARGOCD_LOCAL_HOST",
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
            "REGISTRY_PUBLIC_HOST",
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
    "AUTHENTIK_PUBLIC_HOST",
    "AUTHENTIK_LOCAL_HOST",
    "AUTHENTIK_LEGACY_LOCAL_HOST",
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


def load_env_file(path: Path) -> dict[str, str]:
    if not path.exists():
        return {}
    return parse_env_file(path)


def env_or_existing(env_values: dict[str, str], key: str, fallback: str = "") -> str:
    value = (env_values.get(key) or "").strip()
    return value if value else fallback


def derive_local_domain(cluster_domain: str) -> str:
    parts = [part for part in cluster_domain.split(".") if part]
    if len(parts) <= 1:
        return f"{parts[0] if parts else 'example'}.local"
    parts[-1] = "local"
    return ".".join(parts)


def derive_public_host(prefix: str, domain: str) -> str:
    return f"{prefix}.{domain}".strip(".")


def derive_repo_name(repo_url: str) -> str:
    cleaned = repo_url.strip().rstrip("/")
    if not cleaned:
        return "cluster"
    name = cleaned.split("/")[-1]
    if name.endswith(".git"):
        name = name[:-4]
    return name or "cluster"


def sync_config_from_env(env_path: Path, config_path: Path) -> None:
    env_values = load_env_file(env_path)
    cluster_domain = env_or_existing(env_values, "CLUSTER_DOMAIN", "example.services")
    cluster_local_domain = env_or_existing(env_values, "CLUSTER_LOCAL_DOMAIN", derive_local_domain(cluster_domain))
    repo_owner = env_or_existing(env_values, "GITEA_SEED_TARGET_OWNER", "gitea-admin")
    repo_name = env_or_existing(env_values, "GITEA_SEED_TARGET_REPO", derive_repo_name(env_or_existing(env_values, "ARGOCD_GITHUB_REPO_URL", "")))
    public_domain_filter = env_or_existing(env_values, "REGISTRY_PUBLIC_DOMAIN", f"registry.{cluster_domain}")
    default_ansible_runner_image = f"{public_domain_filter}/{repo_owner}/ansible-runner:latest"
    ansible_runner_image = env_or_existing(env_values, "ANSIBLE_RUNNER_IMAGE", default_ansible_runner_image)
    legacy_runner_images = {
        f"gitea.{cluster_local_domain}/{repo_owner}/ansible-runner:latest",
        f"gitea.{cluster_domain}/{repo_owner}/ansible-runner:latest",
        f"registry.{cluster_local_domain}/{repo_owner}/ansible-runner:latest",
        f"gitea-http.gitea.svc.cluster.local:3000/{repo_owner}/ansible-runner:latest",
    }
    if ansible_runner_image in legacy_runner_images:
        ansible_runner_image = default_ansible_runner_image
    tailscale_domain = env_or_existing(env_values, "TAILSCALE_DOMAIN", "example.ts.net")
    tailscale_cluster_tag = env_or_existing(env_values, "TAILSCALE_CLUSTER_TAG", "tag:cluster")
    values = {
        "CLUSTER_DOMAIN": cluster_domain,
        "CLUSTER_LOCAL_DOMAIN": cluster_local_domain,
        "GITEA_REPO_OWNER": repo_owner,
        "GITEA_REPO_NAME": repo_name or "cluster",
        "GITEA_PUBLIC_HOST": derive_public_host("gitea", cluster_domain),
        "GITEA_LOCAL_HOST": derive_public_host("gitea", cluster_local_domain),
        "GITEA_CANONICAL_HOST": env_or_existing(env_values, "GITEA_CANONICAL_HOST", derive_public_host("gitea", cluster_local_domain)),
        "ARGOCD_PUBLIC_HOST": derive_public_host("argocd", cluster_domain),
        "ARGOCD_LOCAL_HOST": derive_public_host("argocd", cluster_local_domain),
        "OPENBAO_PUBLIC_HOST": derive_public_host("openbao", cluster_domain),
        "OPENBAO_LOCAL_HOST": derive_public_host("openbao", cluster_local_domain),
        "HOMEPAGE_PUBLIC_HOST": derive_public_host("home", cluster_domain),
        "HOMEPAGE_LOCAL_HOST": derive_public_host("home", cluster_local_domain),
        "HOMEPAGE_ALLOWED_HOSTS": ",".join(
            [
                derive_public_host("home", cluster_local_domain),
                derive_public_host("home", cluster_domain),
                "homepage.homepage.svc.cluster.local",
                "homepage.homepage.svc",
                "localhost",
                "127.0.0.1",
            ]
        ),
        "HEADLAMP_PUBLIC_HOST": derive_public_host("headlamp", cluster_domain),
        "HEADLAMP_LOCAL_HOST": derive_public_host("headlamp", cluster_local_domain),
        "ALERTMANAGER_PUBLIC_HOST": derive_public_host("alertmanager", cluster_domain),
        "ALERTMANAGER_LOCAL_HOST": derive_public_host("alertmanager", cluster_local_domain),
        "GRAFANA_PUBLIC_HOST": derive_public_host("grafana", cluster_domain),
        "GRAFANA_LOCAL_HOST": derive_public_host("grafana", cluster_local_domain),
        "PROMETHEUS_PUBLIC_HOST": derive_public_host("prometheus", cluster_domain),
        "PROMETHEUS_LOCAL_HOST": derive_public_host("prometheus", cluster_local_domain),
        "RANCHER_PUBLIC_HOST": env_or_existing(env_values, "RANCHER_PUBLIC_DOMAIN", derive_public_host("rancher", cluster_domain)),
        "AUTHENTIK_PUBLIC_HOST": derive_public_host("authentik", cluster_domain),
        "AUTHENTIK_LOCAL_HOST": derive_public_host("authentik", cluster_local_domain),
        "AUTHENTIK_LEGACY_LOCAL_HOST": "authentik.local",
        "AUTHENTIK_FORWARD_AUTH_URL": (
            "http://authentik-server.authentik.svc.cluster.local:80"
            "/outpost.goauthentik.io/auth/nginx"
        ),
        "AUTHENTIK_LOCAL_AUTH_SIGNIN": (
            "https://$host/outpost.goauthentik.io/start?rd=$scheme://$http_host$escaped_request_uri"
        ),
        "AUTHENTIK_PUBLIC_AUTH_SIGNIN": (
            "https://$host/outpost.goauthentik.io/start?rd=$scheme://$http_host$escaped_request_uri"
        ),
        "AUTHENTIK_AUTH_RESPONSE_HEADERS": (
            "Set-Cookie,X-authentik-username,X-authentik-groups,"
            "X-authentik-entitlements,X-authentik-email,X-authentik-name,X-authentik-uid"
        ),
        "AUTHENTIK_AUTH_SNIPPET": "proxy_set_header X-Forwarded-Host $http_host;",
        "REGISTRY_LOCAL_HOST": derive_public_host("registry", cluster_local_domain),
        "REGISTRY_PUBLIC_HOST": public_domain_filter,
        "ANSIBLE_RUNNER_IMAGE": ansible_runner_image,
        "TAILSCALE_DOMAIN": tailscale_domain,
        "TAILSCALE_CLUSTER_TAG": tailscale_cluster_tag if tailscale_cluster_tag.startswith("tag:") else f"tag:{tailscale_cluster_tag}",
        "EXTERNAL_DNS_DOMAIN_FILTER": cluster_domain,
    }
    content = "\n".join(f"{key}={values[key]}" for key in ENV_KEYS) + "\n"
    config_path.write_text(content, encoding="utf-8")


def render_templates(config: dict[str, str], check: bool) -> list[str]:
    derived = dict(config)
    derived["INTERNAL_GIT_REPO_URL"] = (
        f"http://gitea-http.gitea.svc.cluster.local:3000/"
        f"{config['GITEA_REPO_OWNER']}/{config['GITEA_REPO_NAME']}.git"
    )
    derived["GITEA_ROOT_URL"] = f"http://{config['GITEA_CANONICAL_HOST']}/"
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
        "example.ts.net": config["TAILSCALE_DOMAIN"],
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
    parser = argparse.ArgumentParser(description="Sync public-safe pods config and render derived manifests.")
    parser.add_argument("--env-file", default=".env", help="Source env file for syncing cluster config.")
    parser.add_argument("--config-file", default=str(DEFAULT_CONFIG), help="Pods cluster config env file.")
    parser.add_argument("--sync-from-env", action="store_true", help="Update the cluster config file from .env values.")
    parser.add_argument("--check", action="store_true", help="Validate rendered files are in sync.")
    args = parser.parse_args(argv[1:])

    config_path = Path(args.config_file)
    env_path = Path(args.env_file)

    if args.sync_from_env:
        sync_config_from_env(env_path, config_path)

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
