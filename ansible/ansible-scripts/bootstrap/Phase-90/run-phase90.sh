#!/usr/bin/env bash
set -euo pipefail

# Phase 90 reconciles live cluster state after GitOps owns normal operations.
# It gathers and repairs bounded drift; it is not permitted to recreate the
# temporary bootstrap authority removed during the handoff.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck disable=SC1091
. "${script_dir}/diagnostics.sh"
# shellcheck disable=SC1091
. "${script_dir}/control-pair-common.sh"

ANSIBLE_INVENTORY="${ANSIBLE_INVENTORY:-ansible/node-inventory.yml}"
ANSIBLE_PLAYBOOK="${ANSIBLE_PLAYBOOK:-ansible/playbooks/healthcheck.yml}"
BOOTSTRAP_BREAKGLASS="${BOOTSTRAP_BREAKGLASS:-1}"
BOOTSTRAP_PHASE90_LOCAL_ONLY="${BOOTSTRAP_PHASE90_LOCAL_ONLY:-1}"
BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR:-/var/lib/bootstrap-secrets}"
OPENBAO_NAMESPACE="${OPENBAO_NAMESPACE:-openbao}"
OPENBAO_POD="${OPENBAO_POD:-openbao-0}"
phase90_log_root="${BOOTSTRAP_PHASE_LOG_DIR:-/var/log/bootstrap}"
PHASE90_LOG_FILE="${PHASE90_LOG_FILE:-${phase90_log_root}/phase90.log}"
BOOTSTRAP_DIAG_PHASE="phase90"
BOOTSTRAP_DIAG_LOG_PATH="${PHASE90_LOG_FILE}"
bootstrap_diag_init
phase90_start_ts="$(date +%s)"
phase90_last_error_cmd=""
phase90_last_error_line=""

if [[ -z "${BUNDLE_BOOTSTRAP_LOG_FILE:-}" ]]; then
  mkdir -p "$(dirname "${PHASE90_LOG_FILE}")"
  exec >>"${PHASE90_LOG_FILE}" 2>&1
fi

# Phase 90 can run during recovery even when the control-pair phases were not
# the entrypoint. The sourced file provides side-effect-free shared helpers;
# Phase 90 still owns its recovery-specific setup and policy below.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
cd "${repo_root}"

if [[ -z "${ANSIBLE_CONFIG:-}" && -f "${repo_root}/ansible/ansible.cfg" ]]; then
  export ANSIBLE_CONFIG="${repo_root}/ansible/ansible.cfg"
fi
export ANSIBLE_ROLES_PATH="${repo_root}/ansible/automation-roles:${repo_root}/ansible/playbooks/roles:/etc/ansible/roles:/usr/share/ansible/roles"

if [[ -z "${KUBECONFIG:-}" && -f /etc/rancher/rke2/rke2.yaml ]]; then
  export KUBECONFIG=/etc/rancher/rke2/rke2.yaml
fi

kubectl_bin=""
if command -v kubectl >/dev/null 2>&1; then
  kubectl_bin="$(command -v kubectl)"
elif [[ -x /var/lib/rancher/rke2/bin/kubectl ]]; then
  kubectl_bin="/var/lib/rancher/rke2/bin/kubectl"
fi

read_secret_file() {
  local path="${1:-}"
  if [[ -z "${path}" || ! -f "${path}" ]]; then
    return 0
  fi
  tr -d '\r' <"${path}" | awk 'NF{line=$0} END{printf "%s", line}'
}

retry_cmd() {
  local attempts="${1:-5}"
  local sleep_seconds="${2:-5}"
  shift 2
  local i=1
  while (( i <= attempts )); do
    if "$@"; then
      return 0
    fi
    if (( i == attempts )); then
      return 1
    fi
    sleep "${sleep_seconds}"
    i=$((i + 1))
  done
  return 1
}

# Preserve a distinction between observations and critical recovery failures so
# exported diagnostics tell an operator whether intervention is actually needed.
phase90_failures=0
phase90_critical_failures=0

