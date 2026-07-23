#!/usr/bin/env python3
"""Protect the platform-to-host automatic maintenance ownership contract."""
from __future__ import annotations

import copy
import importlib.util
import json
import sys
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]
PROFILE = ROOT / "platform.yaml"
RENDERER = ROOT / "tasks/scripts/render-platform-profile.py"
KURED_APP = ROOT / "pods/operations/kured.app.yaml"
PROMETHEUS_APP = ROOT / "pods/observability/prometheus.app.yaml"
DAY2 = ROOT / "ansible/playbooks/day2.yml"
ROLE_TASKS = ROOT / "ansible/automation-roles/host-maintenance/tasks/main.yml"
ROLE_DEFAULTS = ROOT / "ansible/automation-roles/host-maintenance/defaults/main.yml"


def load_renderer():
    spec = importlib.util.spec_from_file_location("render_platform_profile", RENDERER)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load platform profile renderer")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main() -> int:
    failures: list[str] = []
    renderer = load_renderer()
    profile = renderer.load_profile(PROFILE)
    renderer.validate_profile(profile)
    values = renderer.config_from_profile(profile)
    maintenance = profile["spec"]["hostMaintenance"]
    reboots = maintenance["reboots"]

    app = yaml.safe_load(KURED_APP.read_text(encoding="utf-8"))
    helm_values = yaml.safe_load(app["source_helm_values"])
    configuration = helm_values["configuration"]
    expected = {
        "rebootDays": reboots["days"],
        "startTime": reboots["startTime"],
        "endTime": reboots["endTime"],
        "timeZone": reboots["timeZone"],
        "concurrency": reboots["concurrency"],
        "drainTimeout": reboots["drainTimeout"],
        "drainGracePeriod": reboots["drainGracePeriodSeconds"],
        "forceReboot": reboots["forceReboot"],
        "lockTtl": str(reboots["lockTtl"]),
        "lockReleaseDelay": reboots["lockReleaseDelay"],
    }
    for key, expected_value in expected.items():
        if configuration.get(key) != expected_value:
            failures.append(
                f"Kured rendered {key}={configuration.get(key)!r}, expected {expected_value!r}"
            )

    if app.get("source_target_revision") != "6.1.0":
        failures.append("Kured chart must remain pinned to reviewed version 6.1.0")
    if "RespectIgnoreDifferences=true" not in app.get("sync_options", []):
        failures.append("Argo must respect Kured's live lock annotation")
    ignored = app.get("ignore_differences", [])
    if not any(
        "/metadata/annotations/weave.works~1kured-node-lock"
        in entry.get("jsonPointers", [])
        for entry in ignored
    ):
        failures.append("Kured's live DaemonSet lock annotation is not ignored by Argo")
    if configuration.get("alertFilterRegexp") != "^AdaetumMaintenance":
        failures.append("Kured must gate only on Adaetum maintenance alerts")
    if not configuration.get("alertFilterMatchOnly") or not configuration.get("alertFiringOnly"):
        failures.append("Kured Prometheus gating must consider matching firing alerts only")
    if json.loads(values["HOST_MAINTENANCE_SENTINEL_COMMAND"]) != 'sh -c "! needs-restarting --reboothint"':
        failures.append("Rocky reboot detection does not use needs-restarting --reboothint")

    paused = copy.deepcopy(profile)
    paused["spec"]["hostMaintenance"]["enabled"] = False
    paused_values = renderer.config_from_profile(paused)
    if json.loads(paused_values["HOST_MAINTENANCE_SENTINEL_COMMAND"]) != "/bin/false":
        failures.append("master pause does not make Kured's reboot check inert")
    if json.loads(paused_values["HOST_MAINTENANCE_PROMETHEUS_URL"]) != "":
        failures.append("master pause unexpectedly retains the Prometheus reboot gate")

    day2 = DAY2.read_text(encoding="utf-8")
    for required in (
        "serial: 1",
        "../../platform.yaml",
        "role: host-maintenance",
        "adaetum_platform_profile.spec.hostMaintenance",
    ):
        if required not in day2:
            failures.append(f"day-two maintenance ownership is missing {required!r}")

    role_tasks = ROLE_TASKS.read_text(encoding="utf-8")
    role_contract = role_tasks + ROLE_DEFAULTS.read_text(encoding="utf-8")
    for required in (
        "dnf-automatic",
        "host_maintenance_timer_unit",
        "needs-restarting --help",
    ):
        if required not in role_contract:
            failures.append(f"host-maintenance role is missing {required!r}")
    for forbidden in ("ansible.builtin.reboot", "systemctl reboot", "shutdown -r"):
        if forbidden in role_tasks:
            failures.append(f"host-maintenance role must not own reboot execution ({forbidden})")

    prometheus = yaml.safe_load(PROMETHEUS_APP.read_text(encoding="utf-8"))
    prometheus_values = yaml.safe_load(prometheus["source_helm_values"])
    rule_groups = prometheus_values["additionalPrometheusRulesMap"]["adaetum-host-maintenance"]["groups"]
    alert_names = {
        rule["alert"]
        for group in rule_groups
        for rule in group.get("rules", [])
        if "alert" in rule
    }
    required_alerts = {
        "AdaetumMaintenanceNodeNotReady",
        "AdaetumMaintenanceNodePressure",
        "AdaetumMaintenanceKubeletUnavailable",
        "AdaetumMaintenanceDaemonSetUnavailable",
        "AdaetumMaintenanceNodeStillCordoned",
    }
    missing_alerts = required_alerts - alert_names
    if missing_alerts:
        failures.append("missing maintenance blockers: " + ", ".join(sorted(missing_alerts)))

    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print("host maintenance profile, Rocky updater, Kured, and health-gate contracts passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
