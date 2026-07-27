#!/usr/bin/env python3
"""Protect Adaetum's bounded, Argo-compatible workload autoscaling contract."""
from __future__ import annotations

import sys
from pathlib import Path

import yaml


REPO_ROOT = Path(__file__).resolve().parents[1]
TARGETS = {
    ("cloudflared", "cloudflared"): {
        "app": "pods/cloudflared/cloudflared.app.yaml",
        "deployment": "pods/cloudflared/cloudflared/deployment.yaml",
        "autoscaling": "pods/cloudflared/cloudflared/autoscaling.yaml",
        "container": "cloudflared",
    },
    ("observability", "apprise"): {
        "app": "pods/observability/apprise.app.yaml",
        "deployment": "pods/observability/apprise/apprise-deployment.yaml",
        "autoscaling": "pods/observability/apprise/autoscaling.yaml",
        "container": "apprise",
    },
    ("homepage", "homepage"): {
        "app": "pods/portal/homepage.app.yaml",
        "deployment": "pods/portal/homepage/deployment.yaml",
        "autoscaling": "pods/portal/homepage/autoscaling.yaml",
        "container": "homepage",
    },
}


def load(path: str) -> dict:
    data = yaml.safe_load((REPO_ROOT / path).read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"{path}: expected a YAML mapping")
    return data


def documents(path: str) -> list[dict]:
    return [
        item
        for item in yaml.safe_load_all((REPO_ROOT / path).read_text(encoding="utf-8"))
        if isinstance(item, dict)
    ]


def helm_values(path: str) -> tuple[dict, dict]:
    app = load(path)
    values = yaml.safe_load(app.get("source_helm_values", ""))
    if not isinstance(values, dict):
        raise ValueError(f"{path}: source_helm_values must parse as a mapping")
    return app, values


def require(condition: bool, message: str, failures: list[str]) -> None:
    if not condition:
        failures.append(message)


def find_kind(items: list[dict], kind: str) -> dict:
    matches = [item for item in items if item.get("kind") == kind]
    if len(matches) != 1:
        raise ValueError(f"expected exactly one {kind}, found {len(matches)}")
    return matches[0]


def validate_workload(namespace: str, name: str, config: dict, failures: list[str]) -> None:
    deployment = find_kind(documents(config["deployment"]), "Deployment")
    spec = deployment.get("spec", {})
    pod = spec.get("template", {})
    pod_spec = pod.get("spec", {})
    pod_labels = pod.get("metadata", {}).get("labels", {})
    require(spec.get("replicas", 0) >= 2, f"{name}: Git replica floor must be at least two", failures)
    rolling = spec.get("strategy", {}).get("rollingUpdate", {})
    require(rolling.get("maxUnavailable") == 0, f"{name}: rolling updates must allow zero unavailable pods", failures)
    require(
        pod_labels.get("autoscaling.adaetum.io/rebalance") == "true",
        f"{name}: approved workload is missing the rebalance allowlist label",
        failures,
    )
    spread = pod_spec.get("topologySpreadConstraints", [])
    require(
        any(
            rule.get("topologyKey") == "kubernetes.io/hostname"
            and rule.get("whenUnsatisfiable") == "ScheduleAnyway"
            for rule in spread
        ),
        f"{name}: must prefer safe cross-node spreading without blocking a single-node cluster",
        failures,
    )
    container = next(
        (item for item in pod_spec.get("containers", []) if item.get("name") == config["container"]),
        {},
    )
    resources = container.get("resources", {})
    require(
        all(resources.get(section, {}).get(resource) for section in ("requests", "limits") for resource in ("cpu", "memory")),
        f"{name}: main container needs CPU/memory requests and limits",
        failures,
    )
    resize = {item.get("resourceName"): item.get("restartPolicy") for item in container.get("resizePolicy", [])}
    require(
        resize.get("memory") == "NotRequired",
        f"{name}: memory resize must not request a restart",
        failures,
    )

    scaling = documents(config["autoscaling"])
    hpa = find_kind(scaling, "HorizontalPodAutoscaler").get("spec", {})
    require(hpa.get("minReplicas", 0) >= 2, f"{name}: HPA must never scale below two", failures)
    require(hpa.get("maxReplicas", 0) > hpa.get("minReplicas", 0), f"{name}: HPA needs scale-out headroom", failures)
    require(
        hpa.get("scaleTargetRef", {}).get("name") == name,
        f"{name}: HPA target does not match its Deployment",
        failures,
    )
    metrics = hpa.get("metrics", [])
    require(
        len(metrics) == 1 and metrics[0].get("resource", {}).get("name") == "cpu",
        f"{name}: HPA must exclusively own CPU scaling",
        failures,
    )
    behavior = hpa.get("behavior", {})
    require(
        behavior.get("scaleUp", {}).get("stabilizationWindowSeconds") == 0,
        f"{name}: scale-up must not be delayed",
        failures,
    )
    require(
        behavior.get("scaleDown", {}).get("stabilizationWindowSeconds", 0) >= 900,
        f"{name}: scale-down stabilization must be at least 15 minutes",
        failures,
    )
    pdb = find_kind(scaling, "PodDisruptionBudget").get("spec", {})
    require(pdb.get("minAvailable", 0) >= 1, f"{name}: a PodDisruptionBudget must retain availability", failures)

    app = load(config["app"])
    ignored = app.get("ignore_differences", [])
    require(
        any(
            item.get("kind") == "Deployment"
            and item.get("name") == name
            and "/spec/replicas" in item.get("jsonPointers", [])
            for item in ignored
        ),
        f"{name}: Argo must ignore the HPA-owned replica field",
        failures,
    )
    require(
        "RespectIgnoreDifferences=true" in app.get("sync_options", []),
        f"{name}: Argo must respect the replica ownership boundary during sync",
        failures,
    )