phase90_exit_trap() {
  local rc=$?
  local end_ts duration
  end_ts="$(date +%s)"
  duration="$((end_ts - phase90_start_ts))"
  if [[ "${rc}" -eq 0 ]]; then
    bootstrap_diag_record \
      "phase=phase90" \
      "step=phase90" \
      "component=phase90" \
      "operation=run-complete" \
      "severity=info" \
      "exit_code=0" \
      "duration_seconds=${duration}" \
      "summary=phase90 complete" \
      "log_path=${PHASE90_LOG_FILE}"
  else
    bootstrap_diag_record \
      "phase=phase90" \
      "step=phase90" \
      "component=phase90" \
      "operation=run-failed" \
      "severity=error" \
      "exit_code=${rc}" \
      "duration_seconds=${duration}" \
      "failure_kind=$(bootstrap_diag_classify_failure_kind "${phase90_last_error_cmd}")" \
      "summary=phase90 failed at line ${phase90_last_error_line:-unknown}: ${phase90_last_error_cmd:-unknown}" \
      "log_path=${PHASE90_LOG_FILE}"
  fi
  exit "${rc}"
}
trap 'phase90_exit_trap' EXIT
trap 'phase90_last_error_line="${BASH_LINENO[0]:-unknown}"; phase90_last_error_cmd="${BASH_COMMAND:-unknown}"' ERR

bootstrap_diag_record \
  "phase=phase90" \
  "step=phase90" \
  "component=phase90" \
  "operation=run-start" \
  "severity=info" \
  "summary=phase90 starting" \
  "log_path=${PHASE90_LOG_FILE}"

run_phase90_step() {
  local label="${1:-}"
  local severity="${2:-critical}"
  shift 2

  echo "[phase90] step start: ${label}"

  set +e
  "$@"
  local rc=$?
  set -e

  if (( rc == 0 )); then
    echo "[phase90] step ok: ${label}"
    return 0
  fi

  echo "[phase90] step failed: ${label} (rc=${rc})" >&2
  phase90_failures=$((phase90_failures + 1))
  if [[ "${severity}" == "critical" ]]; then
    phase90_critical_failures=$((phase90_critical_failures + 1))
  fi
  return 0
}

read_openbao_platform_field() {
  local field="${1:-}"
  local token="${2:-}"
  if [[ -z "${field}" || -z "${token}" || -z "${kubectl_bin}" ]]; then
    return 0
  fi
  "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${token}" \
    bao kv get -field="${field}" secret/bootstrap/platform 2>/dev/null | tr -d '\r\n' || true
}

read_k8s_secret_key() {
  local namespace="${1:-}"
  local secret_name="${2:-}"
  local key="${3:-}"
  if [[ -z "${kubectl_bin}" || -z "${namespace}" || -z "${secret_name}" || -z "${key}" ]]; then
    return 0
  fi
  "${kubectl_bin}" -n "${namespace}" get secret "${secret_name}" -o "jsonpath={.data.${key}}" 2>/dev/null | base64 -d 2>/dev/null || true
}

read_phase90_bootstrap_field() {
  local field="${1:-}"
  local openbao_token="${2:-}"
  local openbao_value=""
  local backup_value=""
  if [[ -n "${openbao_token}" ]]; then
    openbao_value="$(read_openbao_platform_field "${field}" "${openbao_token}")"
    if [[ -n "${openbao_value}" ]]; then
      printf '%s' "${openbao_value}"
      return 0
    fi
  fi
  backup_value="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/${field}")"
  if [[ -n "${backup_value}" ]]; then
    printf '%s' "${backup_value}"
  fi
}

write_phase90_bootstrap_field() {
  local field="${1:-}"
  local value="${2:-}"
  local openbao_token="${3:-}"

  if [[ -z "${field}" ]]; then
    return 0
  fi
  if [[ -n "${BOOTSTRAP_SECRET_DIR:-}" && -d "${BOOTSTRAP_SECRET_DIR}" ]]; then
    printf '%s\n' "${value}" > "${BOOTSTRAP_SECRET_DIR}/${field}"
    chmod 0600 "${BOOTSTRAP_SECRET_DIR}/${field}" 2>/dev/null || true
  fi
  if [[ -n "${openbao_token}" ]]; then
    "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv patch secret/bootstrap/platform "${field}=${value}" >/dev/null 2>&1 || true
  fi
}

