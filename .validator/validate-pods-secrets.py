#!/usr/bin/env python3
"""Detect probable secret values accidentally committed in pods source or setup docs."""
from __future__ import annotations

import re
import sys
from pathlib import Path

import yaml


REPO_ROOT = Path(__file__).resolve().parents[1]
SCAN_PATHS = [REPO_ROOT / "pods", REPO_ROOT / "setup.md"]

ROTATION_CONTRACTS = {
    "ansible/playbooks/day2.yml": (
        "tailscale-user-sync",
        "healthcheck",
    ),
    "ansible/Dockerfile": (
        'ENV ANSIBLE_PLAYBOOK="playbooks/day2.yml"',
    ),
    "ansible/ansible-scripts/cron-entrypoint": (
        'playbooks/day2.yml',
    ),
    "ansible/ansible-scripts/run-ansible": (
        'playbooks/day2.yml',
    ),
    "pods/ansible/ansible/ansible-runner-deployment.yaml": (
        'value: "playbooks/day2.yml"',
        "secret.reloader.stakater.com/reload: gitea-registry-creds",
        "secretProviderClass: ansible-runner-openbao",
    ),
    "pods/ansible/ansible/csi.yaml": (
        "kind: SecretProviderClass",
        "roleName: ansible-runner",
        "secretPath: secret/data/apps/ansible/tailscale",
        "objectName: oauth_client_id",
        "objectName: oauth_client_secret",
    ),
    "pods/cloudflared/cloudflared/deployment.yaml": (
        "serviceAccountName: cloudflared",
        "secretProviderClass: cloudflared-openbao",
        "name: cloudflared-tunnel",
        "name: TUNNEL_TOKEN",
    ),
    "pods/ingress/external-dns/deployment.yaml": (
        "secretProviderClass: external-dns-openbao",
        "name: external-dns-cloudflare",
        "name: CF_API_TOKEN",
    ),
    "pods/portal/homepage/deployment.yaml": (
        "secretProviderClass: homepage-openbao",
        'secret_dir = Path("/run/openbao")',
    ),
    "pods/portal/homepage/config/services.yaml": (
        "username: __HOMEPAGE_GRAFANA_USERNAME__",
        "password: __HOMEPAGE_GRAFANA_PASSWORD__",
    ),
    "pods/observability/apprise/apprise-deployment.yaml": (
        "serviceAccountName: apprise",
        "secretProviderClass: apprise-openbao",
    ),
    "pods/gitea/gitea-secret-sync/csi.yaml": (
        "kind: SecretProviderClass",
        "roleName: gitea",
        "secretPath: secret/data/apps/gitea/admin",
        "secretPath: secret/data/apps/gitea/encryption",
        "secretPath: secret/data/apps/gitea/runtime",
        "secretName: gitea-admin-secret",
        "secretName: gitea-encryption",
        "secretName: gitea-runtime",
    ),
    "pods/gitea/gitea-values.yaml": (
        "secretProviderClass: gitea-openbao",
        "secret.reloader.stakater.com/reload: gitea-postgresql",
        "name: GITEA__DATABASE__PASSWD",
        "name: GITEA__security__SECRET_KEY",
        "name: GITEA__security__INTERNAL_TOKEN",
        "name: GITEA__oauth2__JWT_SECRET",
        "JWT_SIGNING_ALGORITHM: HS256",
        "passwordMode: keepUpdated",
        "secretName: gitea-push-mirror",
        "mountPath: /var/run/adaetum/push-mirror",
    ),
    "pods/gitea/gitea-values.yaml.tmpl": (
        "secretProviderClass: gitea-openbao",
        "secret.reloader.stakater.com/reload: gitea-postgresql",
        "name: GITEA__DATABASE__PASSWD",
        "name: GITEA__security__SECRET_KEY",
        "name: GITEA__security__INTERNAL_TOKEN",
        "name: GITEA__oauth2__JWT_SECRET",
        "JWT_SIGNING_ALGORITHM: HS256",
        "passwordMode: keepUpdated",
        "secretName: gitea-push-mirror",
        "mountPath: /var/run/adaetum/push-mirror",
    ),
    "pods/authentik/authentik-secret-sync/external-secret.yaml": (
        "kind: ExternalSecret",
        "key: apps/authentik/postgresql",
        "refreshInterval: 1m",
    ),
    "pods/authentik/authentik-secret-sync/csi.yaml": (
        "kind: SecretProviderClass",
        "roleName: authentik",
        "secretPath: secret/data/apps/authentik/encryption",
        "secretPath: secret/data/apps/authentik/admin",
        "secretName: authentik-encryption",
        "secretName: authentik-admin",
    ),
    "pods/authentik/authentik.app.yaml": (
        'source_target_revision: "2026.5.5"',
        "serviceAccountName: authentik-csi",
        "secretProviderClass: authentik-openbao",
        "name: authentik-encryption",
        "name: authentik-postgresql",
        "name: authentik-admin",
        "user.set_password(password)",
    ),
    "pods/authentik/authentik-secret-sync/postgresql-rotation.yaml": (
        "name: authentik-postgresql-rotation",
        'resourceNames: ["authentik-postgresql"]',
        "ALTER ROLE postgres PASSWORD",
        "ALTER ROLE authentik PASSWORD",
        "kubectl -n authentik patch secret authentik-postgresql",
    ),
    "pods/argocd/secret-sync/external-secrets.yaml": (
        "name: argocd-repo-https",
        "name: argocd-repository-bootstrap",
        "key: apps/argocd/repository",
    ),
    "pods/argocd/secret-sync/admin-external-secret.yaml": (
        "kind: ExternalSecret",
        "name: argocd-admin-desired",
        "key: apps/argocd/admin",
        "name: argocd-runtime",
        "key: apps/argocd/runtime",
        "secretKey: server.secretkey",
        "name: argocd-redis",
        "secretKey: auth",
        "property: redis_password",
        "creationPolicy: Merge",
    ),
    "pods/argocd/secret-sync/admin-rotation.yaml": (
        "name: argocd-admin-rotation",
        "resourceNames: [\"argocd-secret\"]",
        "htpasswd -niBC 10 admin",
        "admin.passwordMtime",
        "adaetum.io/admin-password-sha256",
    ),
    "pods/observability/grafana-secret-sync/external-secret.yaml": (
        "kind: ExternalSecret",
        "key: apps/observability/grafana",
        "name: grafana-admin",
        "secretKey: admin-password",
        "refreshInterval: 1m",
    ),
    "pods/observability/grafana-secret-sync/homepage-external-secret.yaml": (
        "kind: ExternalSecret",
        "name: homepage-grafana-desired",
        "namespace: observability",
        "key: apps/homepage/grafana",
        "refreshInterval: 1m",
    ),
    "pods/observability/grafana-secret-sync/homepage-viewer-rotation.yaml": (
        "name: grafana-viewer-rotation",
        'resourceNames: ["homepage-grafana"]',
        'namespace: observability',
        'namespace: homepage',
        '"${grafana_url}/api/admin/users"',
        '"${grafana_url}/api/admin/users/${user_id}/password"',
        '"${grafana_url}/api/orgs/1/users/${user_id}"',
        "'{\"role\":\"Viewer\"}'",
        "authenticate /var/run/desired/username /var/run/desired/password",
        "patch secret homepage-grafana",
    ),
    "pods/observability/grafana.app.yaml": (
        'source_target_revision: "10.5.15"',
        "secretProviderClass: grafana-openbao",
        'cat /run/openbao/secret-key',
        'admin reset-admin-password "${GF_SECURITY_ADMIN_PASSWORD}"',
        "exec /run.sh",
        "type: Recreate",
        "storageClassName: longhorn",
    ),
    "pods/gitea/gitea-secret-sync/runner-external-secret.yaml": (
        "kind: ExternalSecret",
        "key: apps/gitea/actions-runner",
    ),
    "pods/gitea/gitea-secret-sync/push-mirror-external-secret.yaml": (
        "kind: ExternalSecret",
        "name: gitea-push-mirror",
        "key: apps/gitea/push-mirror",
        "refreshInterval: 1m",
        "property: remote_url",
        "property: username",
        "property: token",
    ),
    "pods/gitea/gitea-secret-sync/postgresql-external-secret.yaml": (
        "kind: ExternalSecret",
        "key: apps/gitea/postgresql",
        "name: gitea-postgresql-desired",
    ),
    "pods/gitea/gitea-secret-sync/postgresql-rotation.yaml": (
        "name: gitea-postgresql-rotation",
        'resourceNames: ["gitea-postgresql"]',
        "ALTER ROLE postgres PASSWORD",
        "ALTER ROLE gitea PASSWORD",
        "kubectl -n gitea patch secret gitea-postgresql",
    ),
    "pods/ansible/ansible/registry-external-secret.yaml": (
        "kind: ExternalSecret",
        "key: apps/gitea/registry",
        "type: kubernetes.io/dockerconfigjson",
    ),
    "pods/secrets/openbao-sync/store.yaml": (
        "kind: ClusterSecretStore",
        "role: external-secrets",
        "namespace: external-secrets",
        "- openbao",
    ),
    "pods/secrets/openbao/policies/external-secrets.hcl": (
        'path "secret/data/apps/*"',
        'capabilities = ["read"]',
    ),
    "pods/secrets/openbao/policies/config.hcl": (
        'path "auth/kubernetes/role/*"',
        'path "sys/policies/acl/*"',
    ),
    "pods/secrets/rancher-secret-sync/external-secret.yaml": (
        "kind: ExternalSecret",
        "key: apps/rancher/admin",
        "name: rancher-admin-desired",
    ),
    "pods/secrets/rancher-secret-sync/rotation.yaml": (
        "name: rancher-admin-rotation",
        'kind: "PasswordChangeRequest"',
        "currentPassword: $current_password",
        "newPassword: $new_password",
        "patch secret bootstrap-secret",
        "login_with_password /var/run/desired/password",
    ),
    "pods/secrets/openbao/config/job.yaml": (
        "role=openbao-config",
        "optional: true",
        "token_policies=external-secrets",
        "token_policies=openbao-config",
    ),
    "ansible/ansible-scripts/bootstrap/Phase-90/run-phase99.sh": (
        "discover_openbao_leaf_paths secret/apps",
        "Refusing to burn local secrets without a complete workload-secret export.",
        '"${discovered_app_paths[@]}"',
        "app_export_failed",
        '"${BOOTSTRAP_RUNTIME_ENV_FILE:-/etc/bootstrap-runtime.env}"',
        "/etc/ansible-bundle-bootstrap.env",
        "/etc/tailscale-firstboot.env",
        'Failed to remove first-boot credential file:',
    ),
    "ansible/ansible-scripts/bundle-bootstrap": (
        "remove_first_boot_env_files()",
        '"${BOOTSTRAP_RUNTIME_ENV_FILE:-/etc/bootstrap-runtime.env}"',
        "/etc/ansible-bundle-bootstrap.env",
        "/etc/tailscale-firstboot.env",
        "failed to remove first-boot credential file:",
        "remove_first_boot_env_files\nbootstrap_phase_status_write",
    ),
    "ks-src/fragments/shared/portable/12-tailscale-firstboot-flow.shfrag": (
        "remove_installed_bootstrap_artifacts()",
        "/etc/tailscale-firstboot.env",
        "/usr/local/sbin/ansible-bundle-bootstrap.sh",
        "/usr/local/sbin/tailscale-firstboot.sh",
        "/root/*-ks.cfg",
        "remove_installed_bootstrap_artifacts\ntouch \"${done_file}\"",
    ),
    "ansible/ansible-scripts/bootstrap/Phase-40/run-phase40.sh": (
        "seed_openbao_app_fields()",
        "seed_openbao_app_fields argocd/runtime",
        '"redis_password=${argocd_redis_password_val}"',
        "seed_openbao_app_fields gitea/encryption",
        "seed_openbao_app_fields gitea/runtime",
        "seed_openbao_app_fields gitea/push-mirror",
        "seed_openbao_app_fields authentik/encryption",
        "seed_openbao_app_fields authentik/postgresql",
        "seed_openbao_app_fields authentik/admin",
        "seed_openbao_app_fields homepage/grafana",
    ),
    "ansible/ansible-scripts/bootstrap/Phase-50/run-phase50.sh": (
        "ARGOCD_SERVER_SECRET_KEY",
        "read_openbao_app_field argocd/runtime server_secret_key",
        "ARGOCD_REDIS_PASSWORD",
        "read_openbao_app_field argocd/runtime redis_password",
        "argocd-repository-bootstrap argocd-repository-bootstrap argocd",
        "seed_openbao_app_fields authentik/encryption",
        "seed_openbao_app_fields authentik/postgresql",
        "authentik-postgresql-desired",
        "read_openbao_app_field authentik/admin bootstrap_password",
        "read_openbao_app_field gitea/actions-runner token",
        "seed_openbao_app_fields gitea/actions-runner",
        "bootstrap_wait_for_external_secret_delivery",
        "secret.reloader.stakater.com/reload: gitea-actions-runner",
        'configure_gitea_push_mirror_from_openbao "phase50" "$@"',
    ),
    "ansible/ansible-scripts/bootstrap/Phase-60/run-phase60.sh": (
        "ARGOCD_SERVER_SECRET_KEY",
        "read_openbao_app_field argocd/runtime server_secret_key",
        "ARGOCD_REDIS_PASSWORD",
        "read_openbao_app_field argocd/runtime redis_password",
        "argocd-repository-bootstrap argocd-repository-bootstrap argocd",
        "seed_openbao_app_fields authentik/encryption",
        "seed_openbao_app_fields authentik/postgresql",
        "authentik-postgresql-desired",
        "read_openbao_app_field authentik/admin bootstrap_password",
        "read_openbao_app_field gitea/actions-runner token",
        "secret.reloader.stakater.com/reload: gitea-actions-runner",
        'configure_gitea_push_mirror_from_openbao "phase60" "$@"',
    ),
    "ansible/ansible-scripts/bootstrap/Phase-70/run-phase70.sh": (
        "read_openbao_app_field homepage/widgets HOMEPAGE_ARGOCD_WIDGET_KEY",
        "read_openbao_app_field homepage/widgets HOMEPAGE_GITEA_WIDGET_AUTH",
        '--scopes "read:notification,read:repository,read:issue"',
        "gitea_widget_token_has_required_scopes",
        "revoke_stale_gitea_widget_tokens",
        "seed_openbao_app_fields gitea/registry",
        "gitea-registry-creds gitea-registry-creds ansible-runner",
    ),
    "ansible/ansible-scripts/bootstrap/Phase-90/run-phase90.sh": (
        "read_openbao_app_field homepage/widgets HOMEPAGE_ARGOCD_WIDGET_KEY",
        "read_openbao_app_field homepage/widgets HOMEPAGE_GITEA_WIDGET_AUTH",
        '--scopes "read:notification,read:repository,read:issue"',
        "gitea_widget_token_has_required_scopes",
        "revoke_stale_gitea_widget_tokens",
    ),
    "ansible/ansible-scripts/bootstrap/control-pair-common.sh": (
        "seed_openbao_app_fields()",
        'bao kv patch "secret/apps/${path}"',
        'bao kv put "secret/apps/${path}"',
        "gitea_widget_token_has_required_scopes()",
        "revoke_stale_gitea_widget_tokens()",
        '"read:notification", "read:repository", "read:issue"',
        'name.startswith("homepage-widget")',
        'tokens/${token_id}',
        "configure_gitea_push_mirror_from_openbao()",
        "read_openbao_app_field gitea/push-mirror token",
        "seed_openbao_app_fields gitea/push-mirror",
        "bootstrap_wait_for_external_secret_delivery",
        "credential_dir=/var/run/adaetum/push-mirror",
        "Remove credentials written by the legacy hook implementation",
    ),
    "ansible/playbooks/platform-bootstrap.yml": (
        'rancher_chart_version: "2.14.3"',
        'argocd_chart_version: "10.1.4"',
        "--version {{ rancher_chart_version }}",
        "create secret generic authentik-encryption",
        "create secret generic authentik-postgresql",
        "create secret generic gitea-encryption",
        "create secret generic gitea-runtime",
        "grafana_secret_key",
        "argocd_server_secret_key",
        "argocd_redis_password",
        "homepage_grafana_password",
        "redisSecretInit.enabled=false",
        "global.deploymentAnnotations.secret",
        "global.statefulsetAnnotations.secret",
    ),
    "ansible/ansible-scripts/bootstrap/Phase-20/run-phase20.sh": (
        'write_secret "argocd_server_secret_key" 48',
        'write_secret "argocd_redis_password" 24',
        'write_secret "gitea_secret_key" 48',
        'write_secret "gitea_internal_token" 48',
        'write_secret "gitea_jwt_secret" 48',
        'write_secret "grafana_secret_key" 48',
        'write_secret "homepage_grafana_password" 24',
    ),
    "ansible/automation-roles/argocd-install/templates/argocd-values.yaml.j2": (
        "server.secretkey",
        "deploymentAnnotations:",
        "statefulsetAnnotations:",
        "secret.reloader.stakater.com/reload: argocd-redis",
        "secret.reloader.stakater.com/reload: argocd-secret,argocd-redis",
        "redisSecretInit:",
        "enabled: false",
    ),
    "ansible/automation-roles/argocd-install/defaults/main.yml": (
        'argocd_chart_version: "10.1.4"',
        "ARGOCD_REDIS_PASSWORD",
    ),
    "ansible/automation-roles/argocd-install/tasks/main.yml": (
        "Require the OpenBao-bound Redis bootstrap password",
        "create secret generic argocd-redis",
        'ARGOCD_REDIS_PASSWORD: "{{ argocd_redis_password }}"',
    ),
}

