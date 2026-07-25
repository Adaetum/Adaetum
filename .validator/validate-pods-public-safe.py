#!/usr/bin/env python3
"""Ensure committable outputs remain safe to publish and free of local identity."""
from __future__ import annotations

import argparse
import hashlib
import os
import re
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
ENV_PATH = REPO_ROOT / ".env"
CONFIG_PATH = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"
LOCAL_OVERRIDE_PATH = REPO_ROOT / ".maintainer-overrides" / "allow-example-placeholders"
GIT_DIR_OVERRIDE_PATH = REPO_ROOT / ".git" / "adaetum-allow-example-placeholders"
# Hashes keep the private identifiers themselves out of the public repository.
# Token-level matching still catches them inside hostnames, URLs, and comments.
FORBIDDEN_TOKEN_SHA256 = {
    "980afcf0c54566554a260a37129bfdf7f98a53e5ece14bc67ad38cfb0490062d",
    "8997d1107e1bcafe27bf6823f1b7012eaea9f3c21d63b13f2bfbe9c589ffaf21",
}
IDENTIFIER_TOKEN = re.compile(rb"[A-Za-z0-9_-]+")
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
    REPO_ROOT / "pods" / "observability" / "ntfy" / "ntfy-cluster-config.yaml",
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
    "NTFY_PUBLIC_HOST",
    "NTFY_LOCAL_HOST",
    "RANCHER_PUBLIC_HOST",
    "RANCHER_LOCAL_HOST",
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


def known_private_identifier_failures() -> list[str]:
    """Scan every tracked or unignored file without printing private values."""
    result = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=REPO_ROOT,
        check=True,
        capture_output=True,
    )
    failures: list[str] = []
    for raw_path in filter(None, result.stdout.split(b"\0")):
        path = REPO_ROOT / os.fsdecode(raw_path)
        if not path.is_file():
            continue
        content = path.read_bytes()
        for match in IDENTIFIER_TOKEN.finditer(content):
            digest = hashlib.sha256(match.group(0).lower()).hexdigest()
            if digest not in FORBIDDEN_TOKEN_SHA256:
                continue
            line = content.count(b"\n", 0, match.start()) + 1
            failures.append(
                f"{path.relative_to(REPO_ROOT)}:{line}: found a known private identifier "
                "(run `task clean` before publication)"
            )
    return failures


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--public-handoff",
        action="store_true",
        help="Reject known private identifiers across every committable file.",
    )
    args = parser.parse_args(argv[1:])

    # Recovery repositories intentionally commit their own profile and rendered
    # manifests. The repository-wide private-identity block belongs only to the
    # explicit upstream handoff performed by task clean.
    failures = known_private_identifier_failures() if args.public_handoff else []
    if maintainer_template_guard_enabled():
        denylist = build_dynamic_denylist()
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
    raise SystemExit(main(sys.argv))
