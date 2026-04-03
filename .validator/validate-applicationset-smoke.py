#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path

import yaml


REPO_ROOT = Path(__file__).resolve().parents[1]
APPSET_PATH = REPO_ROOT / "pods" / "argocd" / "bootstrap" / "applicationset.yaml"
APP_FILES = sorted((REPO_ROOT / "pods").glob("*/*.app.yaml"))


def main() -> int:
    failures: list[str] = []
    appset = yaml.safe_load(APPSET_PATH.read_text(encoding="utf-8"))
    if not isinstance(appset, dict):
        print(f"{APPSET_PATH.relative_to(REPO_ROOT)}: top-level YAML must be a mapping", file=sys.stderr)
        return 1

    try:
        path_glob = appset["spec"]["generators"][0]["git"]["files"][0]["path"]
    except Exception as exc:  # noqa: BLE001
        print(f"{APPSET_PATH.relative_to(REPO_ROOT)}: unable to read ApplicationSet git file generator path: {exc}", file=sys.stderr)
        return 1

    if path_glob != "pods/*/*.app.yaml":
        failures.append(f"{APPSET_PATH.relative_to(REPO_ROOT)}: unexpected file generator path {path_glob!r}")

    if not APP_FILES:
        failures.append("pods/*/*.app.yaml: no app definitions discovered for ApplicationSet")

    for app_path in APP_FILES:
        data = yaml.safe_load(app_path.read_text(encoding="utf-8"))
        if not isinstance(data, dict):
            failures.append(f"{app_path.relative_to(REPO_ROOT)}: top-level YAML must be a mapping")
            continue
        source_type = data.get("source_type")
        if source_type == "git" and "source_path" not in data:
            failures.append(f"{app_path.relative_to(REPO_ROOT)}: git app missing source_path for ApplicationSet rendering")
        if source_type == "helm":
            for key in ("source_repo_url", "source_chart"):
                if not data.get(key):
                    failures.append(f"{app_path.relative_to(REPO_ROOT)}: helm app missing {key} for ApplicationSet rendering")
            has_inline_values = bool(data.get("source_helm_values"))
            has_value_files = isinstance(data.get("source_helm_value_files"), list) and bool(data.get("source_helm_value_files"))
            if not has_inline_values and not has_value_files:
                failures.append(f"{app_path.relative_to(REPO_ROOT)}: helm app missing source_helm_values or source_helm_value_files for ApplicationSet rendering")
        if source_type not in {"git", "helm"}:
            failures.append(f"{app_path.relative_to(REPO_ROOT)}: unsupported source_type for ApplicationSet rendering: {source_type!r}")

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