# These paths run continuously after bootstrap. Reintroducing the installer
# playbook or its local .env would let stale bootstrap copies overwrite the
# OpenBao-owned steady state.
FORBIDDEN_ROTATION_FRAGMENTS = {
    "ansible/Dockerfile": ("playbooks/bootstrap.yml",),
    "ansible/ansible-scripts/cron-entrypoint": ("playbooks/bootstrap.yml",),
    "ansible/ansible-scripts/run-ansible": (
        "playbooks/bootstrap.yml",
        "source /ansible/ansible/.env",
        "source \"/ansible/ansible/.env\"",
    ),
    "pods/ansible/ansible/ansible-runner-deployment.yaml": ("playbooks/bootstrap.yml",),
    "pods/ansible/ansible/ansible-runner-deployment.yaml.tmpl": ("playbooks/bootstrap.yml",),
    # Homepage exposes Cloudflare and Tailscale as links, not authenticated API
    # widgets. Provider setup credentials must never be copied into its delivery
    # Secret by an early bootstrap or late reconciliation phase.
    "pods/portal/homepage/external-secret.yaml": (
        "HOMEPAGE_AUTHENTIK_",
        "HOMEPAGE_CLOUDFLARE_",
        "HOMEPAGE_TAILSCALE_",
        "HOMEPAGE_GRAFANA_ADMIN_PASSWORD",
    ),
    "ansible/ansible-scripts/bootstrap/Phase-50/run-phase50.sh": (
        "HOMEPAGE_AUTHENTIK_",
        "HOMEPAGE_CLOUDFLARE_",
        "HOMEPAGE_TAILSCALE_",
        "HOMEPAGE_GRAFANA_ADMIN_PASSWORD",
    ),
    "ansible/ansible-scripts/bootstrap/Phase-60/run-phase60.sh": (
        "HOMEPAGE_AUTHENTIK_",
        "HOMEPAGE_CLOUDFLARE_",
        "HOMEPAGE_TAILSCALE_",
        "HOMEPAGE_GRAFANA_ADMIN_PASSWORD",
    ),
    "ansible/ansible-scripts/bootstrap/Phase-70/run-phase70.sh": (
        "HOMEPAGE_AUTHENTIK_",
        "HOMEPAGE_CLOUDFLARE_",
        "HOMEPAGE_TAILSCALE_",
        "HOMEPAGE_GRAFANA_ADMIN_PASSWORD",
        '--scopes "all"',
    ),
    "ansible/ansible-scripts/bootstrap/Phase-90/run-phase90.sh": (
        "HOMEPAGE_AUTHENTIK_",
        "HOMEPAGE_CLOUDFLARE_",
        "HOMEPAGE_TAILSCALE_",
        "HOMEPAGE_GRAFANA_ADMIN_PASSWORD",
        '--scopes "all"',
    ),
    "ansible/ansible-scripts/bootstrap/Phase-40/run-phase40.sh": (
        "bao kv put secret/apps/",
    ),
}

