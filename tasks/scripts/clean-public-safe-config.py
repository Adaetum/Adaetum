#!/usr/bin/env python3
"""Restore the repository's public-safe profile and generated tracked outputs.

This supports maintainers who need a safe baseline after local setup work. It
does not read runtime secrets: the profile renderer is the only authority for
every generated file this command writes.
"""
from __future__ import annotations

import argparse
import copy
import importlib.util
import subprocess
import sys
from pathlib import Path

import yaml


REPO_ROOT = Path(__file__).resolve().parents[2]
RENDER_SCRIPT = REPO_ROOT / "tasks" / "scripts" / "render-pods-config.py"
PROFILE_RENDER_SCRIPT = REPO_ROOT / "tasks" / "scripts" / "render-platform-profile.py"
PUBLIC_SAFE_VALIDATOR = REPO_ROOT / ".validator" / "validate-pods-public-safe.py"
DEFAULT_CONFIG = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"
PROFILE = REPO_ROOT / "platform.yaml"

PUBLIC_SAFE_IDENTITY = {
    "domain": "adaetum.invalid",
    "localDomain": "adaetum.local",
    "overlayDomain": "example-tailnet.ts.net",
    "overlayClusterTag": "tag:cluster",
    "repository": {
        "owner": "gitea-admin",
        "name": "cluster",
        "branch": "main",
    },
}
PUBLIC_SAFE_DELIVERY = {
    "bootstrapBaseUrl": "https://bootstrap.adaetum.invalid",
    "r2Bucket": "iso",
}


def public_safe_profile_from_head() -> dict:
    """Return the committed feature policy with public-safe identity fields.

    Private recovery repositories intentionally commit their own profile, so
    restoring ``HEAD`` alone is not a safe upstream handoff. Preserve the
    current public feature policy while replacing every operator identity and
    delivery field before the profile renderer writes dependent outputs.
    """
    result = subprocess.run(
        ["git", "show", "HEAD:platform.yaml"],
        cwd=REPO_ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    profile = yaml.safe_load(result.stdout)
    if not isinstance(profile, dict) or not isinstance(profile.get("spec"), dict):
        raise RuntimeError("HEAD:platform.yaml is not a valid platform profile")
    profile["spec"]["cluster"] = copy.deepcopy(PUBLIC_SAFE_IDENTITY)
    profile["spec"]["delivery"] = copy.deepcopy(PUBLIC_SAFE_DELIVERY)
    return profile


def reset_profile() -> None:
    """Write the normalized public maintainer baseline to ``platform.yaml``."""
    profile = public_safe_profile_from_head()
    PROFILE.write_text(yaml.safe_dump(profile, sort_keys=False), encoding="utf-8")


def validate_public_tree() -> None:
    """Fail cleanup if a known private identifier remains committable."""
    subprocess.run(
        [sys.executable, str(PUBLIC_SAFE_VALIDATOR), "--public-handoff"],
        cwd=REPO_ROOT,
        check=True,
    )


def load_render_module():
    spec = importlib.util.spec_from_file_location("render_pods_config", RENDER_SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load render script: {RENDER_SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_profile_render_module():
    spec = importlib.util.spec_from_file_location("render_platform_profile", PROFILE_RENDER_SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load platform profile renderer: {PROFILE_RENDER_SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def build_profile_config() -> dict[str, str]:
    """Validate first so cleanup cannot render a malformed public baseline."""
    profile_renderer = load_profile_render_module()
    profile = profile_renderer.load_profile(PROFILE)
    profile_renderer.validate_profile(profile)
    return profile_renderer.config_from_profile(profile)


def write_baseline(config_path: Path) -> dict[str, str]:
    """Write the generated config and all manifests coupled to its host values."""
    module = load_render_module()
    config = build_profile_config()
    config_path.write_text(
        "".join(f"{key}={config[key]}\n" for key in module.ENV_KEYS),
        encoding="utf-8",
    )
    failures = module.render_templates(config, check=False)
    failures.extend(module.render_app_configs(config, check=False))
    if failures:
        raise RuntimeError("\n".join(failures))
    return config


def preview_baseline() -> str:
    """Expose the exact non-secret config without changing the worktree."""
    profile_renderer = load_profile_render_module()
    profile = public_safe_profile_from_head()
    profile_renderer.validate_profile(profile)
    module = load_render_module()
    config = profile_renderer.config_from_profile(profile)
    return "\n".join(f"{key}={config[key]}" for key in module.ENV_KEYS) + "\n"


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Reset platform.yaml and render the tracked public-safe manifests."
    )
    parser.add_argument(
        "--config-file",
        default=str(DEFAULT_CONFIG),
        help="Path to the tracked cluster config env file to rewrite.",
    )
    parser.add_argument(
        "--preview",
        action="store_true",
        help="Print the profile-derived cluster config without modifying the repo.",
    )
    args = parser.parse_args(argv[1:])

    try:
        if args.preview:
            sys.stdout.write(preview_baseline())
            return 0
        reset_profile()
        write_baseline(Path(args.config_file))
        validate_public_tree()
    except Exception as exc:  # noqa: BLE001
        print(str(exc), file=sys.stderr)
        return 1

    print("Reset platform.yaml and rendered tracked public-safe outputs.", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
