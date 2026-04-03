#!/usr/bin/env python3
from __future__ import annotations

import argparse
import importlib.util
import sys
import tempfile
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
RENDER_SCRIPT = REPO_ROOT / "tasks" / "scripts" / "render-pods-config.py"
DEFAULT_CONFIG = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"

BASELINE_ENV = """\
CLUSTER_DOMAIN=example.services
CLUSTER_LOCAL_DOMAIN=example.local
GITEA_SEED_TARGET_OWNER=gitea-admin
GITEA_SEED_TARGET_REPO=cluster
REGISTRY_PUBLIC_DOMAIN=registry.example.services
RANCHER_PUBLIC_DOMAIN=rancher.example.services
TAILSCALE_DOMAIN=example.ts.net
TAILSCALE_CLUSTER_TAG=tag:cluster
"""


def load_render_module():
    spec = importlib.util.spec_from_file_location("render_pods_config", RENDER_SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load render script: {RENDER_SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def build_baseline_config(config_path: Path) -> dict[str, str]:
    module = load_render_module()
    with tempfile.TemporaryDirectory() as tmpdir:
        env_path = Path(tmpdir) / "baseline.env"
        env_path.write_text(BASELINE_ENV, encoding="utf-8")
        module.sync_config_from_env(env_path, config_path)
    return module.parse_env_file(config_path)


def write_baseline(config_path: Path) -> dict[str, str]:
    module = load_render_module()
    config = build_baseline_config(config_path)
    failures = module.render_templates(config, check=False)
    failures.extend(module.render_app_configs(config, check=False))
    if failures:
        raise RuntimeError("\n".join(failures))
    return config


def preview_baseline() -> str:
    module = load_render_module()
    with tempfile.TemporaryDirectory() as tmpdir:
        config_path = Path(tmpdir) / "cluster-config.env"
        config = build_baseline_config(config_path)
    return "\n".join(f"{key}={config[key]}" for key in module.ENV_KEYS) + "\n"


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Reset tracked public-safe pods config and rendered manifests to safe baseline placeholder values."
    )
    parser.add_argument(
        "--config-file",
        default=str(DEFAULT_CONFIG),
        help="Path to the tracked cluster config env file to rewrite.",
    )
    parser.add_argument(
        "--preview",
        action="store_true",
        help="Print the baseline cluster config without modifying the repo.",
    )
    args = parser.parse_args(argv[1:])

    try:
        if args.preview:
            sys.stdout.write(preview_baseline())
            return 0
        write_baseline(Path(args.config_file))
    except Exception as exc:  # noqa: BLE001
        print(str(exc), file=sys.stderr)
        return 1

    print("Reset tracked public-safe pods config to baseline placeholders.", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