# Ansible normally prints task arguments, registered results, and changed
# values. These named tasks cross a credential boundary, so suppressing their
# output is part of the secret-delivery contract rather than a presentation
# preference. Keeping the inventory explicit makes the owning task obvious
# when the validator fails.
NO_LOG_TASK_CONTRACTS = {
    "ansible/playbooks/platform-bootstrap.yml": (
        "Load bootstrap secrets",
        "Read shared rke2 token from primary host",
        "Use primary rke2 token on every host",
        "Write rke2 config (server)",
        "Write rke2 config (joining server)",
        "Write rke2 config (agent)",
        "Create Argo CD admin password hash",
    ),
    "ansible/automation-roles/argocd-install/tasks/main.yml": (
        "Build Argo CD admin password bcrypt hash",
        "Resolve effective Argo CD admin password hash",
    ),
    "ansible/automation-roles/tailscale-retag/tasks/main.yml": (
        "Create Tailscale auth key for retag (curl)",
        "Normalize auth key response (curl)",
        "Capture Tailscale retag authkey",
    ),
}

# Provider success bodies can contain newly issued credentials, while proxy
# URLs can contain embedded userinfo. Diagnostics may retain status and request
# metadata but never these raw values.
FORBIDDEN_SECRET_LOG_FRAGMENTS = {
    "ansible/automation-roles/tailscale-retag/tasks/main.yml": (
        "body={{ tailscale_authkey_response.content",
        "stderr={{ tailscale_authkey_response.stderr",
        "stdout={{ tailscale_authkey_response.stdout",
        "default(tailscale_authkey_response.content",
        "http_proxy={{ ansible_env.http_proxy",
        "https_proxy={{ ansible_env.https_proxy",
    ),
}


