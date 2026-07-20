#!/usr/bin/env python3
"""Protect shared bootstrap helper ownership and security-sensitive behavior.

The control-pair library is the single owner for helpers whose implementation is
identical in both phases. Phase-specific helpers may intentionally differ in
mode handling, logging, and handoff policy, so this validator only rejects
byte-for-byte duplicates.
"""

from __future__ import annotations

import re
import shlex
import subprocess
import sys
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
PHASE_40 = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/Phase-40/run-phase40.sh"
PHASE_50 = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/Phase-50/run-phase50.sh"
PHASE_60 = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/Phase-60/run-phase60.sh"
SHARED_HELPERS = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/control-pair-common.sh"
FUNCTION_START = re.compile(
    r"^(?:(?:function)\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\(\)\s*\{", re.MULTILINE
)


def functions(path: Path) -> dict[str, str]:
    """Return shell-function source keyed by name.

    Adaetum's bootstrap functions use balanced braces. This small parser also
    ignores braces inside quoted strings, which is enough for this structural
    repository check without executing any shell code.
    """
    source = path.read_text(encoding="utf-8")
    result: dict[str, str] = {}
    for match in FUNCTION_START.finditer(source):
        depth = 0
        quote: str | None = None
        index = match.end() - 1
        while index < len(source):
            character = source[index]
            line_start = source.rfind("\n", 0, index) + 1
            if (
                quote is None
                and character == "#"
                and source[line_start:index].strip() == ""
            ):
                next_line = source.find("\n", index)
                index = len(source) if next_line == -1 else next_line
                continue
            if quote:
                if character == "\\":
                    index += 1
                elif character == quote:
                    quote = None
            elif character in "'\"":
                quote = character
            elif character == "{":
                depth += 1
            elif character == "}":
                depth -= 1
                if depth == 0:
                    result[match.group(1)] = source[match.start() : index + 1]
                    break
            index += 1
        else:
            raise ValueError(f"{path}: could not find the end of {match.group(1)}")
    return result


def validate_gitea_token_helpers() -> list[str]:
    """Exercise scope rejection and post-promotion stale-token revocation."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

MOCK_MODE=readonly
curl() {{
  if [[ " $* " == *" --request DELETE "* ]]; then
    MOCK_MODE=revoke-after
    return 0
  fi
  case "${{MOCK_MODE}}" in
    readonly|revoke-after)
      printf '%s' '[{{"id":2,"name":"homepage-widget-2","token_last_eight":"abcdefgh","scopes":["read:issue","read:repository","read:notification"]}}]'
      ;;
    broad)
      printf '%s' '[{{"id":1,"name":"homepage-widget","token_last_eight":"abcdefgh","scopes":["all"]}}]'
      ;;
    revoke-before)
      printf '%s' '[{{"id":1,"name":"homepage-widget","token_last_eight":"oldtoken","scopes":["all"]}},{{"id":2,"name":"homepage-widget-2","token_last_eight":"abcdefgh","scopes":["read:issue","read:repository","read:notification"]}}]'
      ;;
  esac
}}

gitea_widget_token_has_required_scopes \
  http://gitea gitea-admin password 0123456789abcdefgh
MOCK_MODE=broad
if gitea_widget_token_has_required_scopes \
  http://gitea gitea-admin password 0123456789abcdefgh; then
  echo 'all-scope token was accepted' >&2
  exit 1
fi
MOCK_MODE=revoke-before
revoke_stale_gitea_widget_tokens \
  http://gitea gitea-admin password 0123456789abcdefgh
[[ "${{MOCK_MODE}}" == revoke-after ]]
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"Gitea token helper behavior failed: {detail}"]


def validate_push_mirror_helper() -> list[str]:
    """Prove OpenBao wins on rerun and the hook reads only its projection."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

openbao_token=bao-token
repo_root={shlex.quote(str(REPOSITORY_ROOT))}
kubectl_bin=mock_kubectl
OPENBAO_NAMESPACE=openbao
OPENBAO_POD=openbao-0
EXEC_VALIDATED=0

read_openbao_app_field() {{
  case "$2" in
    remote_url) printf '%s' 'https://github.com/example/recovery.git' ;;
    username) printf '%s' 'openbao-user' ;;
    token) printf '%s' 'openbao-token-value' ;;
  esac
}}
seed_openbao_app_fields() {{
  echo 'existing OpenBao push-mirror fields were unexpectedly reseeded' >&2
  return 1
}}
repo_url_is_github() {{ return 0; }}
github_token_looks_like_pat() {{ return 0; }}
find_ready_gitea_pod() {{ printf '%s' gitea-0; }}

