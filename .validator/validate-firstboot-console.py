#!/usr/bin/env python3
"""Protect the first-boot console's runner-to-renderer status contract."""
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
RUNNER = ROOT / "ansible" / "ansible-scripts" / "bundle-bootstrap"
PHASE90 = ROOT / "ansible" / "ansible-scripts" / "bootstrap" / "Phase-90" / "run-phase90.sh"
CONTROL_PAIR_COMMON = ROOT / "ansible" / "ansible-scripts" / "bootstrap" / "control-pair-common.sh"
CONSOLE = ROOT / "ks-src" / "fragments" / "shared" / "portable" / "11-tailscale-firstboot-lib.shfrag"
FIRSTBOOT_FLOW = ROOT / "ks-src" / "fragments" / "shared" / "portable" / "12-tailscale-firstboot-flow.shfrag"
ISO_BUILD = ROOT / "tasks" / "iso.yml"
INSTALLER_HANDOFF = ROOT / "ks-src" / "fragments" / "installers" / "kickstart" / "rocky10" / "60-embed-repo.ksfrag"
ISO_WORKFLOW = ROOT / ".github" / "workflows" / "iso-build.yml"


def main() -> int:
    runner = RUNNER.read_text(encoding="utf-8")
    phase90 = PHASE90.read_text(encoding="utf-8")
    control_pair_common = CONTROL_PAIR_COMMON.read_text(encoding="utf-8")
    console = CONSOLE.read_text(encoding="utf-8")
    firstboot_flow = FIRSTBOOT_FLOW.read_text(encoding="utf-8")
    iso_build = ISO_BUILD.read_text(encoding="utf-8")
    installer_handoff = INSTALLER_HANDOFF.read_text(encoding="utf-8")
    iso_workflow = ISO_WORKFLOW.read_text(encoding="utf-8")
    fields = (
        "BOOTSTRAP_PROGRESS_PHASE_DETAIL",
        "BOOTSTRAP_PROGRESS_PHASE_LOG_PATH",
        "BOOTSTRAP_PROGRESS_PHASE_STARTED_AT",
        "BOOTSTRAP_PROGRESS_UPDATED_AT",
        "BOOTSTRAP_PROGRESS_RUN_STARTED_AT",
        "BOOTSTRAP_PROGRESS_PHASE_BASELINE_SECONDS",
        "BOOTSTRAP_PROGRESS_TOTAL_REMAINING_BASELINE_SECONDS",
    )
    for field in fields:
        if f"printf '{field}=%q\\n'" not in runner:
            raise SystemExit(f"first-boot console contract failed: runner does not publish {field}")
        if field not in console:
            raise SystemExit(f"first-boot console contract failed: console does not consume {field}")
    for required in (
        "phase_detail_for_status()",
        "bootstrap_phase_status_detail_from_log()",
        "bootstrap_phase_status_start_log_monitor",
        "Ansible task:",
    ):
        if required not in runner:
            raise SystemExit(f"first-boot console contract failed: runner does not publish detailed progress ({required})")
    for required in (
        "gum_status_latest_activity()",
        "gum_status_log_freshness()",
        "gum_status_render()",
        "GUM_STATUS_LOG_DUMPS",
        "[GUM-STATUS] snapshot",
        "Estimate:",
        '"${GUM_BIN}" style --border rounded',
    ):
        if required not in console:
            raise SystemExit(f"first-boot console contract failed: missing {required}")
    for forbidden in (
        "ui_",
        "\\033[",
        "STATUS_TTY_PATH",
        "STATUS_SCROLL_TOP",
    ):
        if forbidden in console:
            raise SystemExit(f"first-boot console contract failed: legacy renderer remains ({forbidden})")
    for required in (
        "bootstrap_wait_for_deployment_rollout",
        "observability grafana grafana 'app.kubernetes.io/name=grafana'",
    ):
        if required not in phase90:
            raise SystemExit(f"Grafana rollout recovery contract failed: missing {required}")
    for required in (
        "bootstrap_capture_deployment_rollout_diagnostics()",
        'rollout status "deploy/${deployment}" --timeout=45s',
        'get pvc -o wide',
        'get events --sort-by=.lastTimestamp',
        'logs "${pod_name}" --all-containers --previous --tail=200',
        "ProgressDeadlineExceeded",
    ):
        if required not in control_pair_common:
            raise SystemExit(f"deployment rollout recovery contract failed: missing {required}")
    for required in (
        "gum_status_start_monitor 15",
        "gum_status_stop_monitor",
        "gum_status_result",
    ):
        if required not in firstboot_flow:
            raise SystemExit(f"first-boot console contract failed: flow does not use {required}")
    if "ui_" in firstboot_flow:
        raise SystemExit("first-boot console contract failed: flow still calls the legacy renderer")
    for required in (
        'gum_version="0.17.0"',
        'gum_rpm_path="${work_dir}/ks/gum.rpm"',
        'Gum RPM checksum mismatch',
        'ISO_GUM_RPM_SOURCE',
    ):
        if required not in iso_build:
            raise SystemExit(f"first-boot console contract failed: ISO does not bake {required}")
    for required in (
        'gum_source="/run/install/repo/ks/gum.rpm"',
        'rpm --root "${target_root}" -Uvh --replacepkgs "${gum_source}"',
        'gum_target="${target_root}/usr/bin/gum"',
    ):
        if required not in installer_handoff:
            raise SystemExit(f"first-boot console contract failed: installer does not require {required}")
    for required in (
        'GUM_BIN="/usr/bin/gum"',
        'Required Gum console binary is missing',
        '"${GUM_BIN}" style --bold --foreground 39 -- "Adaetum first-boot status renderer ready"',
    ):
        if required not in console:
            raise SystemExit(f"first-boot console contract failed: renderer does not require {required}")
    for required in (
        'Prepare required Gum console dependency',
        'ISO_GUM_RPM_SOURCE=${gum_path}',
        'gum-${gum_version}-1.${gum_arch}.rpm',
    ):
        if required not in iso_workflow:
            raise SystemExit(f"first-boot console contract failed: Actions does not prepare {required}")
    print("first-boot console contract passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