def validate_post_handoff_secret_writers() -> list[str]:
    """Keep Phase 50+ from recreating workload credentials from bootstrap.

    The normal native Secret path in these phases is Headlamp's Kubernetes-owned
    service-account token request. Three named structural bridges are permitted
    only before their ESO resources exist, breaking the initial GitOps/ESO
    cycle; every later run waits for CSI or the explicitly allowed ESO adapter.
    """
    failures: list[str] = []
    bootstrap_bridge_names = {
        "argocd-repo-https",
        "argocd-repository-bootstrap",
        "gitea-actions-runner",
        "gitea-registry-creds",
    }
    for phase, script_name in (("50", "run-phase50.sh"), ("60", "run-phase60.sh"), ("70", "run-phase70.sh"), ("90", "run-phase90.sh")):
        relative_path = f"ansible/ansible-scripts/bootstrap/Phase-{phase}/{script_name}"
        path = REPO_ROOT / relative_path
        text = path.read_text(encoding="utf-8")
        has_bootstrap_bridge = "BOOTSTRAP-ONLY structural bridge" in text
        if "create secret generic" in text and not (
            has_bootstrap_bridge and "create secret generic gitea-actions-runner" in text
        ):
            failures.append(f"{relative_path}: post-handoff direct Secret creation is forbidden")
        if "create secret docker-registry" in text and not (
            has_bootstrap_bridge and "gitea-registry-creds" in text
        ):
            failures.append(f"{relative_path}: post-handoff direct image-pull Secret creation is forbidden")
        secret_manifests = re.findall(
            r"apiVersion:\s*v1\s*\nkind:\s*Secret\b(?P<body>.*?)(?=\n---|\napiVersion:|\Z)",
            text,
            flags=re.DOTALL,
        )
        for manifest in secret_manifests:
            secret_names = set(re.findall(r"(?m)^metadata:\s*\n\s+name:\s*([^\s]+)\s*$", manifest))
            is_bootstrap_bridge = has_bootstrap_bridge and secret_names and secret_names <= bootstrap_bridge_names
            if "type: kubernetes.io/service-account-token" not in manifest and not is_bootstrap_bridge:
                failures.append(
                    f"{relative_path}: post-handoff native Secret is not a Kubernetes-owned service-account token"
                )
        if re.search(r'"kind"\s*:\s*"Secret"', text):
            failures.append(f"{relative_path}: post-handoff JSON Secret creation is forbidden")
    return failures