def validate_vpa(failures: list[str]) -> None:
    app, values = helm_values("pods/operations/vertical-pod-autoscaler.app.yaml")
    require(app.get("source_target_revision") == "0.11.0", "VPA chart must remain pinned to 0.11.0", failures)
    for component in ("admissionController", "recommender", "updater"):
        config = values.get(component, {})
        require(config.get("replicas", 0) >= 2, f"VPA {component} must run at least two replicas", failures)
        require(
            config.get("podDisruptionBudget", {}).get("minAvailable", 0) >= 1,
            f"VPA {component} needs a disruption budget",
            failures,
        )
    require(
        "--feature-gates=InPlace=true" in values.get("admissionController", {}).get("extraArgs", []),
        "VPA admission controller must enable in-place resize",
        failures,
    )
    require(
        "--feature-gates=InPlace=true" in values.get("updater", {}).get("extraArgs", []),
        "VPA updater must enable in-place resize",
        failures,
    )
    require(
        values.get("admissionController", {}).get("mutatingWebhookConfiguration", {}).get("failurePolicy") == "Ignore",
        "VPA admission must fail open so controller trouble cannot block pods",
        failures,
    )

    vpas = documents("pods/operations/workload-autosizing/vertical-pod-autoscalers.yaml")
    seen: set[tuple[str, str]] = set()
    for item in vpas:
        key = (item.get("metadata", {}).get("namespace"), item.get("spec", {}).get("targetRef", {}).get("name"))
        seen.add(key)
        spec = item.get("spec", {})
        require(spec.get("updatePolicy", {}).get("updateMode") == "InPlace", f"{key}: VPA must be in-place only", failures)
        require(spec.get("updatePolicy", {}).get("minReplicas", 0) >= 2, f"{key}: VPA needs a two-replica floor", failures)
        policies = spec.get("resourcePolicy", {}).get("containerPolicies", [])
        controlled = next((policy for policy in policies if policy.get("containerName") != "*"), {})
        require(controlled.get("controlledResources") == ["memory"], f"{key}: VPA may control memory only", failures)
        require(controlled.get("controlledValues") == "RequestsOnly", f"{key}: VPA may control requests only", failures)
        wildcard = next((policy for policy in policies if policy.get("containerName") == "*"), {})
        require(wildcard.get("mode") == "Off", f"{key}: unlisted containers must be excluded from VPA", failures)
    require(seen == set(TARGETS), f"VPA allowlist differs from approved targets: {sorted(seen)}", failures)


