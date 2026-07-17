#!/usr/bin/env python3
"""Report whether a recovery repository is ready without inspecting secrets."""
from __future__ import annotations

import importlib.util
import re
import shutil
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
PROFILE_PATH = REPO_ROOT / "platform.yaml"
PROFILE_VALIDATOR = REPO_ROOT / "tasks" / "scripts" / "validate-platform-profile.py"
ROCKY_INSTALLER_ISO = re.compile(r"^Rocky-10(?:\.\d+)?-(?:x86_64|aarch64)-(?:minimal|dvd1|dvd)\.iso$")


def load_profile_validator():
    """Reuse the public-contract validator so preflight cannot drift from setup."""
    spec = importlib.util.spec_from_file_location("validate_platform_profile", PROFILE_VALIDATOR)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load platform profile validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def has_command(*names: str) -> bool:
    """Accept equivalent executable names for tools whose package names vary."""
    return any(shutil.which(name) for name in names)


def has_pyyaml() -> bool:
    try:
        import yaml  # noqa: F401
    except ImportError:
        return False
    return True


def main() -> int:
    # This command is intentionally read-only: it tells a repository owner what they
    # must provide without probing provider accounts or asking for credentials.
    blockers: list[str] = []
    notes: list[str] = []

    if not has_command("python3"):
        blockers.append("python3 is not installed")
    elif not has_pyyaml():
        blockers.append("PyYAML is not installed; run python3 -m pip install pyyaml")
    if not has_command("task"):
        blockers.append("Task is not installed")
    if not has_command("git"):
        blockers.append("git is not installed")
    if not has_command("rclone"):
        blockers.append("rclone is not installed")
    if not has_command("7z", "7za", "7zz"):
        blockers.append("a 7-Zip command (7z, 7za, or 7zz) is not installed")

    if has_pyyaml():
        try:
            validator = load_profile_validator()
            profile = validator.load_yaml(PROFILE_PATH)
            errors = validator.validate_profile(profile)
            if errors:
                blockers.extend(f"platform.yaml: {error}" for error in errors)
            else:
                cluster = profile["spec"]["cluster"]
                domain = cluster["domain"]
                bootstrap_url = profile["spec"]["delivery"]["bootstrapBaseUrl"]
                if domain.endswith(".invalid") or ".invalid/" in bootstrap_url:
                    blockers.append("platform.yaml still uses non-routable defaults; replace spec.cluster.domain and spec.delivery.bootstrapBaseUrl")
                if cluster["overlayDomain"] == "example-tailnet.ts.net":
                    blockers.append("platform.yaml still uses the example Tailscale domain; replace spec.cluster.overlayDomain")
        except (OSError, RuntimeError, ValueError) as exc:
            blockers.append(f"cannot validate platform.yaml: {exc}")

    if not any(ROCKY_INSTALLER_ISO.match(path.name) for path in REPO_ROOT.glob("*.iso")):
        blockers.append("no supported Rocky Linux 10 Minimal or DVD installer ISO is present in the repository root")
    if not has_command("uv"):
        notes.append("uv is absent; task initialize can install it automatically")
    if not has_command("gum"):
        notes.append("Gum is absent; task init will attempt installation, while task initialize uses plain prompts")
    if not has_command("docker"):
        notes.append("Docker is absent; use an existing ISO or install Docker for local ISO builds")

    if blockers:
        print("Setup preflight is blocked:", file=sys.stderr)
        for blocker in blockers:
            print(f"- {blocker}", file=sys.stderr)
    else:
        print("Non-secret setup preflight passed.")
    print("Secret credentials are intentionally not checked by this command.")
    for note in notes:
        print(f"Note: {note}")
    return 1 if blockers else 0


if __name__ == "__main__":
    raise SystemExit(main())