# Kubernetes itself owns this long-lived service-account token. All committed
# application credential manifests must instead be ExternalSecrets backed by
# OpenBao; adding another static Secret here is an ownership regression.
ALLOWED_SECRET_MANIFESTS = {
    "pods/portal/homepage/secret.yaml": "type: kubernetes.io/service-account-token",
}

# These delivery Secrets are deliberately not direct ExternalSecret targets.
# Each has a named controller/coordinator because copying a desired value into
# the active Secret without first changing product state would break access.
# Keep this list small: an entry is an exception to the normal OpenBao -> ESO
# ownership path and therefore needs an operational reason.
COORDINATED_SECRET_REFERENCES = {
    "authentik-postgresql": "database-first Authentik PostgreSQL rotation",
    "bootstrap-secret": "Rancher-native administrator password rotation",
    "gitea-postgresql": "database-first Gitea PostgreSQL rotation",
    "homepage-grafana": "Grafana Viewer identity promotion",
    "openbao-bootstrap-token": "temporary Phase 40 OpenBao configuration credential",
}

ALLOWED_VALUES = {
    "",
    "change-me",
    "CHANGEME",
    "example",
    "example.local",
    "example.services",
    "example.ts.net",
    "REDACTED",
    "[redacted]",
    "your-token-here",
    "your-secret-here",
}

