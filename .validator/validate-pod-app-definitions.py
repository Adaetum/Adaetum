#!/usr/bin/env python3
"""Validate the small, declarative schema Adaetum uses for Argo pod apps."""
from __future__ import annotations

import sys
from pathlib import Path

import yaml


REPO_ROOT = Path(__file__).resolve().parents[1]
APP_FILES = sorted((REPO_ROOT / "pods").rglob("*.app.yaml"))

COMMON_REQUIRED = {"name", "project", "source_type", "source_target_revision", "destination_namespace"}
GIT_REQUIRED = {"source_path"}
HELM_REQUIRED = {"source_repo_url", "source_chart"}


def load_yaml(path: Path) -> dict:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"{path.relative_to(REPO_ROOT)}: top-level YAML must be a mapping")
    return data


def validate_app(path: Path, data: dict, seen_names: set[str]) -> list[str]:
    failures: list[str] = []
    rel = path.relative_to(REPO_ROOT)

    for key in sorted(COMMON_REQUIRED):
        value = data.get(key)
        if not isinstance(value, str) or not value.strip():
            failures.append(f"{rel}: missing required field {key}")

    name = data.get("name")
    if isinstance(name, str) and name:
        if name in seen_names:
            failures.append(f"{rel}: duplicate app name {name}")
        seen_names.add(name)

    source_type = data.get("source_type")
    if source_type not in {"git", "helm"}:
        failures.append(f"{rel}: source_type must be 'git' or 'helm'")
        return failures

    if source_type == "git":
        for key in sorted(GIT_REQUIRED):
            value = data.get(key)
            if not isinstance(value, str) or not value.strip():
                failures.append(f"{rel}: git app missing required field {key}")
        if "source_repo_url" in data:
            failures.append(f"{rel}: git app should not set source_repo_url; ApplicationSet supplies the repo")
        source_path = data.get("source_path")
        if isinstance(source_path, str) and source_path.strip():
            target = (REPO_ROOT / source_path).resolve()
            try:
                target.relative_to(REPO_ROOT.resolve())
            except ValueError:
                failures.append(f"{rel}: source_path escapes repo root: {source_path}")
            else:
                if not target.exists():
                    failures.append(f"{rel}: source_path does not exist: {source_path}")
                elif not target.is_dir():
                    failures.append(f"{rel}: source_path is not a directory: {source_path}")
                elif not (target / "kustomization.yaml").exists():
                    failures.append(f"{rel}: git source_path is missing kustomization.yaml: {source_path}")
    else:
        for key in sorted(HELM_REQUIRED):
            value = data.get(key)
            if not isinstance(value, str) or not value.strip():
                failures.append(f"{rel}: helm app missing required field {key}")
        has_inline_values = isinstance(data.get("source_helm_values"), str) and bool(data["source_helm_values"].strip())
        value_files = data.get("source_helm_value_files")
        has_value_files = isinstance(value_files, list) and bool(value_files)
        if not has_inline_values and not has_value_files:
            failures.append(f"{rel}: helm app must set source_helm_values or source_helm_value_files")
        if value_files is not None and not isinstance(value_files, list):
            failures.append(f"{rel}: source_helm_value_files must be a list when present")
        elif isinstance(value_files, list):
            for index, value_file in enumerate(value_files):
                if not isinstance(value_file, str) or not value_file.strip():
                    failures.append(f"{rel}: source_helm_value_files[{index}] must be a non-empty string")

    sync_options = data.get("sync_options")
    if sync_options is not None and not isinstance(sync_options, list):
        failures.append(f"{rel}: sync_options must be a list when present")

    ignore_differences = data.get("ignore_differences")
    if ignore_differences is not None and not isinstance(ignore_differences, list):
        failures.append(f"{rel}: ignore_differences must be a list when present")

    return failures


def main() -> int:
    failures: list[str] = []
    seen_names: set[str] = set()

    for path in APP_FILES:
        try:
            data = load_yaml(path)
        except Exception as exc:  # noqa: BLE001
            failures.append(str(exc))
            continue
        failures.extend(validate_app(path, data, seen_names))

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