mock_kubectl() {{
  if [[ " $* " == *" get crd externalsecrets.external-secrets.io "* ]]; then
    return 0
  fi
  if [[ " $* " == *" get deployment external-secrets "* && " $* " == *".status.conditions"* ]]; then
    printf '%s' True
    return 0
  fi
  if [[ " $* " == *" apply -f "*"push-mirror-external-secret.yaml"* ]]; then
    return 0
  fi
  if [[ " $* " == *" exec -i gitea-0 "* ]]; then
    [[ " $* " == *" EXPECTED_REPO_URL=https://github.com/example/recovery.git "* ]]
    [[ " $* " == *" EXPECTED_USERNAME=openbao-user "* ]]
    [[ " $* " != *"openbao-token-value"* ]]
    payload="$(cat)"
    grep -Fq 'credential_dir="/var/run/adaetum/push-mirror"' <<<"${{payload}}"
    grep -Fq 'cat "${{credential_dir}}/token"' <<<"${{payload}}"
    grep -Fq 'rm -f' <<<"${{payload}}"
    grep -Fq '.github-mirror.password' <<<"${{payload}}"
    EXEC_VALIDATED=1
    return 0
  fi
  if [[ " $* " == *" get secret gitea-push-mirror "* ]]; then
    return 0
  fi
  if [[ " $* " == *" get externalsecret gitea-push-mirror "* ]]; then
    if [[ " $* " == *" jsonpath="* ]]; then
      printf 'True\tReady\tOpenBao delivery ready'
    fi
    return 0
  fi
  if [[ " $* " == *" apply -f - "* ]]; then
    cat >/dev/null
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}}

configure_gitea_push_mirror_from_openbao \
  phase-test gitea-admin cluster \
  https://github.com/stale/recovery.git stale-user stale-token
[[ "${{EXEC_VALIDATED}}" == 1 ]]
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"Gitea push-mirror helper behavior failed: {detail}"]


def validate_external_secret_timeout() -> list[str]:
    """Prove a controller that never reports status cannot block boot forever."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

BOOTSTRAP_EXTERNAL_SECRET_TIMEOUT_SECONDS=1
BOOTSTRAP_EXTERNAL_SECRET_POLL_SECONDS=1
DIAGNOSTICS_CAPTURED=0

bootstrap_capture_external_secret_diagnostics() {{
  [[ "$5" == "secret-sync-timeout" ]]
  DIAGNOSTICS_CAPTURED=1
}}

mock_kubectl() {{
  if [[ " $* " == *" get externalsecret stalled "* ]]; then
    return 0
  fi
  if [[ " $* " == *" get secret stalled-target "* ]]; then
    return 1
  fi
  if [[ " $* " == *" get deployment external-secrets "* ]]; then
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}}

if bootstrap_wait_for_external_secret_delivery \
  mock_kubectl ansible stalled stalled-target stalled-component; then
  echo 'ExternalSecret without controller status unexpectedly passed' >&2
  exit 1
fi
[[ "${{DIAGNOSTICS_CAPTURED}}" == 1 ]]
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
        timeout=5,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"ExternalSecret timeout behavior failed: {detail}"]


def validate_external_secret_ready() -> list[str]:
    """Prove a reconciled ExternalSecret is recognized without polling again."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

BOOTSTRAP_EXTERNAL_SECRET_TIMEOUT_SECONDS=1
BOOTSTRAP_EXTERNAL_SECRET_POLL_SECONDS=1

mock_kubectl() {{
  if [[ " $* " == *" get secret ready-target "* ]]; then
    return 0
  fi
  if [[ " $* " == *" get externalsecret ready "* && " $* " == *" -o jsonpath="* ]]; then
    expected_jsonpath='{{range .status.conditions[?(@.type=="Ready")]}}{{.status}}{{"\t"}}{{.reason}}{{"\t"}}{{.message}}{{end}}'
    if [[ " $* " != *"${{expected_jsonpath}}"* ]]; then
      echo "unexpected Ready-condition JSONPath: $*" >&2
      return 1
    fi
    printf 'True\tSecretSynced\tsecret synced'
    return 0
  fi
  if [[ " $* " == *" get externalsecret ready "* ]]; then
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}}