KEY_ASSIGNMENT_RE = re.compile(
    r"(?i)\b([A-Z0-9_]*(?:token|password|authkey|client_secret|private[_-]?key|secret_key)[A-Z0-9_]*)\b\s*[:=]\s*[\"']?([^\"'\s#]+)"
)

BEARER_RE = re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._=-]{20,}\b")
GITHUB_PAT_RE = re.compile(r"\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b")
PRIVATE_KEY_RE = re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")
BASE64_BLOB_RE = re.compile(r"\b[A-Za-z0-9+/]{80,}={0,2}\b")


def iter_files() -> list[Path]:
    files: list[Path] = []
    for path in SCAN_PATHS:
        if path.is_file():
            files.append(path)
            continue
        files.extend(
            candidate
            for candidate in path.rglob("*")
            if candidate.is_file() and candidate.suffix.lower() in {".yaml", ".yml", ".env", ".md", ".tmpl"}
        )
    return sorted(files)


def is_placeholder(value: str) -> bool:
    lowered = value.strip().lower()
    if lowered in {entry.lower() for entry in ALLOWED_VALUES}:
        return True
    if lowered.startswith("example.") or lowered.startswith("your-") or lowered.startswith("$"):
        return True
    if value.strip().startswith("__") and value.strip().endswith("__"):
        return True
    if value.strip().startswith("{{"):
        return True
    # Shell's `${NAME:-}` default-value form is split by the assignment
    # heuristic at the colon. The captured suffix is syntax, not a value.
    if value.strip() == "-}":
        return True
    if lowered.startswith("<") and lowered.endswith(">"):
        return True
    if lowered.startswith("`") and lowered.endswith("`"):
        return True
    if lowered.startswith("$("):
        return True
    if value.strip().startswith(("os.environ[", "sys.argv[")):
        return True
    if lowered in {"true", "false"}:
        return True
    return False


def validate_file(path: Path) -> list[str]:
    failures: list[str] = []
    rel = path.relative_to(REPO_ROOT)
    text = path.read_text(encoding="utf-8")

    if PRIVATE_KEY_RE.search(text):
        failures.append(f"{rel}: contains private key material marker")
    if GITHUB_PAT_RE.search(text):
        failures.append(f"{rel}: contains GitHub token-like value")
    if BEARER_RE.search(text):
        failures.append(f"{rel}: contains bearer token-like value")

    for lineno, line in enumerate(text.splitlines(), start=1):
        if "secretKeyRef" in line or "valueFrom:" in line:
            continue
        assignment = KEY_ASSIGNMENT_RE.search(line)
        if assignment:
            key, value = assignment.groups()
            # psql's :'name' form safely quotes a runtime variable as an SQL
            # literal; the identifier is not a committed password value.
            if "ALTER ROLE" in line and "PASSWORD :'" in line:
                continue
            if key.lower().endswith(("passwordmode", "passwordkey", "secretkey")) or key.lower() == "token_policies":
                continue
            if not is_placeholder(value):
                failures.append(f"{rel}:{lineno}: suspicious secret-like assignment value: {value}")

        if BASE64_BLOB_RE.search(line) and not is_placeholder(line.strip()):
            failures.append(f"{rel}:{lineno}: contains unusually long base64-like blob")

    return failures


def validate_rotation_contracts() -> list[str]:
    """Protect the continuous OpenBao sync boundary from partial deletion."""
    failures: list[str] = []
    for relative_path, required_fragments in ROTATION_CONTRACTS.items():
        path = REPO_ROOT / relative_path
        if not path.is_file():
            failures.append(f"{relative_path}: required OpenBao rotation contract file is missing")
            continue
        text = path.read_text(encoding="utf-8")
        for fragment in required_fragments:
            if fragment not in text:
                failures.append(f"{relative_path}: missing rotation contract fragment: {fragment}")
    for relative_path, forbidden_fragments in FORBIDDEN_ROTATION_FRAGMENTS.items():
        path = REPO_ROOT / relative_path
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8")
        for fragment in forbidden_fragments:
            if fragment in text:
                failures.append(f"{relative_path}: recurring automation contains bootstrap authority: {fragment}")
    return failures


