#!/usr/bin/env python3
"""Protect the first-boot console's runner-to-renderer status contract."""
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
RUNNER = ROOT / "ansible" / "ansible-scripts" / "bundle-bootstrap"
PHASE90 = ROOT / "ansible" / "ansible-scripts" / "bootstrap" / "Phase-90" / "run-phase90.sh"
PHASE99 = ROOT / "ansible" / "ansible-scripts" / "bootstrap" / "Phase-90" / "run-phase99.sh"
PHASE10 = ROOT / "ansible" / "ansible-scripts" / "bootstrap" / "Phase-10" / "run-phase10.sh"
CONTROL_PAIR_COMMON = ROOT / "ansible" / "ansible-scripts" / "bootstrap" / "control-pair-common.sh"
CONSOLE = ROOT / "ks-src" / "fragments" / "shared" / "portable" / "11-tailscale-firstboot-lib.shfrag"
FIRSTBOOT_FLOW = ROOT / "ks-src" / "fragments" / "shared" / "portable" / "12-tailscale-firstboot-flow.shfrag"
ISO_BUILD = ROOT / "tasks" / "iso.yml"
INSTALLER_HANDOFF = ROOT / "ks-src" / "fragments" / "installers" / "kickstart" / "rocky10" / "60-embed-repo.ksfrag"
ISO_WORKFLOW = ROOT / ".github" / "workflows" / "iso-build.yml"


def main() -> int:
    runner = RUNNER.read_text(encoding="utf-8")
    phase10 = PHASE10.read_text(encoding="utf-8")
    phase90 = PHASE90.read_text(encoding="utf-8")
    phase99 = PHASE99.read_text(encoding="utf-8")
    control_pair_common = CONTROL_PAIR_COMMON.read_text(encoding="utf-8")
    console = CONSOLE.read_text(encoding="utf-8")
    firstboot_flow = FIRSTBOOT_FLOW.read_text(encoding="utf-8")
    iso_build = ISO_BUILD.read_text(encoding="utf-8")
    installer_handoff = INSTALLER_HANDOFF.read_text(encoding="utf-8")
    iso_workflow = ISO_WORKFLOW.read_text(encoding="utf-8")
    for required in (
        "task platform:validate",
        "task bootstrap:phase10:validate-runtime",
    ):
        if required not in phase10:
            raise SystemExit(f"Phase 10 intake contract failed: required check is missing ({required})")
    for forbidden in (
        "prek run",
        "pre-commit run",
        "bootstrap:phase10:check-ks",
        "bootstrap:phase10:compile-ks",
        "bootstrap:phase10:validate-pods-contract",
    ):
        if forbidden in phase10:
            raise SystemExit(
                f"Phase 10 intake contract failed: repository validation remains ({forbidden})"
            )
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
        "OpenBao secret delivery:",
    ):
        if required not in runner:
            raise SystemExit(f"first-boot console contract failed: runner does not publish detailed progress ({required})")
    for required in (
        'public_env_file="${bundle_repo_root}/pods/cluster-config/cluster-config.env"',
        'while IFS= read -r public_env_line',
        'printf -v "${public_env_key}"',
        'export "${public_env_key}"',
    ):
        if required not in runner:
            raise SystemExit(
                f"first-boot public-profile contract failed: bundle runner is missing {required}"
            )
    if runner.find('while IFS= read -r public_env_line') < runner.find('. "${env_file}"'):
        raise SystemExit(
            "first-boot public-profile contract failed: public profile must override runtime defaults"
        )
    failure_capture = re.compile(
        r'if run_step "\$\{key\}" "\$\{label\}" "\$@"; then.*?return 0\s*'
        r'else\s*(?:#.*\n\s*)*rc=\$\?\s*fi.*?'
        r'bootstrap_phase_status_write "\$\{phase_id\}" "\$\{key\}" "FAILED".*?'
        r'return "\$\{rc\}"',
        re.DOTALL,
    )
    if not failure_capture.search(runner):
        raise SystemExit(
            "bootstrap phase failure-propagation contract failed: "
            "run_step's exit code must be captured in its else branch"
        )
    if 'export KUBECONFIG="${KUBECONFIG:-/etc/rancher/rke2/rke2.yaml}"' not in phase99:
        raise SystemExit("Phase 99 kubectl contract failed: RKE2 KUBECONFIG default is missing")
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
        "bootstrap_capture_external_secret_diagnostics()",
        "bootstrap_wait_for_external_secret_delivery()",
        "bootstrap_wait_for_csi_secret_delivery()",
        "BOOTSTRAP_EXTERNAL_SECRET_TIMEOUT_SECONDS",
        "BOOTSTRAP_EXTERNAL_SECRET_POLL_SECONDS",
        "ProgressDeadlineExceeded",
        "get application external-secrets openbao-secret-sync -o wide",
        "get deployments,pods -o wide",
        'rollout status "deploy/${deployment}" --timeout=45s',
        'get pvc -o wide',
        'get externalsecret -o wide',
        'get clustersecretstore openbao -o wide',
        'logs deploy/external-secrets --all-containers --tail=200',
        'get events --sort-by=.lastTimestamp',
        'logs "${pod_name}" --all-containers --previous --tail=200',
    ):
        if required not in control_pair_common:
            raise SystemExit(f"deployment rollout recovery contract failed: missing {required}")
    for required in (
        "verify_openbao_secret_delivery_phase70()",
        "reconcile_csi_runtime_rotations_phase70()",
        "BOOTSTRAP_RECONCILE_SECRET_ROTATION",
        "gitea-openbao gitea",
        "authentik-openbao authentik",
        "grafana-openbao grafana",
        "apprise-openbao apprise",
        "required OpenBao-backed workload secrets did not synchronize",
    ):
        if required not in (ROOT / "ansible" / "ansible-scripts" / "bootstrap" / "Phase-70" / "run-phase70.sh").read_text(encoding="utf-8"):
            raise SystemExit(f"OpenBao secret-delivery gate contract failed: missing {required}")
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
