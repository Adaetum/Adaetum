#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import tempfile
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
RENDER_SCRIPT = REPO_ROOT / "tasks" / "scripts" / "render-pods-config.py"

SAMPLE_ENV = """\
CLUSTER_DOMAIN=lab.example.net
CLUSTER_LOCAL_DOMAIN=lab.example.local
GITEA_SEED_TARGET_OWNER=forkowner
GITEA_SEED_TARGET_REPO=cluster
ARGOCD_GITHUB_REPO_URL=https://github.com/forkowner/cluster.git
REGISTRY_PUBLIC_DOMAIN=registry.lab.example.net
ANSIBLE_RUNNER_IMAGE=registry.lab.example.net/forkowner/ansible-runner:v1
TAILSCALE_DOMAIN=lab.ts.net
TAILSCALE_CLUSTER_TAG=tag:lab
"""


def load_render_module():
    spec = importlib.util.spec_from_file_location("render_pods_config", RENDER_SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load render script: {RENDER_SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main() -> int:
    module = load_render_module()
    with tempfile.TemporaryDirectory() as tmpdir:
        tmp_path = Path(tmpdir)
        env_path = tmp_path / "sample.env"
        config_path = tmp_path / "cluster-config.env"
        env_path.write_text(SAMPLE_ENV, encoding="utf-8")
        module.sync_config_from_env(env_path, config_path)
        values = module.parse_env_file(config_path)

    failures: list[str] = []
    for key in module.ENV_KEYS:
        if not values.get(key):
            failures.append(f"roundtrip config missing required key: {key}")

    if values.get("GITEA_REPO_OWNER") != "forkowner":
        failures.append("roundtrip config did not preserve GITEA_SEED_TARGET_OWNER")
    if values.get("GITEA_REPO_NAME") != "cluster":
        failures.append("roundtrip config did not preserve GITEA_SEED_TARGET_REPO")
    if values.get("TAILSCALE_CLUSTER_TAG") != "tag:lab":
        failures.append("roundtrip config did not preserve normalized TAILSCALE_CLUSTER_TAG")
    if values.get("GITEA_PUBLIC_HOST") != "gitea.lab.example.net":
        failures.append("roundtrip config did not derive GITEA_PUBLIC_HOST from CLUSTER_DOMAIN")
    if values.get("ARGOCD_LOCAL_HOST") != "argocd.lab.example.local":
        failures.append("roundtrip config did not derive ARGOCD_LOCAL_HOST from CLUSTER_LOCAL_DOMAIN")

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
