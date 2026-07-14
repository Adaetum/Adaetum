#!/usr/bin/env python3
"""Validate Adaetum's public, non-secret platform profile."""
from __future__ import annotations

import argparse
import copy
import sys
from pathlib import Path
from typing import Any

try:
    import yaml
except ImportError as exc:  # pragma: no cover - dependency failure is user-facing
    raise SystemExit("PyYAML is required: python3 -m pip install pyyaml") from exc


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_PROFILE = REPO_ROOT / "platform.yaml"
API_VERSION = "adaetum.io/v1alpha1"
SECRET_MARKERS = ("secret", "token", "password", "credential", "privatekey", "api_key")


def load_yaml(path: Path) -> dict[str, Any]:
    try:
        data = yaml.safe_load(path.read_text(encoding="utf-8"))
    except (OSError, yaml.YAMLError) as exc:
        raise ValueError(f"cannot read {path}: {exc}") from exc
    if not isinstance(data, dict):
        raise ValueError(f"{path}: expected a YAML mapping")
    return data


def validate_profile(profile: dict[str, Any]) -> list[str]:
    """Return every contract violation without exposing profile values."""
    errors: list[str] = []
    if profile.get("apiVersion") != API_VERSION:
        errors.append(f"apiVersion must be {API_VERSION}")
    if profile.get("kind") != "PlatformProfile":
        errors.append("kind must be PlatformProfile")
    unknown_top_level = set(profile) - {"apiVersion", "kind", "metadata", "spec"}
    if unknown_top_level:
        errors.append(f"unknown top-level fields: {', '.join(sorted(unknown_top_level))}")

    metadata = profile.get("metadata")
    if not isinstance(metadata, dict) or not isinstance(metadata.get("name"), str) or not metadata["name"].strip():
        errors.append("metadata.name must be a non-empty string")

    spec = profile.get("spec")
    if not isinstance(spec, dict):
        return errors + ["spec must be a mapping"]
    unknown_spec_fields = set(spec) - {"installation", "cluster", "delivery"}
    if unknown_spec_fields:
        errors.append(f"unknown spec fields: {', '.join(sorted(unknown_spec_fields))}")
    if spec.get("installation") != "rocky10":
        errors.append("spec.installation must be rocky10 (the only stable installer)")

    cluster = spec.get("cluster")
    if not isinstance(cluster, dict):
        errors.append("spec.cluster must be a mapping")
    else:
        expected_cluster_fields = {"domain", "localDomain", "overlayDomain", "overlayClusterTag", "repository"}
        unknown_cluster_fields = set(cluster) - expected_cluster_fields
        if unknown_cluster_fields:
            errors.append(f"unknown spec.cluster fields: {', '.join(sorted(unknown_cluster_fields))}")
        for field in ("domain", "localDomain", "overlayDomain", "overlayClusterTag"):
            if not isinstance(cluster.get(field), str) or not cluster[field].strip():
                errors.append(f"spec.cluster.{field} must be a non-empty string")
        repository = cluster.get("repository")
        if not isinstance(repository, dict) or not all(
            isinstance(repository.get(field), str) and repository[field].strip()
            for field in ("owner", "name")
        ):
            errors.append("spec.cluster.repository.owner and .name must be non-empty strings")

    delivery = spec.get("delivery")
    if not isinstance(delivery, dict):
        errors.append("spec.delivery must be a mapping")
    else:
        expected_delivery_fields = {"bootstrapBaseUrl", "r2Bucket"}
        unknown_delivery_fields = set(delivery) - expected_delivery_fields
        if unknown_delivery_fields:
            errors.append(f"unknown spec.delivery fields: {', '.join(sorted(unknown_delivery_fields))}")
        bootstrap_url = delivery.get("bootstrapBaseUrl")
        if not isinstance(bootstrap_url, str) or not bootstrap_url.startswith(("https://", "http://")):
            errors.append("spec.delivery.bootstrapBaseUrl must be an http(s) URL")
        bucket = delivery.get("r2Bucket")
        if not isinstance(bucket, str) or not bucket.strip():
            errors.append("spec.delivery.r2Bucket must be a non-empty string")

    def contains_secret_key(value: Any, path: str = "") -> None:
        if isinstance(value, dict):
            for key, child in value.items():
                key_text = str(key).lower().replace("-", "").replace("_", "")
                child_path = f"{path}.{key}" if path else str(key)
                if any(marker in key_text for marker in SECRET_MARKERS):
                    errors.append(f"{child_path} looks like secret material; profiles must be non-secret")
                contains_secret_key(child, child_path)
        elif isinstance(value, list):
            for index, child in enumerate(value):
                contains_secret_key(child, f"{path}[{index}]")

    contains_secret_key(spec, "spec")
    return errors


def run_self_test() -> list[str]:
    """Exercise required rejection paths without writing files or using secrets."""
    profile = load_yaml(DEFAULT_PROFILE)
    failures: list[str] = []
    if validate_profile(profile):
        failures.append("the committed platform profile must be valid")

    unsupported_installer = copy.deepcopy(profile)
    unsupported_installer["spec"]["installation"] = "ubuntu24"
    if "spec.installation must be rocky10 (the only stable installer)" not in validate_profile(unsupported_installer):
        failures.append("unsupported installer was accepted")

    unknown_field = copy.deepcopy(profile)
    unknown_field["spec"]["unrecognized"] = "value"
    if "unknown spec fields: unrecognized" not in validate_profile(unknown_field):
        failures.append("unknown profile field was accepted")

    secret_field = copy.deepcopy(profile)
    secret_field["spec"]["apiToken"] = "not-a-real-secret"
    if not any("secret material" in error for error in validate_profile(secret_field)):
        failures.append("secret-shaped profile field was accepted")
    return failures


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, default=DEFAULT_PROFILE, help="Path to platform.yaml")
    parser.add_argument("--self-test", action="store_true", help="Run non-mutating contract rejection checks")
    args = parser.parse_args()
    try:
        errors = validate_profile(load_yaml(args.profile))
    except ValueError as exc:
        print(f"platform profile invalid: {exc}", file=sys.stderr)
        return 1
    if errors:
        print("platform profile invalid:", file=sys.stderr)
        print("\n".join(f"- {error}" for error in errors), file=sys.stderr)
        return 1
    if args.self_test:
        failures = run_self_test()
        if failures:
            print("platform profile self-test failed:", file=sys.stderr)
            print("\n".join(f"- {failure}" for failure in failures), file=sys.stderr)
            return 1
    print(f"platform profile valid: {args.profile}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
