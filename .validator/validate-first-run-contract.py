#!/usr/bin/env python3
"""Regression checks for the first-run ownership boundaries."""
from __future__ import annotations

import os
import subprocess
import tempfile
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]
WIZARD = ROOT / "tasks" / "scripts" / "run-first-run-wizard.sh"
FIRST_RUN = ROOT / "tasks" / "scripts" / "first-run-preflight.sh"
ZONE_LISTER = ROOT / "tasks" / "scripts" / "list-cloudflare-zones.py"
TAILNET_LISTER = ROOT / "tasks" / "scripts" / "list-tailscale-tailnets.py"
SETUP = ROOT / "tasks" / "scripts" / "run-opinionated-setup.sh"
INITIAL_SETUP = ROOT / "tasks" / "scripts" / "run-initial-setup.sh"
UI = ROOT / "tasks" / "scripts" / "gum-ui.sh"
PROFILE = ROOT / "platform.yaml"
CONFIGURE = ROOT / "tasks" / "scripts" / "configure-platform-profile.py"
ISO = ROOT / "tasks" / "scripts" / "manage-rocky-installer-iso.sh"
WORKER = ROOT / ".github" / "workflows" / "ks-worker.yml"
ROCKY_HEADER = ROOT / "ks-src" / "fragments" / "installers" / "kickstart" / "rocky10" / "00-header.ksfrag"


def fail(message: str) -> None:
    raise SystemExit(f"first-run contract failed: {message}")


