#!/usr/bin/env python3
"""Ensure every rendered pod template is declared in the renderer inventory."""
from __future__ import annotations

import importlib.util
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
RENDER_SCRIPT = REPO_ROOT / "tasks" / "scripts" / "render-pods-config.py"


def load_render_module():
    spec = importlib.util.spec_from_file_location("render_pods_config", RENDER_SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load render script: {RENDER_SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main() -> int:
    module = load_render_module()
    template_targets = [(REPO_ROOT / src, REPO_ROOT / dst) for src, dst in module.TEMPLATE_TARGETS]
    app_config_targets = [(REPO_ROOT / dst) for dst, _name, _keys in module.APP_CONFIG_TARGETS]
    failures: list[str] = []

    mapped_templates = {src.resolve() for src, _ in template_targets}
    mapped_targets = {dst.resolve() for _, dst in template_targets}

    for src, dst in template_targets:
        if not src.exists():
            failures.append(f"missing template file: {src.relative_to(REPO_ROOT)}")
        if not dst.exists():
            failures.append(f"missing rendered target file: {dst.relative_to(REPO_ROOT)}")

    for target in app_config_targets:
        if not target.exists():
            failures.append(f"missing rendered app config target file: {target.relative_to(REPO_ROOT)}")

    for tmpl in (REPO_ROOT / "pods").rglob("*.tmpl"):
        if tmpl.resolve() not in mapped_templates:
            failures.append(f"orphan template not mapped by render script: {tmpl.relative_to(REPO_ROOT)}")

    for src, dst in template_targets:
        if dst.suffix == ".tmpl":
            failures.append(f"render target cannot itself be a template: {dst.relative_to(REPO_ROOT)}")
        if src.with_suffix("") != dst and src.name.endswith(".yaml.tmpl") and dst.suffix not in {".yaml", ".app.yaml"}:
            failures.append(f"unexpected rendered target for template {src.relative_to(REPO_ROOT)} -> {dst.relative_to(REPO_ROOT)}")

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