def validate_ansible_secret_logging() -> list[str]:
    """Require redaction exactly where Ansible handles secret values."""
    failures: list[str] = []

    def named_tasks(value: object) -> list[dict[str, object]]:
        tasks: list[dict[str, object]] = []
        if isinstance(value, dict):
            if isinstance(value.get("name"), str):
                tasks.append(value)
            for child in value.values():
                tasks.extend(named_tasks(child))
        elif isinstance(value, list):
            for child in value:
                tasks.extend(named_tasks(child))
        return tasks

    for relative_path, required_names in NO_LOG_TASK_CONTRACTS.items():
        path = REPO_ROOT / relative_path
        if not path.is_file():
            failures.append(f"{relative_path}: secret-bearing Ansible file is missing")
            continue
        try:
            documents = list(yaml.safe_load_all(path.read_text(encoding="utf-8")))
        except yaml.YAMLError as exc:
            failures.append(f"{relative_path}: cannot validate Ansible no_log contracts: {exc}")
            continue
        tasks = [task for document in documents for task in named_tasks(document)]
        for required_name in required_names:
            matches = [task for task in tasks if task.get("name") == required_name]
            if not matches:
                failures.append(f"{relative_path}: secret-bearing task is missing: {required_name}")
            elif any(task.get("no_log") is not True for task in matches):
                failures.append(f"{relative_path}: secret-bearing task must set no_log: true: {required_name}")

    for relative_path, forbidden_fragments in FORBIDDEN_SECRET_LOG_FRAGMENTS.items():
        path = REPO_ROOT / relative_path
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8")
        for fragment in forbidden_fragments:
            if fragment in text:
                failures.append(f"{relative_path}: provider diagnostic can expose a secret: {fragment}")
    return failures


def validate_committed_secret_manifests() -> list[str]:
    """Reject application credentials committed as ordinary Kubernetes Secrets."""
    failures: list[str] = []
    pods_root = REPO_ROOT / "pods"
    for path in sorted(pods_root.rglob("*.yaml")):
        text = path.read_text(encoding="utf-8")
        if not re.search(r"(?m)^kind: Secret\s*$", text):
            continue
        relative_path = str(path.relative_to(REPO_ROOT))
        required_type = ALLOWED_SECRET_MANIFESTS.get(relative_path)
        if required_type is None:
            failures.append(
                f"{relative_path}: committed Kubernetes Secret is not an approved controller-owned identity"
            )
        elif required_type not in text:
            failures.append(f"{relative_path}: approved Secret is missing ownership marker: {required_type}")
    return failures


def validate_secret_reference_ownership() -> list[str]:
    """Require every native workload Secret reference to have a named owner.

    This complements the known-file rotation contracts above. Those contracts
    protect the implementation details of current integrations; this inventory
    catches a newly added secretKeyRef, envFrom Secret, or Secret volume even
    when nobody remembered to add it to that known-file list.
    """
    failures: list[str] = []
    external_secret_targets: set[str] = set()
    csi_secret_targets: set[str] = set()
    secret_references: list[tuple[str, str]] = []

    def walk(value: object, relative_path: str, path: tuple[str, ...] = ()) -> None:
        if isinstance(value, dict):
            for reference_key in ("secretKeyRef", "secretRef"):
                reference = value.get(reference_key)
                if isinstance(reference, dict) and isinstance(reference.get("name"), str):
                    secret_references.append((relative_path, reference["name"]))

            volume_secret = value.get("secret")
            if isinstance(volume_secret, dict):
                secret_name = volume_secret.get("secretName")
                if isinstance(secret_name, str):
                    secret_references.append((relative_path, secret_name))
                # Projected volume sources use `secret.name` rather than
                # `secret.secretName`.
                projected_name = volume_secret.get("name")
                if "sources" in path and isinstance(projected_name, str):
                    secret_references.append((relative_path, projected_name))

            image_pull_secrets = value.get("imagePullSecrets")
            if isinstance(image_pull_secrets, list):
                for reference in image_pull_secrets:
                    if isinstance(reference, dict) and isinstance(reference.get("name"), str):
                        secret_references.append((relative_path, reference["name"]))

            for key, child in value.items():
                walk(child, relative_path, path + (str(key),))
        elif isinstance(value, list):
            for index, child in enumerate(value):
                walk(child, relative_path, path + (str(index),))

    for path in sorted((REPO_ROOT / "pods").rglob("*.yaml")):
        relative_path = str(path.relative_to(REPO_ROOT))
        try:
            documents = list(yaml.safe_load_all(path.read_text(encoding="utf-8")))
        except yaml.YAMLError as exc:
            failures.append(f"{relative_path}: cannot inventory Secret ownership: {exc}")
            continue

        for document in documents:
            if not isinstance(document, dict):
                continue
            if document.get("kind") == "ExternalSecret":
                metadata = document.get("metadata") or {}
                spec = document.get("spec") or {}
                target = spec.get("target") or {}
                target_name = target.get("name") or metadata.get("name")
                if isinstance(target_name, str):
                    external_secret_targets.add(target_name)
            if document.get("kind") == "SecretProviderClass":
                spec = document.get("spec") or {}
                for secret_object in spec.get("secretObjects") or []:
                    if isinstance(secret_object, dict) and isinstance(secret_object.get("secretName"), str):
                        csi_secret_targets.add(secret_object["secretName"])
            walk(document, relative_path)

    approved_names = external_secret_targets | csi_secret_targets | set(COORDINATED_SECRET_REFERENCES)
    for relative_path, secret_name in sorted(set(secret_references)):
        if secret_name not in approved_names:
            failures.append(
                f"{relative_path}: Secret reference {secret_name!r} has no ExternalSecret or CSI target "
                "or approved product-aware owner"
            )
    return failures


