#!/usr/bin/env python3
"""Restore the repository's public-safe profile and generated tracked outputs.

This supports maintainers who need a safe baseline after local setup work. It
does not read runtime secrets: the profile renderer is the only authority for
every generated file this command writes.
"""
from __future__ import annotations

import argparse
import importlib.util
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
RENDER_SCRIPT = REPO_ROOT / "tasks" / "scripts" / "render-pods-config.py"
PROFILE_RENDER_SCRIPT = REPO_ROOT / "tasks" / "scripts" / "render-platform-profile.py"
DEFAULT_CONFIG = REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env"
PROFILE = REPO_ROOT / "platform.yaml"


def reset_profile() -> None:
    """Restore the committed profile without introducing a second profile source."""
    result = subprocess.run(
        ["git", "show", "HEAD:platform.yaml"],
        cwd=REPO_ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    PROFILE.write_text(result.stdout, encoding="utf-8")


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
    module = load_render_module()
    config = build_profile_config()
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
    except Exception as exc:  # noqa: BLE001
        print(str(exc), file=sys.stderr)
        return 1

    print("Reset platform.yaml and rendered tracked public-safe outputs.", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
