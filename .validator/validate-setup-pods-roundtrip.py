#!/usr/bin/env python3
"""Validate the platform-profile-to-pods rendering contract."""
from __future__ import annotations

import importlib.util
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
PROFILE_RENDER_SCRIPT = REPO_ROOT / "tasks" / "scripts" / "render-platform-profile.py"
PROFILE_PATH = REPO_ROOT / "platform.yaml"


def load_profile_renderer():
    spec = importlib.util.spec_from_file_location("render_platform_profile", PROFILE_RENDER_SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load platform renderer: {PROFILE_RENDER_SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main() -> int:
    renderer = load_profile_renderer()
    profile = renderer.load_profile(PROFILE_PATH)
    renderer.validate_profile(profile)
    values = renderer.config_from_profile(profile)
    pod_renderer = renderer.load_pods_renderer()

    failures: list[str] = []
    for key in pod_renderer.ENV_KEYS:
        if not values.get(key):
            failures.append(f"profile render is missing required key: {key}")

    cluster = profile["spec"]["cluster"]
    if values.get("GITEA_REPO_OWNER") != cluster["repository"]["owner"]:
        failures.append("profile render did not preserve spec.cluster.repository.owner")
    if values.get("GITEA_REPO_NAME") != cluster["repository"]["name"]:
        failures.append("profile render did not preserve spec.cluster.repository.name")
    if values.get("GITOPS_REPO_BRANCH") != cluster["repository"]["branch"]:
        failures.append("profile render did not preserve spec.cluster.repository.branch")
    if values.get("TAILSCALE_CLUSTER_TAG") != cluster["overlayClusterTag"]:
        failures.append("profile render did not preserve spec.cluster.overlayClusterTag")
    if values.get("GITEA_PUBLIC_HOST") != f"gitea.{cluster['domain']}":
        failures.append("profile render did not derive GITEA_PUBLIC_HOST from spec.cluster.domain")
    if values.get("ARGOCD_LOCAL_HOST") != f"argocd.{cluster['localDomain']}":
        failures.append("profile render did not derive ARGOCD_LOCAL_HOST from spec.cluster.localDomain")

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