def main() -> int:
    wizard = WIZARD.read_text(encoding="utf-8")
    first_run = FIRST_RUN.read_text(encoding="utf-8")
    setup = SETUP.read_text(encoding="utf-8")
    initial_setup = INITIAL_SETUP.read_text(encoding="utf-8")
    ui = UI.read_text(encoding="utf-8")
    iso = ISO.read_text(encoding="utf-8")
    worker = WORKER.read_text(encoding="utf-8")
    rocky_header = ROCKY_HEADER.read_text(encoding="utf-8")
    if "ADAETUM_FIRST_RUN=1" not in wizard:
        fail("first-run launcher does not enter the shared setup program")
    if "I have updated platform.yaml" in first_run or "placed the Rocky Linux" in first_run:
        fail("wizard still asks the operator to prepare profile or ISO manually")
    if ".setup-opinionated.cache.env" in setup:
        fail("setup still persists the retired local secret cache")
    if "Continue with simulated GitHub browser sign-in?" not in first_run:
        fail("dry-run bypasses the GitHub sign-in decision")
    if "first_run_select_tailscale_domain" not in first_run:
        fail("first-run does not discover the Tailscale tailnet")
    if "first_run_capture_tailscale_oauth" not in first_run:
        fail("first-run does not collect the Tailscale enrollment client")
    if "Use ${selected_zone} as the cluster domain root?" in first_run:
        fail("Cloudflare zone selection still asks a redundant confirmation")
    if "first_run_domain=\"\"" not in first_run:
        fail("safe profile placeholders are still accepted as first-run defaults")
    fixture = subprocess.run(["python3", str(ZONE_LISTER), "--fixture"], text=True, capture_output=True, check=False)
    if fixture.returncode or "example.net" not in fixture.stdout:
        fail("dry-run Cloudflare zone fixture is unavailable")
    fixture = subprocess.run(["python3", str(TAILNET_LISTER), "--fixture"], text=True, capture_output=True, check=False)
    if fixture.returncode or "tailnet-a1b2.ts.net" not in fixture.stdout:
        fail("dry-run Tailscale tailnet fixture is unavailable")
    if 'Using the Cloudflare token authorized during provider setup.' not in setup:
        fail("setup re-prompts for the Cloudflare token captured during first run")
    if 'Using the GitHub credential authorized during fork setup.' not in setup:
        fail("setup re-prompts for the GitHub credential captured during first run")
    if 'platform_profile="${ADAETUM_PLATFORM_PROFILE:-${platform_profile}}"' not in setup:
        fail("dry-run reviewed profile is not carried into the shared setup steps")
    if 'bash ./tasks/scripts/run-initial-setup.sh' not in setup:
        fail("embedded bootstrap runner depends on an executable file mode")
    expected_sections = (
        'first_run_phase 1 "Fork"',
        'first_run_phase 2 "Providers"',
        'first_run_phase 3 "Profile"',
        'first_run_phase 4 "Installer"',
        'first_run_phase 5 "Bootstrap"',
    )
    if any(section not in first_run for section in expected_sections):
        fail("first-run does not carry the five-section journey through preparation")
    if 'adaetum_ui_milestone "5.${idx}"' not in setup:
        fail("bootstrap exposes a competing top-level step hierarchy")
    if 'INITIAL_SETUP_EMBEDDED_PREFIX=5.3' not in setup:
        fail("embedded publishing stages are not nested beneath bootstrap milestone 5.3")
    if 'adaetum_ui_completion "Dry run complete"' not in setup:
        fail("first-run has no five-section completion state")
    if 'adaetum_ui_panel "Your installer"' not in setup:
        fail("first-run does not present the completed installer")
    if 'adaetum_ui_key_value "ISO file"' not in setup:
        fail("installer handoff omits the generated ISO path")
    if "attach this ISO to the target physical host or VM" not in setup:
        fail("installer handoff omits the next physical-host action")
    if 'Rocky-10.2-${dry_run_arch}-minimal.iso' not in setup:
        fail("dry-run installer handoff does not preview a realistic ISO filename")
    for component in ("adaetum_ui_phase", "adaetum_ui_panel", "adaetum_ui_milestone", "adaetum_ui_completion"):
        if f"{component}()" not in ui:
            fail(f"shared console UI is missing {component}")
    if 'adaetum_ui_task "${initial_setup_embedded_prefix}.${idx}"' not in initial_setup:
        fail("embedded setup stages bypass the shared console hierarchy")
    ui_smoke = subprocess.run(
        [
            "bash",
            "-c",
            f'. "{UI}"; '
            'adaetum_ui_phase 5 5 "Bootstrap" "Prepare the installer."; '
            'adaetum_ui_milestone "5.3" "Publish bootstrap artifacts"; '
            'adaetum_ui_subtask "5.3.3.1" "Upload installer media"; '
            'adaetum_ui_completion "Dry run complete" "All five sections completed."',
        ],
        text=True,
        capture_output=True,
        check=False,
        env={**os.environ, "ADAETUM_GUM_UI": "0"},
    )
    if ui_smoke.returncode:
        fail(f"shared console UI smoke test failed: {ui_smoke.stderr.strip()}")
    for expected in ("SECTION 5/5", "[5.3] Publish bootstrap artifacts", "5.3.3.1", "Dry run complete"):
        if expected not in ui_smoke.stdout:
            fail(f"shared console UI did not render {expected!r}")
    if "${HOME}/Downloads" not in iso or "adopt)" not in iso:
        fail("installer discovery/adoption contract is incomplete")
    if 'const allowed = [".iso",' not in worker or "application/x-iso9660-image" not in worker:
        fail("bootstrap delivery worker does not serve ISO artifacts")
    if "GOLDEN_ISO_KEY=rocky10/Rocky-10.2-aarch64-minimal.iso" not in rocky_header:
        fail("kickstart golden ISO key does not match the supported Rocky release")

    with tempfile.TemporaryDirectory() as directory:
        output = Path(directory) / "platform.yaml"
        result = subprocess.run(
            [
                "python3", str(CONFIGURE), "--profile", str(PROFILE), "--output", str(output),
                "--domain", "example.test", "--local-domain", "example.test.local",
                "--overlay-domain", "example-tailnet.ts.net", "--overlay-cluster-tag", "tag:cluster",
                "--repository-owner", "gitea-admin", "--repository-name", "cluster",
                "--bootstrap-base-url", "https://bootstrap.example.test", "--r2-bucket", "iso",
            ],
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode:
            fail(f"profile proposal failed: {result.stderr.strip()}")
        proposed = yaml.safe_load(output.read_text(encoding="utf-8"))
        if proposed["spec"]["cluster"]["domain"] != "example.test":
            fail("profile proposal did not write collected public domain")
        if proposed["spec"]["delivery"]["bootstrapBaseUrl"] != "https://bootstrap.example.test":
            fail("profile proposal did not preserve reviewed bootstrap URL")
    print("first-run contract passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
