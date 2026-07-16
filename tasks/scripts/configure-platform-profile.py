#!/usr/bin/env python3
"""Read, propose, and atomically write Adaetum's public platform profile."""
from __future__ import annotations

import argparse
import importlib.util
import os
import tempfile
from pathlib import Path

import yaml


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_PROFILE = REPO_ROOT / "platform.yaml"
VALIDATOR = REPO_ROOT / "tasks" / "scripts" / "validate-platform-profile.py"


def load_validator():
    spec = importlib.util.spec_from_file_location("platform_validator", VALIDATOR)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load platform profile validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_profile(path: Path) -> dict:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError("platform profile must be a YAML mapping")
    return data


def write_atomic(path: Path, profile: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent, delete=False) as handle:
        yaml.safe_dump(profile, handle, sort_keys=False)
        temporary = Path(handle.name)
    os.replace(temporary, path)


def show(profile: dict) -> None:
    cluster = profile["spec"]["cluster"]
    delivery = profile["spec"]["delivery"]
    values = {
        "domain": cluster["domain"],
        "local_domain": cluster["localDomain"],
        "overlay_domain": cluster["overlayDomain"],
        "overlay_cluster_tag": cluster["overlayClusterTag"],
        "repository_owner": cluster["repository"]["owner"],
        "repository_name": cluster["repository"]["name"],
        "bootstrap_base_url": delivery["bootstrapBaseUrl"],
        "r2_bucket": delivery["r2Bucket"],
    }
    for key, value in values.items():
        print(f"{key}={value}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, default=DEFAULT_PROFILE)
    parser.add_argument("--show", action="store_true")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--domain")
    parser.add_argument("--local-domain")
    parser.add_argument("--overlay-domain")
    parser.add_argument("--overlay-cluster-tag")
    parser.add_argument("--repository-owner")
    parser.add_argument("--repository-name")
    parser.add_argument("--bootstrap-base-url")
    parser.add_argument("--r2-bucket")
    args = parser.parse_args()

    profile = load_profile(args.profile)
    if args.show:
        show(profile)
        return 0

    required = (
        args.domain, args.local_domain, args.overlay_domain, args.overlay_cluster_tag,
        args.repository_owner, args.repository_name, args.bootstrap_base_url, args.r2_bucket,
    )
    if not args.output or any(value is None for value in required):
        parser.error("--output and every public profile value are required when updating a profile")

    cluster = profile["spec"]["cluster"]
    delivery = profile["spec"]["delivery"]
    cluster["domain"] = args.domain.strip().lower()
    cluster["localDomain"] = args.local_domain.strip().lower()
    cluster["overlayDomain"] = args.overlay_domain.strip().lower()
    cluster["overlayClusterTag"] = args.overlay_cluster_tag.strip()
    cluster["repository"] = {"owner": args.repository_owner.strip(), "name": args.repository_name.strip()}
    delivery["bootstrapBaseUrl"] = args.bootstrap_base_url.strip().rstrip("/")
    delivery["r2Bucket"] = args.r2_bucket.strip().lower()

    errors = load_validator().validate_profile(profile)
    if errors:
        raise ValueError("; ".join(errors))
    write_atomic(args.output, profile)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, yaml.YAMLError, RuntimeError) as exc:
        raise SystemExit(f"cannot configure platform profile: {exc}")
