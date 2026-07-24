#!/usr/bin/env python3
"""Validate Adaetum's public, non-secret platform profile."""
from __future__ import annotations

import argparse
import copy
import re
import sys
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

try:
    import yaml
except ImportError as exc:  # pragma: no cover - dependency failure is user-facing
    raise SystemExit("PyYAML is required: python3 -m pip install pyyaml") from exc


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_PROFILE = REPO_ROOT / "platform.yaml"
API_VERSION = "adaetum.io/v1alpha1"
SECRET_MARKERS = ("secret", "token", "password", "credential", "privatekey", "api_key")
DURATION_RE = re.compile(r"^(?:0|(?:[0-9]+(?:ns|us|µs|ms|s|m|h))+)$")


def is_duration(value: Any) -> bool:
    """Accept Go/systemd-compatible simple durations and numeric zero only."""
    if value == 0 and not isinstance(value, bool):
        return True
    return isinstance(value, str) and bool(DURATION_RE.fullmatch(value))


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
    unknown_spec_fields = set(spec) - {"installation", "cluster", "delivery", "hostMaintenance"}
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
        if isinstance(repository, dict):
            unknown_repository_fields = set(repository) - {"owner", "name", "branch"}
            if unknown_repository_fields:
                errors.append(
                    "unknown spec.cluster.repository fields: "
                    + ", ".join(sorted(unknown_repository_fields))
                )
        if not isinstance(repository, dict) or not all(
            isinstance(repository.get(field), str) and repository[field].strip()
            for field in ("owner", "name", "branch")
        ):
            errors.append("spec.cluster.repository.owner, .name, and .branch must be non-empty strings")
        elif not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._/-]*", repository["branch"]):
            errors.append("spec.cluster.repository.branch must be a valid Git branch name")
        elif repository["branch"].endswith(("/", ".")) or ".." in repository["branch"] or "//" in repository["branch"]:
            errors.append("spec.cluster.repository.branch must be a valid Git branch name")

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

    maintenance = spec.get("hostMaintenance")
    if not isinstance(maintenance, dict):
        errors.append("spec.hostMaintenance must be a mapping")
    else:
        expected_fields = {"enabled", "updates", "reboots", "safety", "observability"}
        unknown_fields = set(maintenance) - expected_fields
        if unknown_fields:
            errors.append(
                "unknown spec.hostMaintenance fields: "
                + ", ".join(sorted(unknown_fields))
            )
        if not isinstance(maintenance.get("enabled"), bool):
            errors.append("spec.hostMaintenance.enabled must be a boolean")

        updates = maintenance.get("updates")
        if not isinstance(updates, dict):
            errors.append("spec.hostMaintenance.updates must be a mapping")
        else:
            expected_updates = {"policy", "onCalendar", "randomizedDelay"}
            unknown_updates = set(updates) - expected_updates
            if unknown_updates:
                errors.append(
                    "unknown spec.hostMaintenance.updates fields: "
                    + ", ".join(sorted(unknown_updates))
                )
            if updates.get("policy") not in {"full", "security", "download-only", "disabled"}:
                errors.append(
                    "spec.hostMaintenance.updates.policy must be full, security, download-only, or disabled"
                )
            if not isinstance(updates.get("onCalendar"), str) or not updates["onCalendar"].strip():
                errors.append(
                    "spec.hostMaintenance.updates.onCalendar must be a non-empty systemd calendar string"
                )
            if not is_duration(updates.get("randomizedDelay")):
                errors.append(
                    "spec.hostMaintenance.updates.randomizedDelay must be a duration such as 30m or 1h"
                )

        reboots = maintenance.get("reboots")
        if not isinstance(reboots, dict):
            errors.append("spec.hostMaintenance.reboots must be a mapping")
        else:
            expected_reboots = {
                "enabled", "days", "startTime", "endTime", "timeZone", "period",
                "concurrency", "drainTimeout", "drainGracePeriodSeconds", "forceReboot",
                "lockTtl", "lockReleaseDelay", "rebootDelay",
            }
            unknown_reboots = set(reboots) - expected_reboots
            if unknown_reboots:
                errors.append(
                    "unknown spec.hostMaintenance.reboots fields: "
                    + ", ".join(sorted(unknown_reboots))
                )
            for field in ("enabled", "forceReboot"):
                if not isinstance(reboots.get(field), bool):
                    errors.append(f"spec.hostMaintenance.reboots.{field} must be a boolean")
            valid_days = {"su", "mo", "tu", "we", "th", "fr", "sa"}
            days = reboots.get("days")
            if (
                not isinstance(days, list)
                or not days
                or any(day not in valid_days for day in days)
                or len(days) != len(set(days))
            ):
                errors.append(
                    "spec.hostMaintenance.reboots.days must contain unique two-letter weekdays"
                )
            for field in ("startTime", "endTime"):
                value = reboots.get(field)
                if not isinstance(value, str) or not re.fullmatch(r"(?:[01]\d|2[0-3]):[0-5]\d", value):
                    errors.append(
                        f"spec.hostMaintenance.reboots.{field} must use 24-hour HH:MM"
                    )
            timezone = reboots.get("timeZone")
            if not isinstance(timezone, str) or not timezone:
                errors.append("spec.hostMaintenance.reboots.timeZone must be a non-empty IANA timezone")
            else:
                try:
                    ZoneInfo(timezone)
                except ZoneInfoNotFoundError:
                    errors.append("spec.hostMaintenance.reboots.timeZone must be a valid IANA timezone")
            for field in ("period", "drainTimeout", "lockReleaseDelay", "rebootDelay"):
                value = reboots.get(field)
                if not is_duration(value):
                    errors.append(
                        f"spec.hostMaintenance.reboots.{field} must be a duration such as 30m or integer zero"
                    )
            lock_ttl = reboots.get("lockTtl")
            if not is_duration(lock_ttl):
                errors.append(
                    "spec.hostMaintenance.reboots.lockTtl must be a duration such as 30m or integer zero"
                )
            concurrency = reboots.get("concurrency")
            if not isinstance(concurrency, int) or isinstance(concurrency, bool) or not 1 <= concurrency <= 10:
                errors.append("spec.hostMaintenance.reboots.concurrency must be an integer from 1 to 10")
            grace = reboots.get("drainGracePeriodSeconds")
            if not isinstance(grace, int) or isinstance(grace, bool) or grace < -1:
                errors.append(
                    "spec.hostMaintenance.reboots.drainGracePeriodSeconds must be -1 or greater"
                )

        safety = maintenance.get("safety")
        if not isinstance(safety, dict):
            errors.append("spec.hostMaintenance.safety must be a mapping")
        else:
            unknown_safety = set(safety) - {"prometheusGate", "blockingPodSelectors"}
            if unknown_safety:
                errors.append(
                    "unknown spec.hostMaintenance.safety fields: "
                    + ", ".join(sorted(unknown_safety))
                )
            if not isinstance(safety.get("prometheusGate"), bool):
                errors.append("spec.hostMaintenance.safety.prometheusGate must be a boolean")
            selectors = safety.get("blockingPodSelectors")
            if not isinstance(selectors, list) or any(
                not isinstance(selector, str) or not selector.strip()
                for selector in selectors
            ):
                errors.append(
                    "spec.hostMaintenance.safety.blockingPodSelectors must be a list of non-empty selectors"
                )

        observability = maintenance.get("observability")
        if not isinstance(observability, dict):
            errors.append("spec.hostMaintenance.observability must be a mapping")
        else:
            unknown_observability = set(observability) - {"metrics", "annotateNodes"}
            if unknown_observability:
                errors.append(
                    "unknown spec.hostMaintenance.observability fields: "
                    + ", ".join(sorted(unknown_observability))
                )
            for field in ("metrics", "annotateNodes"):
                if not isinstance(observability.get(field), bool):
                    errors.append(
                        f"spec.hostMaintenance.observability.{field} must be a boolean"
                    )

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
    unsafe_concurrency = copy.deepcopy(profile)
    unsafe_concurrency["spec"]["hostMaintenance"]["reboots"]["concurrency"] = 0
    if not any("concurrency" in error for error in validate_profile(unsafe_concurrency)):
        failures.append("invalid host-maintenance concurrency was accepted")
    invalid_timezone = copy.deepcopy(profile)
    invalid_timezone["spec"]["hostMaintenance"]["reboots"]["timeZone"] = "not/a-zone"
    if not any("timeZone" in error for error in validate_profile(invalid_timezone)):
        failures.append("invalid host-maintenance timezone was accepted")
    return failures


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, default=DEFAULT_PROFILE, help="Path to platform.yaml")
    parser.add_argument("--self-test", action="store_true", help="Run non-mutating contract rejection checks")
    parser.add_argument("--quiet", action="store_true", help="Print only validation errors")
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
    if not args.quiet:
        print(f"platform profile valid: {args.profile}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
