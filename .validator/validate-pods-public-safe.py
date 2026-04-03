#!/usr/bin/env python3
from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
ENV_PATH = REPO_ROOT / ".env"
CONFIG_PATH = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"
LOCAL_OVERRIDE_PATH = REPO_ROOT / ".maintainer-overrides" / "allow-example-placeholders"
GIT_DIR_OVERRIDE_PATH = REPO_ROOT / ".git" / "adaetum-allow-example-placeholders"
CHECK_PATHS = [
    CONFIG_PATH,
    REPO_ROOT / "pods" / "argocd" / "bootstrap" / "app-of-apps.yaml",
    REPO_ROOT / "pods" / "argocd" / "bootstrap" / "applicationset.yaml",
    REPO_ROOT / "pods" / "argocd" / "platform" / "pre-openbao" / "openbao.yaml",
    REPO_ROOT / "pods" / "argocd" / "platform" / "post-openbao" / "application.yaml",
    REPO_ROOT / "pods" / "argocd" / "platform" / "post-openbao" / "openbao-config.yaml",
    REPO_ROOT / "pods" / "ansible" / "ansible" / "ansible-cluster-config.yaml",
    REPO_ROOT / "pods" / "gitea" / "gitea.app.yaml",
    REPO_ROOT / "pods" / "ingress" / "ingress-cluster-config.yaml",
    REPO_ROOT / "pods" / "ingress" / "observability-routing" / "observability-routing-cluster-config.yaml",
    REPO_ROOT / "pods" / "portal" / "homepage" / "homepage-cluster-config.yaml",
]

ENV_KEYS = (
    "CLUSTER_DOMAIN",
    "CLUSTER_LOCAL_DOMAIN",
    "TAILSCALE_DOMAIN",
    "REGISTRY_PUBLIC_DOMAIN",
    "RANCHER_PUBLIC_DOMAIN",
    "GITEA_CANONICAL_HOST",
)

CONFIG_KEYS = (
    "GITEA_PUBLIC_HOST",
    "GITEA_LOCAL_HOST",
    "GITEA_CANONICAL_HOST",
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
    "RANCHER_PUBLIC_HOST",
    "AUTHENTIK_PUBLIC_HOST",
    "AUTHENTIK_LOCAL_HOST",
    "REGISTRY_LOCAL_HOST",
    "REGISTRY_PUBLIC_HOST",
    "TAILSCALE_DOMAIN",
)


def parse_env(path: Path) -> dict[str, str]:
    if not path.exists():
        return {}
    values: dict[str, str] = {}
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def build_dynamic_denylist() -> tuple[str, ...]:
    tokens: list[str] = []
    env_values = parse_env(ENV_PATH)
    config_values = parse_env(CONFIG_PATH)

    for key in ENV_KEYS:
        value = env_values.get(key, "").strip()
        if value:
            tokens.append(value)
    for key in CONFIG_KEYS:
        value = config_values.get(key, "").strip()
        if value:
            tokens.append(value)

    # Keep safe defaults out of the denylist so a cleaned repo does not fail the guard.
    safe_prefixes = ("gitea-admin", "cluster", "tag:")
    denylist = {
        token
        for token in tokens
        if token
        and not is_safe_placeholder_token(token, safe_prefixes)
        and token not in {"authentik.local"}
    }
    return tuple(sorted(denylist))


def is_safe_placeholder_token(token: str, safe_prefixes: tuple[str, ...]) -> bool:
    if token.startswith(safe_prefixes):
        return True
    if "example." in token:
        return True
    return False


def maintainer_template_guard_enabled() -> bool:
    if os.environ.get("MAINTAINER_TEMPLATE_GUARD", "").strip().lower() in {"1", "true", "yes", "on"}:
        return True
    if os.environ.get("ALLOW_EXAMPLE_PLACEHOLDERS", "").strip().lower() in {"1", "true", "yes", "on"}:
        return True
    if LOCAL_OVERRIDE_PATH.exists():
        return True
    if GIT_DIR_OVERRIDE_PATH.exists():
        return True
    result = subprocess.run(
        ["git", "config", "--bool", "--get", "adaetum.allowExamplePlaceholders"],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode == 0 and result.stdout.strip().lower() == "true":
        return True
    return False


def main() -> int:
    if not maintainer_template_guard_enabled():
        return 0

    denylist = build_dynamic_denylist()
    if not denylist:
        return 0

    failures: list[str] = []
    for path in CHECK_PATHS:
        text = path.read_text(encoding="utf-8")
        for token in denylist:
            if token in text:
                failures.append(
                    f"{path.relative_to(REPO_ROOT)}: found maintainer-specific value: {token} "
                    f"(run `task clean` to reset tracked public-safe config)"
                )

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
