#!/usr/bin/env python3
"""Validate the generated public cluster-config.env contract and placeholder rules."""
from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
CONFIG_PATH = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"
LOCAL_OVERRIDE_PATH = REPO_ROOT / ".maintainer-overrides" / "allow-example-placeholders"
GIT_DIR_OVERRIDE_PATH = REPO_ROOT / ".git" / "adaetum-allow-example-placeholders"

REQUIRED_KEYS = (
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

HOST_KEYS = {
    "GITEA_PUBLIC_HOST",
    "GITEA_LOCAL_HOST",
    "GITEA_CANONICAL_HOST",
    "ARGOCD_PUBLIC_HOST",
    "ARGOCD_LOCAL_HOST",
    "OPENBAO_PUBLIC_HOST",
    "OPENBAO_LOCAL_HOST",
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
}

DOMAIN_KEYS = {"CLUSTER_DOMAIN", "CLUSTER_LOCAL_DOMAIN", "EXTERNAL_DNS_DOMAIN_FILTER", "TAILSCALE_DOMAIN"}
URL_KEYS = {
    "AUTHENTIK_FORWARD_AUTH_URL",
    "AUTHENTIK_LOCAL_AUTH_SIGNIN",
    "AUTHENTIK_PUBLIC_AUTH_SIGNIN",
}

HOST_RE = re.compile(r"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$")
URL_RE = re.compile(r"^https?://[^\s]+$")
REPO_RE = re.compile(r"^[A-Za-z0-9_.-]+$")
IMAGE_NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*(?::[0-9]+)?(?:/[A-Za-z0-9][A-Za-z0-9._-]*)+$")
IMAGE_TAG_RE = re.compile(r"^[A-Za-z0-9._-]+$")


def allow_example_placeholders() -> bool:
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


def parse_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            raise ValueError(f"{path}: invalid env line: {raw_line}")
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def validate_values(values: dict[str, str]) -> list[str]:
    failures: list[str] = []
    unknown = sorted(set(values) - set(REQUIRED_KEYS))
    missing = [key for key in REQUIRED_KEYS if not values.get(key)]

    for key in missing:
        failures.append(f"{CONFIG_PATH.relative_to(REPO_ROOT)}: missing required key {key}")
    for key in unknown:
        failures.append(f"{CONFIG_PATH.relative_to(REPO_ROOT)}: unknown key {key}")

    for key in HOST_KEYS | DOMAIN_KEYS:
        value = values.get(key, "")
        if value and not HOST_RE.fullmatch(value):
            failures.append(f"{CONFIG_PATH.relative_to(REPO_ROOT)}: invalid hostname/domain for {key}: {value}")
        if value and "example." in value:
            failures.append(f"{CONFIG_PATH.relative_to(REPO_ROOT)}: unresolved example placeholder for {key}: {value}")

    for key in URL_KEYS:
        value = values.get(key, "")
        if value and not URL_RE.fullmatch(value):
            failures.append(f"{CONFIG_PATH.relative_to(REPO_ROOT)}: invalid URL for {key}: {value}")
        if value and "example." in value:
            failures.append(f"{CONFIG_PATH.relative_to(REPO_ROOT)}: unresolved example placeholder for {key}: {value}")

    for key in ("GITEA_REPO_OWNER", "GITEA_REPO_NAME"):
        value = values.get(key, "")
        if value and not REPO_RE.fullmatch(value):
            failures.append(f"{CONFIG_PATH.relative_to(REPO_ROOT)}: invalid repo token for {key}: {value}")

    tag = values.get("TAILSCALE_CLUSTER_TAG", "")
    if tag and not tag.startswith("tag:"):
        failures.append(f"{CONFIG_PATH.relative_to(REPO_ROOT)}: TAILSCALE_CLUSTER_TAG must start with 'tag:'")

    image = values.get("ANSIBLE_RUNNER_IMAGE", "")
    if image and not is_valid_image_reference(image):
        failures.append(f"{CONFIG_PATH.relative_to(REPO_ROOT)}: invalid container image reference: {image}")

    return failures


def is_valid_image_reference(image: str) -> bool:
    if not image or any(char.isspace() for char in image):
        return False
    last_slash = image.rfind("/")
    last_colon = image.rfind(":")
    if last_colon <= last_slash or last_colon == len(image) - 1:
        return False
    name = image[:last_colon]
    tag = image[last_colon + 1 :]
    return bool(IMAGE_NAME_RE.fullmatch(name) and IMAGE_TAG_RE.fullmatch(tag))


def main() -> int:
    if allow_example_placeholders():
        return 0

    try:
        values = parse_env(CONFIG_PATH)
    except Exception as exc:  # noqa: BLE001
        print(str(exc), file=sys.stderr)
        return 1

    failures = validate_values(values)
    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