bootstrap_wait_for_external_secret_delivery \
  mock_kubectl ansible ready ready-target ready-component
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
        timeout=5,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"ExternalSecret ready behavior failed: {detail}"]


def validate_csi_secret_timeout() -> list[str]:
    """Prove a missing CSI class cannot block the first boot indefinitely."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

BOOTSTRAP_CSI_SECRET_TIMEOUT_SECONDS=1
BOOTSTRAP_CSI_SECRET_POLL_SECONDS=1
DIAGNOSTICS_CAPTURED=0

bootstrap_capture_secret_delivery_foundation_diagnostics() {{
  [[ "$3" == "provider-class-missing" ]]
  DIAGNOSTICS_CAPTURED=1
}}

mock_kubectl() {{
  if [[ " $* " == *" get secretproviderclass missing-class "* ]]; then
    return 1
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}}

if bootstrap_wait_for_csi_secret_delivery \
  mock_kubectl ansible missing-class stalled-component; then
  echo 'missing SecretProviderClass unexpectedly passed' >&2
  exit 1
fi
[[ "${{DIAGNOSTICS_CAPTURED}}" == 1 ]]
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
        timeout=5,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"CSI secret timeout behavior failed: {detail}"]


def validate_secret_foundation_ready() -> list[str]:
    """Exercise the successful status contract used by Phases 60 and 70."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

mock_kubectl() {{
  if [[ " $* " == *" -n argocd get application "* && " $* " == *".status.sync.status"* ]]; then
    printf '%s' Synced
    return 0
  fi
  if [[ " $* " == *" -n argocd get application "* && " $* " == *".status.health.status"* ]]; then
    printf '%s' Healthy
    return 0
  fi
  if [[ " $* " == *" get crd "* ]]; then
    return 0
  fi
  if [[ " $* " == *" get clustersecretstore openbao "* ]]; then
    printf '%s' True
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}}

bootstrap_wait_for_secret_delivery_foundation mock_kubectl test-foundation
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
        timeout=5,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"Secret-delivery foundation readiness behavior failed: {detail}"]


