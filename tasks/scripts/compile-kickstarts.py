#!/usr/bin/env python3
"""Compile generated kickstart artifacts from ks-src manifests into dist."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import tempfile
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any

try:
    from jinja2 import Environment, FileSystemLoader, StrictUndefined
except ImportError as exc:  # pragma: no cover - import path failure is user-facing
    print(
        "Jinja2 is required for kickstart compilation. Install it with "
        "'python3 -m pip install jinja2' or run via 'uv run --with jinja2'.",
        file=sys.stderr,
    )
    raise SystemExit(1) from exc


REPO_ROOT = Path(__file__).resolve().parents[2]
KS_SRC_DIR = REPO_ROOT / "ks-src"
MANIFESTS_DIR = KS_SRC_DIR / "manifests"
REQUIRED_ADAPTERS = ("package_manager", "service_manager", "filesystem", "networking")
KICKSTART_PHASE_ORDER = {
    "header": 0,
    "pre": 1,
    "include": 2,
    "packages": 3,
    "post-install": 4,
    "firstboot": 5,
}
FRAGMENT_ROOTS = {
    "installer": "fragments/installers",
    "shared": "fragments/shared",
    "os-family": "fragments/os-family",
}
FORBIDDEN_SHARED_TOKENS = (
    r"\bdnf\b",
    r"\byum\b",
    r"\bapt-get\b",
    r"\bapt\b",
    r"\bzypper\b",
    r"\bpacman\b",
    r"\brpm\b",
    r"\bsystemctl\b",
    r"/run/install/",
    r"/mnt/sysroot",
    r"/usr/lib/systemd/system/",
    r"\bkickstart\b",
    r"\bautoinstall\b",
    r"\banaconda\b",
    r"\bsubiquity\b",
    r"%post\b",
)


class CompileError(RuntimeError):
    """Raised when manifest validation or rendering fails."""


class Reporter:
    def __init__(self) -> None:
        force_color = os.environ.get("FORCE_COLOR", "").strip().lower()
        no_color = os.environ.get("NO_COLOR", "").strip()
        self.use_color = bool(
            not no_color
            and (sys.stdout.isatty() or force_color in {"1", "true", "yes", "on"})
        )
        self.reset = "\033[0m" if self.use_color else ""
        self.bold = "\033[1m" if self.use_color else ""
        self.blue = "\033[1;34m" if self.use_color else ""
        self.green = "\033[1;32m" if self.use_color else ""
        self.yellow = "\033[1;33m" if self.use_color else ""
        self.red = "\033[1;31m" if self.use_color else ""
        self.cyan = "\033[1;36m" if self.use_color else ""

    def header(self, text: str) -> None:
        print(f"{self.cyan}{self.bold}{text}{self.reset}")

    def step(self, index: int, total: int, text: str) -> None:
        print(f"{self.blue}[{index}/{total}]{self.reset} {text}")

    def ok(self, text: str) -> None:
        print(f"{self.green}OK{self.reset} {text}")

    def warn(self, text: str) -> None:
        print(f"{self.yellow}WARN{self.reset} {text}")

    def error(self, text: str) -> None:
        print(f"{self.red}ERROR{self.reset} {text}", file=sys.stderr)

    def item(self, text: str) -> None:
        print(f"  - {text}")


@dataclass(frozen=True)
class Manifest:
    name: str
    installer_type: str
    os_family: str
    template: str
    output: str
    enabled: bool
    shared_fragments: tuple[str, ...]
    os_family_adapters: dict[str, str]
    sections: tuple[dict[str, Any], ...]
    raw: dict[str, Any]


def repo_rel(path: Path) -> str:
    return path.resolve().relative_to(REPO_ROOT.resolve()).as_posix()


def load_manifest(path: Path) -> Manifest:
    raw = json.loads(path.read_text(encoding="utf-8"))
    required = ("name", "installer_type", "os_family", "template", "sections")
    missing = [key for key in required if key not in raw]
    if missing:
        raise CompileError(f"{repo_rel(path)} missing required keys: {', '.join(missing)}")

    output = raw.get("output", "")
    return Manifest(
        name=str(raw["name"]),
        installer_type=str(raw["installer_type"]),
        os_family=str(raw["os_family"]),
        template=str(raw["template"]),
        output=str(output),
        enabled=bool(raw.get("enabled", True)),
        shared_fragments=tuple(str(item) for item in raw.get("shared_fragments", [])),
        os_family_adapters={str(k): str(v) for k, v in (raw.get("os_family_adapters") or {}).items()},
        sections=tuple(dict(item) for item in raw["sections"]),
        raw=raw,
    )


def ensure_relative_file(path_str: str, *, label: str) -> Path:
    path = (KS_SRC_DIR / path_str).resolve()
    if KS_SRC_DIR.resolve() not in path.parents and path != KS_SRC_DIR.resolve():
        raise CompileError(f"{label} escapes ks-src: {path_str}")
    if not path.is_file():
        raise CompileError(f"{label} is missing: {path_str}")
    return path


def validate_fragment_path(manifest: Manifest, section: dict[str, Any]) -> None:
    section_id = str(section.get("id", "<unknown>"))
    kind = str(section.get("kind", ""))
    path = str(section.get("path", ""))
    if kind not in FRAGMENT_ROOTS:
        raise CompileError(f"{manifest.name}: section {section_id} has unsupported kind '{kind}'")
    ensure_relative_file(path, label=f"{manifest.name} section {section_id} fragment")

    if kind == "installer":
        expected = f"fragments/installers/{manifest.installer_type}/"
        if not path.startswith(expected):
            raise CompileError(
                f"{manifest.name}: section {section_id} must live under {expected}, got {path}"
            )
    elif kind == "shared":
        if not path.startswith("fragments/shared/"):
            raise CompileError(f"{manifest.name}: section {section_id} must live under fragments/shared/")
    elif kind == "os-family":
        expected = f"fragments/os-family/{manifest.os_family}/"
        if not path.startswith(expected):
            raise CompileError(
                f"{manifest.name}: section {section_id} must live under {expected}, got {path}"
            )


def validate_shared_fragment_portability(path: Path) -> None:
    text = path.read_text(encoding="utf-8")
    for pattern in FORBIDDEN_SHARED_TOKENS:
        match = re.search(pattern, text, flags=re.IGNORECASE)
        if match:
            raise CompileError(
                f"{repo_rel(path)} violates the shared portability contract: found '{match.group(0)}'"
            )


def validate_manifest(manifest: Manifest, *, require_output: bool = False) -> None:
    template_path = ensure_relative_file(manifest.template, label=f"{manifest.name} template")

    if manifest.installer_type not in {"kickstart", "autoinstall"}:
        raise CompileError(f"{manifest.name}: unsupported installer_type '{manifest.installer_type}'")
    if not manifest.sections:
        raise CompileError(f"{manifest.name}: sections must not be empty")
    if require_output and not manifest.output:
        raise CompileError(f"{manifest.name}: output is required in this mode")

    missing_adapters = [key for key in REQUIRED_ADAPTERS if key not in manifest.os_family_adapters]
    if missing_adapters:
        raise CompileError(
            f"{manifest.name}: missing os_family_adapters: {', '.join(missing_adapters)}"
        )

    section_ids: set[str] = set()
    last_phase_index = -1
    for section in manifest.sections:
        section_id = str(section.get("id", ""))
        if not section_id:
            raise CompileError(f"{manifest.name}: each section needs a non-empty id")
        if section_id in section_ids:
            raise CompileError(f"{manifest.name}: duplicate section id '{section_id}'")
        section_ids.add(section_id)

        validate_fragment_path(manifest, section)
        if str(section.get("kind", "")) == "shared":
            section_path = ensure_relative_file(
                str(section.get("path", "")),
                label=f"{manifest.name} section {section_id} fragment",
            )
            validate_shared_fragment_portability(section_path)

        if manifest.installer_type == "kickstart":
            phase = str(section.get("phase", ""))
            if phase not in KICKSTART_PHASE_ORDER:
                raise CompileError(f"{manifest.name}: section {section_id} has invalid phase '{phase}'")
            phase_index = KICKSTART_PHASE_ORDER[phase]
            if phase_index < last_phase_index:
                raise CompileError(
                    f"{manifest.name}: invalid section ordering around '{section_id}' (phase {phase})"
                )
            last_phase_index = phase_index

    for shared_fragment in manifest.shared_fragments:
        shared_path = ensure_relative_file(shared_fragment, label=f"{manifest.name} shared fragment")
        if not shared_fragment.startswith("fragments/shared/"):
            raise CompileError(
                f"{manifest.name}: shared fragment must live under fragments/shared/, got {shared_fragment}"
            )
        validate_shared_fragment_portability(shared_path)

    for adapter_name, adapter_path_str in manifest.os_family_adapters.items():
        adapter_path = ensure_relative_file(
            adapter_path_str, label=f"{manifest.name} os-family adapter '{adapter_name}'"
        )
        expected = f"fragments/os-family/{manifest.os_family}/"
        if not adapter_path_str.startswith(expected):
            raise CompileError(
                f"{manifest.name}: os-family adapter '{adapter_name}' must live under {expected}"
            )

    if manifest.output:
        output_path = (REPO_ROOT / manifest.output).resolve()
        if REPO_ROOT.resolve() not in output_path.parents:
            raise CompileError(f"{manifest.name}: output escapes repo root: {manifest.output}")
        if output_path.suffix != ".ks":
            # Future autoinstall outputs may change extension; keep the rule narrow for kickstart only.
            if manifest.installer_type == "kickstart":
                raise CompileError(f"{manifest.name}: kickstart outputs must end in .ks")

    if template_path.suffix != ".j2":
        raise CompileError(f"{manifest.name}: template must use .j2 suffix")


def build_jinja_env() -> Environment:
    env = Environment(
        loader=FileSystemLoader(str(KS_SRC_DIR)),
        undefined=StrictUndefined,
        autoescape=False,
        keep_trailing_newline=True,
        trim_blocks=False,
        lstrip_blocks=False,
    )

    def include_fragment(path_str: str) -> str:
        path = ensure_relative_file(path_str, label="fragment include")
        return path.read_text(encoding="utf-8")

    env.globals["include_fragment"] = include_fragment
    return env


def render_manifest(manifest: Manifest) -> str:
    validate_manifest(manifest, require_output=False)
    env = build_jinja_env()
    template = env.get_template(manifest.template)
    rendered = template.render(manifest=manifest.raw)
    rendered = rendered.replace("\r\n", "\n").replace("\r", "\n")
    rendered = rendered.rstrip("\n") + "\n"

    if "__KS_INTERNAL_" in rendered:
        raise CompileError(f"{manifest.name}: unresolved internal assembly marker remains in output")
    if manifest.installer_type == "kickstart" and "# GOLDEN_ISO_KEY=" not in rendered:
        raise CompileError(f"{manifest.name}: rendered output is missing # GOLDEN_ISO_KEY=")
    return rendered


def write_or_check_output(manifest: Manifest, content: str, *, check: bool) -> None:
    if not manifest.output:
        return
    output_path = REPO_ROOT / manifest.output
    existing = output_path.read_text(encoding="utf-8") if output_path.exists() else None
    if check:
        if existing != content:
            raise CompileError(f"{manifest.name}: generated output is stale: {manifest.output}")
        return
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(content, encoding="utf-8", newline="\n")


def iter_manifests(selected_names: set[str] | None) -> list[tuple[Path, Manifest]]:
    manifests: list[tuple[Path, Manifest]] = []
    for path in sorted(MANIFESTS_DIR.glob("*.json")):
        manifest = load_manifest(path)
        if selected_names and manifest.name not in selected_names:
            continue
        manifests.append((path, manifest))
    if selected_names:
        found = {manifest.name for _, manifest in manifests}
        missing = sorted(selected_names - found)
        if missing:
            raise CompileError(f"Unknown manifest(s): {', '.join(missing)}")
    return manifests


def compile_manifests(
    selected_names: set[str] | None, *, check: bool, validate_all: bool, quiet: bool = False
) -> list[str]:
    manifests = iter_manifests(selected_names)
    if not manifests:
        raise CompileError("No manifests found under ks-src/manifests")

    compiled_outputs: list[str] = []
    for _, manifest in manifests:
        validate_manifest(manifest, require_output=False)
        if validate_all and not manifest.enabled:
            continue
        if not manifest.enabled:
            continue
        if not manifest.output:
            raise CompileError(f"{manifest.name}: enabled manifests must define output")
        content = render_manifest(manifest)
        write_or_check_output(manifest, content, check=check)
        compiled_outputs.append(f"{manifest.name} -> {manifest.output}")

    if not quiet:
        if check:
            print(f"Kickstart compile check passed ({len(compiled_outputs)} generated artifact(s) verified).")
        else:
            print(f"Kickstart compile completed ({len(compiled_outputs)} generated artifact(s)).")
        for item in compiled_outputs:
            print(f"  - {item}")
    return compiled_outputs


def validate_all_manifests(selected_names: set[str] | None, *, quiet: bool = False) -> int:
    manifests = iter_manifests(selected_names)
    if not manifests:
        raise CompileError("No manifests found under ks-src/manifests")
    validated_count = 0
    for _, manifest in manifests:
        validate_manifest(manifest, require_output=False)
        if manifest.enabled and manifest.output:
            render_manifest(manifest)
        validated_count += 1
    if not quiet:
        print(f"Kickstart manifest validation passed ({validated_count} manifest(s)).")
    return validated_count


def self_test() -> None:
    with tempfile.TemporaryDirectory(prefix="ks-compile-selftest-") as tmp:
        root = Path(tmp)
        src = root / "ks-src"
        (src / "manifests").mkdir(parents=True)
        (src / "templates").mkdir()
        (src / "fragments" / "installers" / "kickstart" / "testos").mkdir(parents=True)
        (src / "fragments" / "shared" / "portable").mkdir(parents=True)
        (src / "fragments" / "os-family" / "rhel").mkdir(parents=True)

        (src / "templates" / "ok.ks.j2").write_text(
            "{% for section in manifest.sections %}{{ include_fragment(section.path) }}{% endfor %}",
            encoding="utf-8",
        )
        (src / "fragments" / "installers" / "kickstart" / "testos" / "00.ksfrag").write_text(
            "# GOLDEN_ISO_KEY=test.iso\n", encoding="utf-8"
        )
        (src / "fragments" / "shared" / "portable" / "ok.shfrag").write_text(
            "bootstrap_require_commands() {\n  return 0\n}\n",
            encoding="utf-8",
        )
        for name in REQUIRED_ADAPTERS:
            (src / "fragments" / "os-family" / "rhel" / f"{name}.shfrag").write_text(
                f"{name}_adapter() {{\n  return 0\n}}\n",
                encoding="utf-8",
            )

        good_manifest = {
            "name": "ok",
            "installer_type": "kickstart",
            "os_family": "rhel",
            "template": "templates/ok.ks.j2",
            "output": "dist/ks-templates/ok.ks",
            "enabled": True,
            "shared_fragments": ["fragments/shared/portable/ok.shfrag"],
            "os_family_adapters": {
                name: f"fragments/os-family/rhel/{name}.shfrag" for name in REQUIRED_ADAPTERS
            },
            "sections": [
                {
                    "id": "header",
                    "phase": "header",
                    "kind": "installer",
                    "path": "fragments/installers/kickstart/testos/00.ksfrag",
                }
            ],
        }
        (src / "manifests" / "ok.json").write_text(json.dumps(good_manifest), encoding="utf-8")

        bad_duplicate = dict(good_manifest)
        bad_duplicate["name"] = "dup"
        bad_duplicate["sections"] = good_manifest["sections"] * 2
        (src / "manifests" / "dup.json").write_text(json.dumps(bad_duplicate), encoding="utf-8")

        bad_adapter = dict(good_manifest)
        bad_adapter["name"] = "missing-adapter"
        adapters = dict(good_manifest["os_family_adapters"])
        adapters.pop("networking")
        bad_adapter["os_family_adapters"] = adapters
        (src / "manifests" / "missing-adapter.json").write_text(
            json.dumps(bad_adapter), encoding="utf-8"
        )

        bad_misuse = dict(good_manifest)
        bad_misuse["name"] = "misuse"
        bad_misuse["sections"] = [
            {
                "id": "header",
                "phase": "header",
                "kind": "installer",
                "path": "fragments/shared/portable/ok.shfrag",
            }
        ]
        (src / "manifests" / "misuse.json").write_text(json.dumps(bad_misuse), encoding="utf-8")

        (src / "templates" / "unresolved.ks.j2").write_text("{{ not_defined }}", encoding="utf-8")
        bad_unresolved = dict(good_manifest)
        bad_unresolved["name"] = "unresolved"
        bad_unresolved["template"] = "templates/unresolved.ks.j2"
        (src / "manifests" / "unresolved.json").write_text(
            json.dumps(bad_unresolved), encoding="utf-8"
        )

        (src / "fragments" / "shared" / "portable" / "bad.shfrag").write_text(
            "bad_helper() {\n  dnf -y install git\n}\n",
            encoding="utf-8",
        )
        bad_portability = dict(good_manifest)
        bad_portability["name"] = "bad-portability"
        bad_portability["shared_fragments"] = ["fragments/shared/portable/bad.shfrag"]
        (src / "manifests" / "bad-portability.json").write_text(
            json.dumps(bad_portability), encoding="utf-8"
        )

        (src / "fragments" / "shared" / "portable" / "bad-installer.shfrag").write_text(
            'bad_installer_marker() {\n  echo "/run/install/repo"\n}\n',
            encoding="utf-8",
        )
        bad_installer_marker = dict(good_manifest)
        bad_installer_marker["name"] = "bad-installer-marker"
        bad_installer_marker["sections"] = [
            {
                "id": "header",
                "phase": "header",
                "kind": "installer",
                "path": "fragments/installers/kickstart/testos/00.ksfrag",
            },
            {
                "id": "bad-shared",
                "phase": "firstboot",
                "kind": "shared",
                "path": "fragments/shared/portable/bad-installer.shfrag",
            },
        ]
        (src / "manifests" / "bad-installer-marker.json").write_text(
            json.dumps(bad_installer_marker), encoding="utf-8"
        )

        global REPO_ROOT, KS_SRC_DIR, MANIFESTS_DIR
        old_root, old_src, old_manifests = REPO_ROOT, KS_SRC_DIR, MANIFESTS_DIR
        REPO_ROOT, KS_SRC_DIR, MANIFESTS_DIR = root, src, src / "manifests"
        try:
            compile_manifests({"ok"}, check=False, validate_all=True, quiet=True)
            expected_failures = {
                "dup": "duplicate section id",
                "missing-adapter": "missing os_family_adapters",
                "misuse": "must live under fragments/installers",
                "unresolved": "not_defined",
                "bad-portability": "shared portability contract",
                "bad-installer-marker": "shared portability contract",
            }
            for name, expected_text in expected_failures.items():
                try:
                    validate_all_manifests({name})
                except Exception as exc:  # noqa: BLE001 - self-test failure surface
                    if expected_text not in str(exc):
                        raise AssertionError(
                            f"self-test {name} failed with unexpected error: {exc}"
                        ) from exc
                else:
                    raise AssertionError(f"self-test {name} unexpectedly passed")
        finally:
            REPO_ROOT, KS_SRC_DIR, MANIFESTS_DIR = old_root, old_src, old_manifests


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--manifest",
        action="append",
        default=[],
        help="Manifest name to compile/validate (repeatable). Defaults to all manifests.",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="Fail if generated outputs do not match committed files.",
    )
    parser.add_argument(
        "--validate-all",
        action="store_true",
        help="Validate all manifests, including dry-run manifests without outputs.",
    )
    parser.add_argument(
        "--self-test",
        action="store_true",
        help="Run compiler self-tests before processing repo manifests.",
    )
    parser.add_argument(
        "--sync",
        action="store_true",
        help="Run self-tests, validate manifests, compile outputs, and verify the generated artifacts in one pass.",
    )
    return parser.parse_args()


def run_sync(selected: set[str] | None, *, self_test_enabled: bool, reporter: Reporter) -> None:
    total_steps = 4 if self_test_enabled else 3
    step_index = 1
    start = time.perf_counter()

    reporter.header("Kickstart Sync")

    if self_test_enabled:
        reporter.step(step_index, total_steps, "Running compiler self-tests")
        self_test()
        reporter.ok("Compiler self-tests passed.")
        step_index += 1

    reporter.step(step_index, total_steps, "Validating manifests")
    validated_count = validate_all_manifests(selected, quiet=True)
    reporter.ok(f"Validated {validated_count} manifest(s).")
    step_index += 1

    reporter.step(step_index, total_steps, "Compiling generated artifacts")
    compiled_outputs = compile_manifests(selected, check=False, validate_all=True, quiet=True)
    reporter.ok(f"Compiled {len(compiled_outputs)} generated artifact(s).")
    for item in compiled_outputs:
        reporter.item(item)
    step_index += 1

    reporter.step(step_index, total_steps, "Verifying generated artifacts")
    verified_outputs = compile_manifests(selected, check=True, validate_all=True, quiet=True)
    reporter.ok(f"Verified {len(verified_outputs)} generated artifact(s).")
    for item in verified_outputs:
        reporter.item(item)

    elapsed = time.perf_counter() - start
    reporter.ok(f"Kickstart sync finished successfully in {elapsed:.2f}s.")


def main() -> int:
    args = parse_args()
    selected = set(args.manifest) if args.manifest else None
    reporter = Reporter()
    try:
        if args.sync:
            run_sync(selected, self_test_enabled=args.self_test, reporter=reporter)
            return 0
        if args.self_test:
            self_test()
            reporter.ok("Compiler self-tests passed.")
        if args.validate_all:
            validated = validate_all_manifests(selected, quiet=True)
            reporter.ok(f"Validated {validated} manifest(s).")
        compiled = compile_manifests(selected, check=args.check, validate_all=args.validate_all, quiet=True)
        if args.check:
            reporter.ok(f"Verified {len(compiled)} generated artifact(s).")
        else:
            reporter.ok(f"Compiled {len(compiled)} generated artifact(s).")
        for item in compiled:
            reporter.item(item)
    except Exception as exc:  # noqa: BLE001 - convert to CLI failure
        reporter.error(str(exc))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
