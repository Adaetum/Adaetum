#!/usr/bin/env python3
"""Render public platform profile values into generated pod configuration."""
from __future__ import annotations

import argparse
import importlib.util
import sys
from pathlib import Path

try:
    import yaml
except ImportError as exc:  # pragma: no cover
    raise SystemExit("PyYAML is required: python3 -m pip install pyyaml") from exc


REPO_ROOT = Path(__file__).resolve().parents[2]
PROFILE = REPO_ROOT / "platform.yaml"
PODS_RENDERER = REPO_ROOT / "tasks" / "scripts" / "render-pods-config.py"
PROFILE_VALIDATOR = REPO_ROOT / "tasks" / "scripts" / "validate-platform-profile.py"


def load_pods_renderer():
    """Load the internal pod-rendering implementation behind the public profile contract."""
    spec = importlib.util.spec_from_file_location("render_pods_config", PODS_RENDERER)
    if spec is None or spec.loader is None:
        raise RuntimeError("unable to load pod renderer")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def validate_profile(profile: dict) -> None:
    """Reject an invalid profile even when this renderer is called directly."""
    spec = importlib.util.spec_from_file_location("validate_platform_profile", PROFILE_VALIDATOR)
    if spec is None or spec.loader is None:
        raise RuntimeError("unable to load platform profile validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    errors = module.validate_profile(profile)
    if errors:
        raise ValueError("platform profile is invalid: " + "; ".join(errors))


def load_profile(path: Path) -> dict:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError("profile must be a YAML mapping")
    return data


def config_from_profile(profile: dict) -> dict[str, str]:
    """Derive every public runtime value from the recovery repository profile.

    Keep host naming here rather than scattered through manifests or setup
    scripts. The returned mapping is deliberately flat because its consumers
    are environment files, ConfigMaps, and template replacement code.
    """
    cluster = profile["spec"]["cluster"]
    domain = cluster["domain"]
    local_domain = cluster["localDomain"]
    repository = cluster["repository"]
    owner = repository["owner"]
    name = repository["name"]
    host = lambda service, suffix: f"{service}.{suffix}"
    config = {
        "CLUSTER_DOMAIN": domain,
        "CLUSTER_LOCAL_DOMAIN": local_domain,
        "GITEA_REPO_OWNER": owner,
        "GITEA_REPO_NAME": name,
        "GITEA_PUBLIC_HOST": host("gitea", domain),
        "GITEA_LOCAL_HOST": host("gitea", local_domain),
        "GITEA_CANONICAL_HOST": host("gitea", local_domain),
        "ARGOCD_PUBLIC_HOST": host("argocd", domain),
        "ARGOCD_LOCAL_HOST": host("argocd", local_domain),
        "OPENBAO_PUBLIC_HOST": host("openbao", domain),
        "OPENBAO_LOCAL_HOST": host("openbao", local_domain),
        "HOMEPAGE_PUBLIC_HOST": host("home", domain),
        "HOMEPAGE_LOCAL_HOST": host("home", local_domain),
        "HEADLAMP_PUBLIC_HOST": host("headlamp", domain),
        "HEADLAMP_LOCAL_HOST": host("headlamp", local_domain),
        "ALERTMANAGER_PUBLIC_HOST": host("alertmanager", domain),
        "ALERTMANAGER_LOCAL_HOST": host("alertmanager", local_domain),
        "GRAFANA_PUBLIC_HOST": host("grafana", domain),
        "GRAFANA_LOCAL_HOST": host("grafana", local_domain),
        "PROMETHEUS_PUBLIC_HOST": host("prometheus", domain),
        "PROMETHEUS_LOCAL_HOST": host("prometheus", local_domain),
        "RANCHER_PUBLIC_HOST": host("rancher", domain),
        "AUTHENTIK_PUBLIC_HOST": host("authentik", domain),
        "AUTHENTIK_LOCAL_HOST": host("authentik", local_domain),
        "AUTHENTIK_FORWARD_AUTH_URL": "http://authentik-server.authentik.svc.cluster.local:80/outpost.goauthentik.io/auth/nginx",
        "AUTHENTIK_LOCAL_AUTH_SIGNIN": "https://$host/outpost.goauthentik.io/start?rd=$scheme://$http_host$escaped_request_uri",
        "AUTHENTIK_PUBLIC_AUTH_SIGNIN": "https://$host/outpost.goauthentik.io/start?rd=$scheme://$http_host$escaped_request_uri",
        "AUTHENTIK_AUTH_RESPONSE_HEADERS": "Set-Cookie,X-authentik-username,X-authentik-groups,X-authentik-entitlements,X-authentik-email,X-authentik-name,X-authentik-uid",
        "AUTHENTIK_AUTH_SNIPPET": "proxy_set_header X-Forwarded-Host $http_host;",
        "REGISTRY_LOCAL_HOST": host("registry", local_domain),
        "REGISTRY_PUBLIC_HOST": host("registry", domain),
        "ANSIBLE_RUNNER_IMAGE": f"registry.{domain}/{owner}/ansible-runner:latest",
        "TAILSCALE_DOMAIN": cluster["overlayDomain"],
        "TAILSCALE_CLUSTER_TAG": cluster["overlayClusterTag"],
        "EXTERNAL_DNS_DOMAIN_FILTER": domain,
    }
    config["HOMEPAGE_ALLOWED_HOSTS"] = ",".join((config["HOMEPAGE_LOCAL_HOST"], config["HOMEPAGE_PUBLIC_HOST"], "homepage.homepage.svc.cluster.local", "homepage.homepage.svc", "localhost", "127.0.0.1"))
    return config


def setup_values_from_profile(profile: dict, config: dict[str, str]) -> dict[str, str]:
    """Return public values consumed by setup and break-glass tooling."""
    delivery = profile["spec"]["delivery"]
    return {
        "ADAETUM_CONFIG_CONTRACT": "platform/v1alpha1",
        "KS_BASE_URL": delivery["bootstrapBaseUrl"].rstrip("/"),
        "R2_BUCKET": delivery["r2Bucket"],
        "CLUSTER_DOMAIN": config["CLUSTER_DOMAIN"],
        "CLUSTER_LOCAL_DOMAIN": config["CLUSTER_LOCAL_DOMAIN"],
        "TAILSCALE_DOMAIN": config["TAILSCALE_DOMAIN"],
        "TAILSCALE_CLUSTER_TAG": config["TAILSCALE_CLUSTER_TAG"],
        "GITEA_SEED_TARGET_OWNER": config["GITEA_REPO_OWNER"],
        "GITEA_SEED_TARGET_REPO": config["GITEA_REPO_NAME"],
        "REGISTRY_PUBLIC_DOMAIN": config["REGISTRY_PUBLIC_HOST"],
        "RANCHER_PUBLIC_DOMAIN": config["RANCHER_PUBLIC_HOST"],
        "ANSIBLE_RUNNER_IMAGE": config["ANSIBLE_RUNNER_IMAGE"],
    }


def write_env_values(path: Path, values: dict[str, str]) -> None:
    """Replace only profile-owned public values while retaining runtime secrets."""
    existing = path.read_text(encoding="utf-8").splitlines() if path.exists() else []
    rendered: list[str] = []
    written: set[str] = set()
    for line in existing:
        # Preserve comments, blank lines, and all secret/runtime entries exactly
        # as the operator supplied them. Only the profile renderer owns keys in
        # ``values``.
        key = line.split("=", 1)[0].strip() if "=" in line else ""
        if key in values:
            rendered.append(f"{key}={values[key]}")
            written.add(key)
        else:
            rendered.append(line)
    for key in sorted(set(values) - written):
        rendered.append(f"{key}={values[key]}")
    path.write_text("\n".join(rendered).rstrip() + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, default=PROFILE)
    parser.add_argument("--output-env", type=Path, help="Write only the generated public config to this path")
    parser.add_argument("--output-setup-env", type=Path, help="Write public setup values derived from the profile")
    parser.add_argument("--runtime-env", type=Path, help="Update profile-owned public values in this runtime env file")
    parser.add_argument("--config-file", type=Path, help="Write the generated public cluster config to this path")
    parser.add_argument("--render-pods", action="store_true", help="Render tracked pod manifests from the profile")
    parser.add_argument("--check", action="store_true", help="Check generated pod manifests without writing")
    args = parser.parse_args()
    if args.check and not args.render_pods:
        parser.error("--check requires --render-pods")
    try:
        profile = load_profile(args.profile)
        validate_profile(profile)
        config = config_from_profile(profile)
        setup_values = setup_values_from_profile(profile, config)
        renderer = load_pods_renderer()
        # A derived key with no renderer owner would silently create a second
        # configuration contract. Fail before writing any generated artifact.
        unknown = set(config) - set(renderer.ENV_KEYS)
        if unknown:
            raise ValueError(f"renderer does not recognize generated keys: {', '.join(sorted(unknown))}")
        output = "".join(f"{key}={config[key]}\n" for key in renderer.ENV_KEYS)
        if args.output_env:
            args.output_env.parent.mkdir(parents=True, exist_ok=True)
            args.output_env.write_text(output, encoding="utf-8")
        elif not (args.output_setup_env or args.config_file or args.runtime_env or args.render_pods):
            sys.stdout.write(output)
        if args.output_setup_env:
            setup_output = "".join(f"{key}={value}\n" for key, value in sorted(setup_values.items()))
            args.output_setup_env.parent.mkdir(parents=True, exist_ok=True)
            args.output_setup_env.write_text(setup_output, encoding="utf-8")
        if args.config_file:
            args.config_file.write_text(output, encoding="utf-8")
        if args.runtime_env:
            write_env_values(
                args.runtime_env,
                setup_values,
            )
        if args.render_pods:
            failures = renderer.render_templates(config, check=args.check)
            failures.extend(renderer.render_app_configs(config, check=args.check))
            if failures:
                raise ValueError("\n".join(failures))
    except (KeyError, OSError, ValueError, yaml.YAMLError) as exc:
        print(f"cannot render platform profile: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