def validate_phase70_realization_gate() -> list[str]:
    """Keep the post-handoff proof strict, bounded, and non-restarting."""
    phase_60 = PHASE_60.read_text(encoding="utf-8")
    phase_70 = (
        REPOSITORY_ROOT
        / "ansible/ansible-scripts/bootstrap/Phase-70/run-phase70.sh"
    ).read_text(encoding="utf-8")
    failures: list[str] = []

    for required in (
        "require_gitea_service_contract()",
        'get service "${GITEA_INTERNAL_SERVICE_NAME}"',
        'get endpoints "${GITEA_INTERNAL_SERVICE_NAME}"',
        "Gitea service discovery does not resolve to a ready registry endpoint",
        "ensure_gitea_registry_bootstrap_path()",
        "wait_for_gitea_registry_bootstrap_path()",
        "wait_for_gitea_gitops_settle()",
        "discover_gitea_registry_token_service_host",
        '--resolve "${canonical_host}:80:${bootstrap_ip}"',
        '"http://${canonical_host}/v2/"',
        'ensure_gitea_runtime_host_aliases "${registry_token_service_host}"',
        "registry-token-preflight",
        "Kaniko Job is missing the required",
    ):
        if required not in phase_60:
            failures.append(
                f"Phase 60 Gitea service preflight contract missing: {required}"
            )

    realization_start = phase_60.find(
        'echo "[phase60] running realization checks'
    )
    bootstrap_path = phase_60.find(
        '"failed establishing internal Gitea registry bootstrap path"',
        realization_start,
    )
    gitops_settle = phase_60.find(
        '"Gitea GitOps application did not settle after handoff"',
        realization_start,
    )
    golden_path = phase_60.find(
        "require_gitea_golden_path",
        gitops_settle,
    )
    service_contract = phase_60.find(
        '"Gitea service discovery does not resolve to a ready registry endpoint"',
        realization_start,
    )
    image_publish = phase_60.find(
        '"failed publishing ansible-runner image to Gitea registry"',
        realization_start,
    )
    if (
        realization_start < 0
        or gitops_settle < 0
        or golden_path < 0
        or service_contract < 0
        or bootstrap_path < 0
        or image_publish < 0
        or not (
            gitops_settle
            < golden_path
            < service_contract
            < bootstrap_path
            < image_publish
        )
    ):
        failures.append(
            "Phase 60 must wait for the adopted Gitea Application, prove its "
            "rollout and Service, then establish the registry path before "
            "publishing ansible-runner during realization"
        )

    phase_60_functions = functions(PHASE_60)
    gitops_settle_helper = phase_60_functions.get("wait_for_gitea_gitops_settle")
    registry_wait_helper = phase_60_functions.get(
        "wait_for_gitea_registry_bootstrap_path"
    )
    if gitops_settle_helper:
        script = gitops_settle_helper + r'''
set -euo pipefail
kubectl_bin=mock_kubectl
PHASE60_GITEA_GITOPS_SETTLE_TIMEOUT_SECONDS=30
DEBUG_REASON=""
STATE_FILE="$(mktemp)"
printf '%s' 0 >"${STATE_FILE}"

mock_kubectl() {
  local field="${*: -1}"
  local call_count=""
  local observation=""
  local state=""
  call_count="$(cat "${STATE_FILE}")"
  call_count=$((call_count + 1))
  printf '%s' "${call_count}" >"${STATE_FILE}"
  observation=$(((call_count - 1) / 3))
  case "${observation}" in
    0) state="Synced Healthy Succeeded" ;;
    1) state="OutOfSync Progressing Running" ;;
    *) state="Synced Healthy Succeeded" ;;
  esac
  if [[ "${field}" == *"sync.status"* ]]; then
    printf '%s' "${state%% *}"
  elif [[ "${field}" == *"health.status"* ]]; then
    state="${state#* }"
    printf '%s' "${state%% *}"
  elif [[ "${field}" == *"operationState.phase"* ]]; then
    printf '%s' "${state##* }"
  else
    return 1
  fi
}

sleep() { :; }
gitea_debug_dump() { DEBUG_REASON="$1"; }

wait_for_gitea_gitops_settle
[[ "$(cat "${STATE_FILE}")" -ge 12 ]]
[[ -z "${DEBUG_REASON}" ]]
rm -f "${STATE_FILE}"
'''
        result = subprocess.run(
            ["bash", "-c", script],
            cwd=REPOSITORY_ROOT,
            text=True,
            capture_output=True,
            check=False,
            timeout=5,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
            failures.append(f"Gitea GitOps settle behavior failed: {detail}")

    if registry_wait_helper:
        script = registry_wait_helper + r'''
set -euo pipefail
PHASE60_GITEA_REGISTRY_ATTEMPTS=3
PHASE60_GITEA_REGISTRY_RETRY_DELAY=0
ATTEMPTS=0
DEBUG_REASON=""

ensure_gitea_registry_bootstrap_path() {
  ATTEMPTS=$((ATTEMPTS + 1))
  [[ "${ATTEMPTS}" -ge 3 ]]
}
sleep() { :; }
gitea_debug_dump() { DEBUG_REASON="$1"; }

wait_for_gitea_registry_bootstrap_path
[[ "${ATTEMPTS}" == 3 ]]
[[ -z "${DEBUG_REASON}" ]]
'''
        result = subprocess.run(
            ["bash", "-c", script],
            cwd=REPOSITORY_ROOT,
            text=True,
            capture_output=True,
            check=False,
            timeout=5,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
            failures.append(f"Gitea registry convergence behavior failed: {detail}")

    for required in (
        "PHASE60_WARNING_ONLY=0",
        'PHASE60_GITEA_ROLLOUT_TIMEOUT="${PHASE70_GITEA_ROLLOUT_TIMEOUT:-120s}"',
        'PHASE60_GITEA_GITOPS_SETTLE_TIMEOUT_SECONDS="${PHASE70_GITEA_GITOPS_SETTLE_TIMEOUT_SECONDS:-180}"',
        "PHASE60_GITEA_ROLLOUT_ATTEMPTS=1",
        "PHASE60_GITEA_ROLLOUT_RESTART_ON_FAIL=0",
        "GitOps handoff verification failed; stopping before realization checks",
        "verify_secret_delivery_foundation_phase70",
        "secret-delivery foundation failed; stopping before workload realization checks",
        "GitOps realization checks failed; stopping before secret-delivery reconciliation",
        'registry_host="${runner_image%%/*}"',
        "ansible-cluster-config does not declare ANSIBLE_RUNNER_IMAGE",
    ):
        if required not in phase_70:
            failures.append(f"Phase 70 realization gate contract missing: {required}")

    alpha_host_label = "cluster-" + "duck"
    if alpha_host_label in phase_70:
        failures.append("Phase 70 contains an alpha hostname")

    foundation_gate = phase_70.find(
        'run_phase70_step "verify secret-delivery foundation"'
    )
    realization_gate = phase_70.find(
        'run_phase70_step "run GitOps realization checks"'
    )
    if foundation_gate < 0 or realization_gate < 0 or foundation_gate > realization_gate:
        failures.append(
            "Phase 70 must prove the secret-delivery foundation before workload realization"
        )

    service_check = phase_60_functions.get("require_gitea_service_contract")
    if service_check:
        script = service_check + r'''
set -euo pipefail
kubectl_bin=mock_kubectl
GITEA_INTERNAL_SERVICE_NAME=gitea-http
MOCK_STATE=healthy
DEBUG_REASON=""

gitea_debug_dump() {
  DEBUG_REASON="$1"
}

mock_kubectl() {
  if [[ " $* " == *" get service gitea-http "* ]]; then
    if [[ "${MOCK_STATE}" == "missing" ]]; then
      return 1
    fi
    if [[ "$*" == *".spec.clusterIP"* ]]; then
      # The pinned Gitea chart uses a valid headless Service.
      printf '%s' 'None'
    elif [[ "$*" == *".spec.ports"* ]]; then
      printf '%s' '3000 '
    fi
    return 0
  fi
  if [[ " $* " == *" get endpoints gitea-http "* ]]; then
    if [[ "${MOCK_STATE}" == "no-endpoint" ]]; then
      return 0
    fi
    printf '%s' '10.42.148.10 '
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}

require_gitea_service_contract

MOCK_STATE=missing
if require_gitea_service_contract; then
  echo 'missing Gitea Service unexpectedly passed' >&2
  exit 1
fi
[[ "${DEBUG_REASON}" == "service-missing" ]]

MOCK_STATE=no-endpoint
DEBUG_REASON=""
if require_gitea_service_contract; then
  echo 'Gitea Service without a ready endpoint unexpectedly passed' >&2
  exit 1
fi
[[ "${DEBUG_REASON}" == "service-has-no-ready-endpoint" ]]
'''
        result = subprocess.run(
            ["bash", "-c", script],
            cwd=REPOSITORY_ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
            failures.append(f"Gitea service preflight behavior failed: {detail}")

    phase_70_path = (
        REPOSITORY_ROOT
        / "ansible/ansible-scripts/bootstrap/Phase-70/run-phase70.sh"
    )
    phase_70_functions = functions(phase_70_path)
    pull_secret_helper = phase_70_functions.get("ensure_ansible_runner_pull_secret_phase70")
    if pull_secret_helper:
        script = pull_secret_helper + r'''
set -euo pipefail
kubectl_bin=mock_kubectl
SEEDED=0

mock_kubectl() {
  if [[ " $* " == *" get configmap ansible-cluster-config "* ]]; then
    printf '%s' 'registry.mudazukai.cloud/gitea-admin/ansible-runner:latest'
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}

read_phase70_bootstrap_field() {
  case "$1" in
    argocd_repo_username) printf '%s' gitea-admin ;;
    gitea_git_token) printf '%s' registry-token ;;
  esac
}

seed_openbao_app_fields() {
  [[ " $* " == *" host=registry.mudazukai.cloud "* ]]
  [[ " $* " != *"cluster-"*"duck"* ]]
  SEEDED=1
}

bootstrap_wait_for_external_secret_delivery() { return 0; }

ensure_ansible_runner_pull_secret_phase70 openbao-token
[[ "${SEEDED}" == 1 ]]
'''
        result = subprocess.run(
            ["bash", "-c", script],
            cwd=REPOSITORY_ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
            failures.append(f"Phase 70 registry host derivation failed: {detail}")

    return failures


def validate_registry_token_service_discovery() -> list[str]:
    """Prove registry discovery uses canonical identity over the Service bridge."""
    discovery = functions(PHASE_60).get("discover_gitea_registry_token_service_host", "")
    parser_dir = shlex.quote(str(PHASE_60.parent.parent))
    script = rf'''
set -euo pipefail
script_dir={parser_dir}
GITEA_CANONICAL_URL=http://gitea.mudazukai.cloud.local/
GITEA_INTERNAL_SERVICE_HOST=gitea-http.gitea.svc.cluster.local:3000
gitea_service_cluster_ip() {{ printf '%s' '10.43.53.138'; }}
curl() {{
  if [[ " $* " == *" --resolve gitea.mudazukai.cloud.local:80:10.43.53.138 "* ]] && \
     [[ " $* " == *" http://gitea.mudazukai.cloud.local/v2/ "* ]]; then
    printf '%s\r\n' \
      'HTTP/1.1 401 Unauthorized' \
      'Www-Authenticate: Bearer realm="http://gitea.mudazukai.cloud.local/v2/token",service="container_registry"'
  else
    printf '%s\r\n' \
      'HTTP/1.1 401 Unauthorized' \
      'Www-Authenticate: Bearer realm="http://wrong-bootstrap-path.invalid/v2/token",service="container_registry"'
  fi
}}
{discovery}
actual="$(discover_gitea_registry_token_service_host)"
if [[ "${{actual}}" != "gitea.mudazukai.cloud.local" ]]; then
  echo "unexpected discovered host: ${{actual:-<empty>}}" >&2
  exit 1
fi
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"Gitea registry token-service discovery failed: {detail}"]


def validate_secret_foundation_handoff() -> list[str]:
    """Keep the narrowed source-of-truth ApplicationSet ahead of full handoff."""
    phase_60 = PHASE_60.read_text(encoding="utf-8")
    phase_50 = PHASE_50.read_text(encoding="utf-8")
    phase_40 = PHASE_40.read_text(encoding="utf-8")
    failures: list[str] = []
    for required in (
        "applicationset-foundation.yaml",
        "pods/secrets/csi-secrets-store.app.yaml",
        "pods/secrets/openbao-csi-provider.app.yaml",
        "pods/secrets/external-secrets.app.yaml",
        "pods/secrets/openbao-sync.app.yaml",
        "bootstrap_wait_for_secret_delivery_foundation",
        "get applicationset apps",
        "preserving its generator while checking foundation readiness",
    ):
        if required not in phase_60:
            failures.append(f"Phase 60 secret-foundation handoff missing: {required}")
    foundation_apply = phase_60.find(
        'apply_secret_delivery_foundation_bridge "${rendered_dir}/applicationset-foundation.yaml"'
    )
    full_apply = phase_60.find('apply -f "${rendered_dir}/app-of-apps.yaml"')
    if foundation_apply < 0 or full_apply < 0 or foundation_apply > full_apply:
        failures.append(
            "Phase 60 must prove its narrowed ApplicationSet before applying app-of-apps"
        )
    if 'wait_for_argo_application_crd "180s"' in phase_40:
        failures.append(
            "Phase 40 must not wait for the Argo CRD before the phase that installs Argo CD"
        )
    for phase_name, source in (("Phase 50", phase_50), ("Phase 60", phase_60)):
        for required in (
            "get crd externalsecrets.external-secrets.io",
            "deferring admin token reconciliation to Phase 90",
        ):
            if required not in source:
                failures.append(f"{phase_name} clean-install deferral missing: {required}")
    return failures


def main() -> int:
    phase_50_functions = functions(PHASE_50)
    phase_60_functions = functions(PHASE_60)
    duplicates = sorted(
        name
        for name, implementation in phase_50_functions.items()
        if phase_60_functions.get(name) == implementation
    )
    failures = validate_gitea_token_helpers()
    failures.extend(validate_push_mirror_helper())
    failures.extend(validate_external_secret_timeout())
    failures.extend(validate_external_secret_ready())
    failures.extend(validate_csi_secret_timeout())
    failures.extend(validate_secret_foundation_ready())
    failures.extend(validate_phase70_realization_gate())
    failures.extend(validate_registry_token_service_discovery())
    failures.extend(validate_secret_foundation_handoff())
    if duplicates:
        print("Move exact shared helpers into control-pair-common.sh:", file=sys.stderr)
        print("\n".join(f"- {name}" for name in duplicates), file=sys.stderr)
        failures.append("Phase 50/60 contain byte-identical helper implementations")
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print(
        "Control-pair helper ownership ok: "
        f"{len(phase_50_functions)} Phase 50 and {len(phase_60_functions)} Phase 60 helpers; "
        "Gitea token, push-mirror, bounded secret delivery, and Phase 70 realization behavior passed."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