service_cluster_ip_phase90() {
  local namespace="${1:-}"
  local service_name="${2:-}"
  bootstrap_service_address "${kubectl_bin}" "${namespace}" "${service_name}"
}

validate_grafana_admin_password_phase90() {
  local password="${1:-}"
  local service_host=""
  local status=""

  if [[ -z "${password}" ]]; then
    return 1
  fi
  service_host="$(service_cluster_ip_phase90 observability grafana)"
  if [[ -z "${service_host}" ]]; then
    return 1
  fi
  status="$(
    curl -sS -o /dev/null -w '%{http_code}' --max-time 20 \
      -u "admin:${password}" \
      "http://${service_host}:80/api/admin/stats" 2>/dev/null || true
  )"
  [[ "${status}" == "200" ]]
}

resolve_grafana_admin_password_phase90() {
  local openbao_token="${1:-}"
  local password=""
  password="$(read_openbao_app_field observability/grafana admin_password "${openbao_token}")"
  if [[ -z "${password}" ]]; then
    password="$(read_phase90_bootstrap_field grafana_admin_password "${openbao_token}")"
  fi
  printf '%s' "${password}"
}

ensure_grafana_admin_password_phase90() {
  local openbao_token="${1:-}"
  local desired_password=""

  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase90] kubectl not available; cannot reconcile Grafana admin password" >&2
    return 1
  fi

  desired_password="$(resolve_grafana_admin_password_phase90 "${openbao_token}")"
  if [[ -z "${desired_password}" ]]; then
    echo "[phase90] Grafana admin password unavailable from bootstrap backup/OpenBao" >&2
    return 1
  fi

  if ! bootstrap_wait_for_deployment_rollout \
    "${kubectl_bin}" observability grafana grafana 'app.kubernetes.io/name=grafana'; then
    echo "[phase90] Grafana deployment did not become ready in time" >&2
    return 1
  fi

  "${kubectl_bin}" -n observability exec deploy/grafana -- env \
    TARGET_PW="${desired_password}" \
    /bin/sh -lc '/usr/share/grafana/bin/grafana cli --homepath /usr/share/grafana admin reset-admin-password "$TARGET_PW"' >/dev/null

  if ! validate_grafana_admin_password_phase90 "${desired_password}"; then
    echo "[phase90] Grafana admin password reconcile failed API validation" >&2
    return 1
  fi

  echo "[phase90] reconciled Grafana admin password to bootstrap/OpenBao value"
}