def validate_supporting_controllers(failures: list[str]) -> None:
    gold_app, gold = helm_values("pods/operations/goldilocks.app.yaml")
    require(gold_app.get("source_target_revision") == "10.4.1", "Goldilocks chart must remain pinned to 10.4.1", failures)
    require(gold.get("controller", {}).get("enabled") is False, "Goldilocks controller must remain disabled", failures)
    require(gold.get("vpa", {}).get("enabled") is False, "Goldilocks must not install a second VPA", failures)
    require(gold.get("metrics-server", {}).get("enabled") is False, "Goldilocks must use RKE2 metrics-server", failures)
    require(gold.get("dashboard", {}).get("flags", {}).get("show-all") is True, "Goldilocks must show Git-owned VPAs", failures)

    descheduler_app, descheduler = helm_values("pods/operations/descheduler.app.yaml")
    require(descheduler_app.get("source_target_revision") == "0.35.1", "Descheduler must match Kubernetes 1.35", failures)
    require(descheduler.get("kind") == "CronJob", "Descheduler must remain a bounded CronJob", failures)
    require(descheduler.get("schedule") == "*/30 * * * *", "Descheduler interval must remain 30 minutes", failures)
    policy = descheduler.get("deschedulerPolicy", {})
    require(policy.get("maxNoOfPodsToEvictPerNode") == 1, "Descheduler may evict only one pod per node", failures)
    require(policy.get("maxNoOfPodsToEvictPerNamespace") == 1, "Descheduler may evict only one pod per namespace", failures)
    require(policy.get("maxNoOfPodsToEvictTotal", 0) <= 3, "Descheduler total eviction cap is too high", failures)
    profiles = policy.get("profiles", [])
    config = profiles[0].get("pluginConfig", []) if len(profiles) == 1 else []
    evictor = next((item.get("args", {}) for item in config if item.get("name") == "DefaultEvictor"), {})
    require(evictor.get("nodeFit") is True, "Descheduler must verify a replacement node exists", failures)
    require(evictor.get("minReplicas", 0) >= 2, "Descheduler must exclude singleton workloads", failures)
    require(
        evictor.get("labelSelector", {}).get("matchLabels", {}).get("autoscaling.adaetum.io/rebalance") == "true",
        "Descheduler must use the explicit rebalance allowlist",
        failures,
    )
    protections = set(evictor.get("podProtections", {}).get("extraEnabled", []))
    require({"PodsWithPVC", "PodsWithoutPDB"}.issubset(protections), "Descheduler must protect PVC and non-PDB pods", failures)


def validate_crafty_fixed_size(failures: list[str]) -> None:
    """Keep the traditional stateful-service example outside autoscaling."""
    items = documents("pods/games/crafty/crafty.yaml")
    deployment = find_kind(items, "Deployment")
    spec = deployment.get("spec", {})
    pod = spec.get("template", {})
    container = next(
        (item for item in pod.get("spec", {}).get("containers", []) if item.get("name") == "crafty"),
        {},
    )
    require(spec.get("replicas") == 1, "crafty: Git must own a single replica", failures)
    require(spec.get("strategy", {}).get("type") == "Recreate", "crafty: RWO workload must use Recreate", failures)
    require(
        "autoscaling.adaetum.io/rebalance" not in pod.get("metadata", {}).get("labels", {}),
        "crafty: singleton must not opt into descheduler rebalancing",
        failures,
    )
    resources = container.get("resources", {})
    require(
        all(resources.get(section, {}).get(resource) for section in ("requests", "limits") for resource in ("cpu", "memory")),
        "crafty: fixed-size container needs Git-owned CPU and memory requests and limits",
        failures,
    )
    require(
        not any(item.get("kind") in {"HorizontalPodAutoscaler", "VerticalPodAutoscaler"} for item in items),
        "crafty: workload manifest must not contain an HPA or VPA",
        failures,
    )


def main() -> int:
    failures: list[str] = []
    try:
        for (namespace, name), config in TARGETS.items():
            validate_workload(namespace, name, config, failures)
        validate_vpa(failures)
        validate_supporting_controllers(failures)
        validate_crafty_fixed_size(failures)
    except (OSError, ValueError, yaml.YAMLError) as exc:
        failures.append(str(exc))

    # A future deployment must be explicitly added to TARGETS and receive all
    # safeguards before the descheduler label can make it eligible for eviction.
    labeled: set[tuple[str, str]] = set()
    for path in (REPO_ROOT / "pods").rglob("*.yaml"):
        if ".app." in path.name:
            continue
        try:
            for item in yaml.safe_load_all(path.read_text(encoding="utf-8")):
                if not isinstance(item, dict) or item.get("kind") != "Deployment":
                    continue
                metadata = item.get("metadata", {})
                labels = item.get("spec", {}).get("template", {}).get("metadata", {}).get("labels", {})
                if labels.get("autoscaling.adaetum.io/rebalance") == "true":
                    labeled.add((metadata.get("namespace"), metadata.get("name")))
        except yaml.YAMLError:
            continue
    require(labeled == set(TARGETS), f"rebalance label allowlist differs from approved targets: {sorted(labeled)}", failures)

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    print("Workload autoscaling contract is valid.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