def validate_csi_workload_identities() -> list[str]:
    """Prove each CSI workload role is narrow and configured in OpenBao.

    A SecretProviderClass names the pod identity that asks OpenBao for data;
    this check keeps that relationship one-to-one with a read-only policy and
    catches a new CSI consumer that accidentally inherits a broad policy.
    """
    failures: list[str] = []
    roles: dict[str, str] = {}
    config_job = (REPO_ROOT / "pods/secrets/openbao/config/job.yaml").read_text(encoding="utf-8")

    for path in sorted((REPO_ROOT / "pods").rglob("*.yaml")):
        relative_path = str(path.relative_to(REPO_ROOT))
        try:
            documents = list(yaml.safe_load_all(path.read_text(encoding="utf-8")))
        except yaml.YAMLError:
            continue
        for document in documents:
            if not isinstance(document, dict) or document.get("kind") != "SecretProviderClass":
                continue
            spec = document.get("spec") or {}
            if spec.get("provider") != "openbao":
                continue
            parameters = spec.get("parameters") or {}
            role = parameters.get("roleName")
            objects = parameters.get("objects")
            if not isinstance(role, str) or not role:
                failures.append(f"{relative_path}: OpenBao CSI class is missing parameters.roleName")
                continue
            if role in roles:
                failures.append(f"{relative_path}: OpenBao CSI role {role!r} is already used by {roles[role]}")
            else:
                roles[role] = relative_path
            paths = set(re.findall(r"(?m)^\s*secretPath:\s*(secret/data/apps/[^\s]+)\s*$", objects or ""))
            if not paths:
                failures.append(f"{relative_path}: OpenBao CSI class has no application secretPath")
                continue
            policy_path = REPO_ROOT / "pods/secrets/openbao/policies" / f"{role}.hcl"
            if not policy_path.is_file():
                failures.append(f"{relative_path}: missing policy for CSI role {role!r}")
                continue
            policy = policy_path.read_text(encoding="utf-8")
            policy_paths = set(re.findall(r'path\s+"(secret/data/apps/[^"]+)"', policy))
            if paths != policy_paths:
                failures.append(
                    f"{relative_path}: CSI paths {sorted(paths)!r} do not exactly match {policy_path.relative_to(REPO_ROOT)}"
                )
            if 'capabilities = ["read"]' not in policy or "*" in policy:
                failures.append(f"{policy_path.relative_to(REPO_ROOT)}: CSI policy must be read-only and non-wildcard")
            role_command = f"auth/kubernetes/role/{role}"
            if role_command not in config_job or f"token_policies={role}" not in config_job:
                failures.append(f"{relative_path}: OpenBao config job does not configure CSI role {role!r}")
    return failures


def main() -> int:
    failures: list[str] = []
    openbao_csi_app = (
        REPO_ROOT / "pods/secrets/openbao-csi-provider.app.yaml"
    ).read_text(encoding="utf-8")
    csi_driver_app = (
        REPO_ROOT / "pods/secrets/csi-secrets-store.app.yaml"
    ).read_text(encoding="utf-8")
    provider_socket_dir = "providersDir: /var/run/secrets-store-csi-providers"
    if provider_socket_dir not in csi_driver_app:
        failures.append(
            "pods/secrets/csi-secrets-store.app.yaml: "
            "CSI driver provider socket directory contract is missing"
        )
    for required in (
        provider_socket_dir,
        "securityContext:",
        "runAsUser: 0",
        "runAsNonRoot: false",
    ):
        if required not in openbao_csi_app:
            failures.append(
                "pods/secrets/openbao-csi-provider.app.yaml: "
                f"CSI provider socket identity contract is missing {required!r}"
            )
    for path in iter_files():
        failures.extend(validate_file(path))
    failures.extend(validate_rotation_contracts())
    failures.extend(validate_ansible_secret_logging())
    failures.extend(validate_post_handoff_secret_writers())
    failures.extend(validate_committed_secret_manifests())
    failures.extend(validate_secret_reference_ownership())
    failures.extend(validate_csi_workload_identities())

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