ensure_authentik_admin_password_phase90() {
  local deployment_name="authentik-server"
  local openbao_token=""
  local admin_username=""
  local admin_password=""
  local attempt=0
  local reconcile_output=""

  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase90] kubectl not available; cannot reconcile Authentik admin password" >&2
    return 1
  fi

  admin_username="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_admin_username")"
  admin_password="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_admin_password")"

  if "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token >/dev/null 2>&1; then
    openbao_token="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token \
        -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null || true
    )"
  fi

  if [[ -z "${admin_username}" && -n "${openbao_token}" ]]; then
    admin_username="$(read_openbao_app_field authentik/admin admin_username "${openbao_token}")"
  fi
  if [[ -z "${admin_username}" && -n "${openbao_token}" ]]; then
    admin_username="$(read_openbao_platform_field authentik_admin_username "${openbao_token}")"
  fi
  if [[ -z "${admin_password}" && -n "${openbao_token}" ]]; then
    admin_password="$(read_openbao_app_field authentik/admin bootstrap_password "${openbao_token}")"
  fi
  if [[ -z "${admin_password}" && -n "${openbao_token}" ]]; then
    admin_password="$(read_openbao_platform_field authentik_admin_password "${openbao_token}")"
    if [[ -n "${admin_password}" ]]; then
      echo "[phase90] restored Authentik admin password from OpenBao platform secret"
    fi
  fi

  if [[ -z "${admin_username}" ]]; then
    admin_username="akadmin"
  fi
  if [[ -z "${admin_password}" ]]; then
    echo "[phase90] Authentik admin password missing from bootstrap backup and OpenBao" >&2
    return 1
  fi

  echo "[phase90] waiting for deploy/${deployment_name} before applying final Authentik admin password"
  for attempt in $(seq 1 60); do
    if "${kubectl_bin}" -n authentik get deployment "${deployment_name}" >/dev/null 2>&1; then
      break
    fi
    sleep 10
  done
  if ! "${kubectl_bin}" -n authentik get deployment "${deployment_name}" >/dev/null 2>&1; then
    echo "[phase90] Authentik deployment ${deployment_name} was not created in time" >&2
    return 1
  fi

  if ! bootstrap_wait_for_deployment_rollout \
    "${kubectl_bin}" authentik "${deployment_name}" authentik 'app.kubernetes.io/name=authentik'; then
    echo "[phase90] Authentik deployment ${deployment_name} did not become ready in time" >&2
    return 1
  fi

  echo "[phase90] applying final Authentik admin password for ${admin_username}"
  reconcile_output="$(
    AUTHENTIK_BOOTSTRAP_ADMIN_USERNAME="${admin_username}" \
    AUTHENTIK_BOOTSTRAP_ADMIN_PASSWORD="${admin_password}" \
    "${kubectl_bin}" exec -i -n authentik "deploy/${deployment_name}" -- env \
      AUTHENTIK_BOOTSTRAP_ADMIN_USERNAME="${admin_username}" \
      AUTHENTIK_BOOTSTRAP_ADMIN_PASSWORD="${admin_password}" \
      /ak-root/.venv/bin/python /manage.py shell -c \
      'import os; from authentik.core.models import User; username=os.environ["AUTHENTIK_BOOTSTRAP_ADMIN_USERNAME"]; password=os.environ["AUTHENTIK_BOOTSTRAP_ADMIN_PASSWORD"]; email=f"{username}@bootstrap.invalid"; user=User.objects.filter(username=username).first(); created=user is None; user=user or User.objects.create_superuser(username=username, email=email, password=password); user.set_password(password); user.is_active=True; user.save(); user.refresh_from_db(); assert user.check_password(password), f"password verification failed for {username}"; print(f"created_authentik_user_for {username}" if created else f"using_existing_authentik_user_for {username}"); print(f"reconciled_password_for {username}")' \
      2>&1 || true
  )"
  printf '%s\n' "${reconcile_output}"
  if [[ "${reconcile_output}" != *"reconciled_password_for ${admin_username}"* ]]; then
    echo "[phase90] Authentik admin password reconcile did not emit success marker for ${admin_username}" >&2
    return 1
  fi
  return 0
}

echo "[phase90] repo: ${repo_root}"
echo "[phase90] running post-bootstrap healthcheck playbook"

mode_raw="$(printf '%s' "${BOOTSTRAP_BREAKGLASS}" | tr '[:upper:]' '[:lower:]')"
local_only_raw="$(printf '%s' "${BOOTSTRAP_PHASE90_LOCAL_ONLY}" | tr '[:upper:]' '[:lower:]')"
if [[ "${local_only_raw}" == "1" || "${local_only_raw}" == "true" || "${local_only_raw}" == "yes" || "${local_only_raw}" == "on" ]]; then
  ansible-playbook -i "${ANSIBLE_INVENTORY}" "${ANSIBLE_PLAYBOOK}" -e break_glass=true
elif [[ "${mode_raw}" == "1" || "${mode_raw}" == "true" || "${mode_raw}" == "yes" || "${mode_raw}" == "on" ]]; then
  ansible-playbook -i "${ANSIBLE_INVENTORY}" "${ANSIBLE_PLAYBOOK}" -e break_glass=true
else
  ansible-playbook -i "${ANSIBLE_INVENTORY}" "${ANSIBLE_PLAYBOOK}"
fi

run_phase90_step "reconcile Authentik admin password" critical ensure_authentik_admin_password_phase90
run_phase90_step "reconcile Grafana admin password" warning ensure_grafana_admin_password_phase90

if (( phase90_failures > 0 )); then
  echo "[phase90] completed with ${phase90_failures} failed step(s); critical=${phase90_critical_failures}" >&2
  exit 1
fi

cat <<'INFO'

Phase 90 complete (late live-state reconciliation).

INFO
