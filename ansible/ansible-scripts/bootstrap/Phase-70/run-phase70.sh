#!/usr/bin/env bash
set -euo pipefail

# Phase 70 verifies that the in-cluster GitOps control pair works after the
# Phase 50/60 transition. It is a health and handoff proof, not an installer.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck disable=SC1091
. "${script_dir}/diagnostics.sh"
# shellcheck disable=SC1091
. "${script_dir}/control-pair-common.sh"

ANSIBLE_INVENTORY="${ANSIBLE_INVENTORY:-ansible/node-inventory.yml}"
ANSIBLE_PLAYBOOK="${ANSIBLE_PLAYBOOK:-ansible/playbooks/healthcheck.yml}"
BOOTSTRAP_BREAKGLASS="${BOOTSTRAP_BREAKGLASS:-1}"
BOOTSTRAP_PHASE70_LOCAL_ONLY="${BOOTSTRAP_PHASE70_LOCAL_ONLY:-1}"
BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR:-/var/lib/bootstrap-secrets}"
OPENBAO_NAMESPACE="${OPENBAO_NAMESPACE:-openbao}"
OPENBAO_POD="${OPENBAO_POD:-openbao-0}"
phase70_log_root="${BOOTSTRAP_PHASE_LOG_DIR:-/var/log/bootstrap}"
PHASE70_LOG_FILE="${PHASE70_LOG_FILE:-${phase70_log_root}/phase70.log}"
BOOTSTRAP_DIAG_PHASE="phase70"
BOOTSTRAP_DIAG_LOG_PATH="${PHASE70_LOG_FILE}"
bootstrap_diag_init
phase70_start_ts="$(date +%s)"
phase70_last_error_cmd=""
phase70_last_error_line=""

if [[ -z "${BUNDLE_BOOTSTRAP_LOG_FILE:-}" ]]; then
  mkdir -p "$(dirname "${PHASE70_LOG_FILE}")"
  exec >>"${PHASE70_LOG_FILE}" 2>&1
fi

# Use the shared helper here so Phase 70 resolves the same checked-out fork and
# kubectl binary that the control-pair phases use.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
bootstrap_control_pair_prepare_repo "${repo_root}"

if [[ -z "${KUBECONFIG:-}" && -f /etc/rancher/rke2/rke2.yaml ]]; then
  export KUBECONFIG=/etc/rancher/rke2/rke2.yaml
fi

kubectl_bin="$(bootstrap_control_pair_resolve_kubectl || true)"

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

# Keep non-critical observations separate from failures that should make the
# recovery proof fail. The exit trap records both in diagnostics JSON.
phase70_failures=0
phase70_critical_failures=0

phase70_exit_trap() {
  local rc=$?
  local end_ts duration
  end_ts="$(date +%s)"
  duration="$((end_ts - phase70_start_ts))"
  if [[ "${rc}" -eq 0 ]]; then
    bootstrap_diag_record \
      "phase=phase70" \
      "step=phase70" \
      "component=phase70" \
      "operation=run-complete" \
      "severity=info" \
      "exit_code=0" \
      "duration_seconds=${duration}" \
      "summary=phase70 complete" \
      "log_path=${PHASE70_LOG_FILE}"
  else
    bootstrap_diag_record \
      "phase=phase70" \
      "step=phase70" \
      "component=phase70" \
      "operation=run-failed" \
      "severity=error" \
      "exit_code=${rc}" \
      "duration_seconds=${duration}" \
      "failure_kind=$(bootstrap_diag_classify_failure_kind "${phase70_last_error_cmd}")" \
      "summary=phase70 failed at line ${phase70_last_error_line:-unknown}: ${phase70_last_error_cmd:-unknown}" \
      "log_path=${PHASE70_LOG_FILE}"
  fi
  exit "${rc}"
}
trap 'phase70_exit_trap' EXIT
trap 'phase70_last_error_line="${BASH_LINENO[0]:-unknown}"; phase70_last_error_cmd="${BASH_COMMAND:-unknown}"' ERR

bootstrap_diag_record \
  "phase=phase70" \
  "step=phase70" \
  "component=phase70" \
  "operation=run-start" \
  "severity=info" \
  "summary=phase70 starting" \
  "log_path=${PHASE70_LOG_FILE}"

run_phase70_step() {
  local label="${1:-}"
  local severity="${2:-critical}"
  shift 2

  echo "[phase70] step start: ${label}"

  set +e
  "$@"
  local rc=$?
  set -e

  if (( rc == 0 )); then
    echo "[phase70] step ok: ${label}"
    return 0
  fi

  echo "[phase70] step failed: ${label} (rc=${rc})" >&2
  phase70_last_error_line="${BASH_LINENO[0]:-unknown}"
  phase70_last_error_cmd="${label} (rc=${rc})"
  bootstrap_diag_record \
    "phase=phase70" \
    "step=${label}" \
    "component=phase70" \
    "operation=step-failed" \
    "severity=$([[ "${severity}" == "critical" ]] && echo error || echo warning)" \
    "exit_code=${rc}" \
    "summary=${label} failed" \
    "log_path=${PHASE70_LOG_FILE}"
  phase70_failures=$((phase70_failures + 1))
  if [[ "${severity}" == "critical" ]]; then
    phase70_critical_failures=$((phase70_critical_failures + 1))
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

read_phase70_bootstrap_field() {
  local field="${1:-}"
  local openbao_token="${2:-}"
  local backup_value=""

  backup_value="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/${field}")"
  if [[ -n "${backup_value}" ]]; then
    printf '%s' "${backup_value}"
    return 0
  fi
  if [[ -n "${openbao_token}" ]]; then
    read_openbao_platform_field "${field}" "${openbao_token}"
  fi
}

write_phase70_bootstrap_field() {
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

log_widget_field_phase70() {
  local field="${1:-}"
  local status="${2:-}"
  local detail="${3:-}"
  if [[ -n "${detail}" ]]; then
    echo "[phase70] homepage field ${field}: ${status} (${detail})"
  else
    echo "[phase70] homepage field ${field}: ${status}"
  fi
}

persist_widget_field_phase70() {
  local field="${1:-}"
  local value="${2:-}"
  local openbao_token="${3:-}"
  if [[ -z "${field}" || -z "${value}" ]]; then
    return 0
  fi
  write_phase70_bootstrap_field "${field}" "${value}" "${openbao_token}"
  persist_openbao_homepage_field "${field}" "${value}" "${openbao_token}"
}

service_cluster_ip_phase70() {
  local namespace="${1:-}"
  local service_name="${2:-}"
  if [[ -z "${kubectl_bin}" || -z "${namespace}" || -z "${service_name}" ]]; then
    return 0
  fi
  "${kubectl_bin}" -n "${namespace}" get svc "${service_name}" -o jsonpath='{.spec.clusterIP}' 2>/dev/null || true
}

validate_argocd_widget_key_phase70() {
  local token="${1:-}"
  local service_host=""
  local status=""

  if [[ -z "${token}" ]]; then
    return 1
  fi
  service_host="$(service_cluster_ip_phase70 argocd argocd-server)"
  if [[ -z "${service_host}" ]]; then
    return 1
  fi
  status="$(
    curl -sS -o /dev/null -w '%{http_code}' --max-time 20 \
      -H "Authorization: Bearer ${token}" \
      "http://${service_host}:80/api/v1/applications" 2>/dev/null || true
  )"
  [[ "${status}" == "200" ]]
}

validate_grafana_admin_password_phase70() {
  local password="${1:-}"
  local service_host=""
  local status=""

  if [[ -z "${password}" ]]; then
    return 1
  fi
  service_host="$(service_cluster_ip_phase70 observability grafana)"
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

validate_gitea_widget_auth_phase70() {
  local token="${1:-}"
  local service_host=""
  local status=""

  if [[ -z "${token}" ]]; then
    return 1
  fi
  service_host="$(service_cluster_ip_phase70 gitea gitea-http)"
  if [[ -z "${service_host}" ]]; then
    return 1
  fi
  status="$(
    curl -sS -o /dev/null -w '%{http_code}' --max-time 20 \
      -H "Authorization: token ${token}" \
      "http://${service_host}:3000/api/v1/user" 2>/dev/null || true
  )"
  [[ "${status}" == "200" ]]
}

ensure_ansible_runner_pull_secret_phase70() {
  local openbao_token="${1:-}"
  local registry_username=""
  local registry_token=""
  local registry_host=""
  local push_host=""
  local runner_image=""

  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase70] kubectl not available; cannot reconcile ansible-runner pull secret" >&2
    return 1
  fi

  runner_image="$(
    "${kubectl_bin}" -n ansible get configmap ansible-cluster-config \
      -o jsonpath='{.data.ANSIBLE_RUNNER_IMAGE}' 2>/dev/null || true
  )"
  if [[ "${runner_image}" == */* ]]; then
    registry_host="${runner_image%%/*}"
  fi
  if [[ -z "${registry_host}" ]]; then
    echo "[phase70] ansible-runner pull secret reconcile failed: ansible-cluster-config does not declare ANSIBLE_RUNNER_IMAGE" >&2
    return 1
  fi
  push_host="$(ansible_runner_registry_host_push)"

  registry_username="$(read_phase70_bootstrap_field argocd_repo_username "${openbao_token}")"
  if [[ -z "${registry_username}" ]]; then
    registry_username="$(read_phase70_bootstrap_field gitea_admin_username "${openbao_token}")"
  fi
  if [[ -z "${registry_username}" ]]; then
    registry_username="gitea-admin"
  fi

  registry_token="$(read_phase70_bootstrap_field gitea_git_token "${openbao_token}")"
  if [[ -z "${registry_token}" ]]; then
    registry_token="$(read_phase70_bootstrap_field argocd_repo_token "${openbao_token}")"
  fi
  if [[ -z "${registry_token}" ]]; then
    echo "[phase70] ansible-runner pull secret reconcile failed: registry token unavailable from backup/OpenBao" >&2
    return 1
  fi

  # The image-pull Secret is a Kubernetes API consumer, so ESO remains the
  # adapter. Phase 70 may seed a missing OpenBao field but never writes its
  # delivery copy directly.
  seed_openbao_app_fields gitea/registry "${openbao_token}" \
    "host=${registry_host}" \
    "push_host=${push_host}" \
    "username=${registry_username}" \
    "token=${registry_token}"
  bootstrap_request_external_secret_refresh "${kubectl_bin}" ansible gitea-registry-creds
  bootstrap_wait_for_external_secret_delivery \
    "${kubectl_bin}" ansible gitea-registry-creds gitea-registry-creds ansible-runner 30

  echo "[phase70] ensured ansible-runner image pull secret for ${registry_host}"
}

ansible_runner_image_exists_phase70() {
  local openbao_token="${1:-}"
  local image=""
  local registry_username=""
  local registry_token=""
  local registry_host=""
  local repo_path=""
  local image_ref=""
  local registry_base_url=""
  local bearer_token=""
  local status=""

  if [[ -z "${kubectl_bin}" ]]; then
    return 1
  fi

  image="$("${kubectl_bin}" -n ansible get configmap ansible-cluster-config -o jsonpath='{.data.ANSIBLE_RUNNER_IMAGE}' 2>/dev/null || true)"
  if [[ -z "${image}" ]]; then
    return 1
  fi

  mapfile -t image_parts < <(python3 - <<'PY' "${image}"
import sys
image = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
if not image or "/" not in image or ":" not in image.rsplit("/", 1)[-1]:
    raise SystemExit(1)
left, image_ref = image.rsplit(":", 1)
registry_host, repo_path = left.split("/", 1)
print(registry_host)
print(repo_path)
print(image_ref)
PY
  )
  if [[ "${#image_parts[@]}" -ne 3 ]]; then
    return 1
  fi
  registry_host="${image_parts[0]}"
  repo_path="${image_parts[1]}"
  image_ref="${image_parts[2]}"

  registry_username="$(read_phase70_bootstrap_field argocd_repo_username "${openbao_token}")"
  if [[ -z "${registry_username}" ]]; then
    registry_username="$(read_phase70_bootstrap_field gitea_admin_username "${openbao_token}")"
  fi
  if [[ -z "${registry_username}" ]]; then
    registry_username="gitea-admin"
  fi

  registry_token="$(read_phase70_bootstrap_field gitea_git_token "${openbao_token}")"
  if [[ -z "${registry_token}" ]]; then
    registry_token="$(read_phase70_bootstrap_field argocd_repo_token "${openbao_token}")"
  fi
  if [[ -z "${registry_token}" ]]; then
    return 1
  fi

  case "${registry_host}" in
    *.svc|*.svc.*|*.cluster.local|*".svc:"*)
      registry_base_url="http://${registry_host}"
      ;;
    *)
      registry_base_url="https://${registry_host}"
      ;;
  esac

  bearer_token="$(
    curl -fsS -u "${registry_username}:${registry_token}" \
      "${registry_base_url}/v2/token?service=container_registry&scope=repository:${repo_path}:pull" \
      | python3 -c 'import sys, json; print(json.load(sys.stdin).get("token", ""))' 2>/dev/null || true
  )"
  if [[ -z "${bearer_token}" ]]; then
    return 1
  fi

  status="$(
    curl -sS -o /dev/null -w '%{http_code}' \
      -H "Authorization: Bearer ${bearer_token}" \
      "${registry_base_url}/v2/${repo_path}/manifests/${image_ref}" 2>/dev/null || true
  )"
  [[ "${status}" == "200" ]]
}

ensure_ansible_runner_phase70() {
  local openbao_token=""
  local job_name="ansible-runner-image-build"

  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase70] kubectl not available; cannot reconcile ansible-runner" >&2
    return 1
  fi

  if ! "${kubectl_bin}" get namespace ansible >/dev/null 2>&1; then
    echo "[phase70] ansible namespace missing; skipping ansible-runner reconcile"
    return 0
  fi

  if "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token >/dev/null 2>&1; then
    openbao_token="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token \
        -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null || true
    )"
  fi

  if ! bootstrap_wait_for_deployment_rollout \
    "${kubectl_bin}" gitea gitea gitea 'app.kubernetes.io/name=gitea'; then
    echo "[phase70] Gitea deployment did not become ready in time; cannot reconcile ansible-runner" >&2
    return 1
  fi

  ensure_ansible_runner_pull_secret_phase70 "${openbao_token}"

  if "${kubectl_bin}" -n ansible get job "${job_name}" >/dev/null 2>&1; then
    if ! "${kubectl_bin}" -n ansible wait --for=condition=complete "job/${job_name}" --timeout=10m >/dev/null 2>&1; then
      echo "[phase70] ansible-runner image build job did not complete in time" >&2
      return 1
    fi
  else
    if ! ansible_runner_image_exists_phase70 "${openbao_token}"; then
      echo "[phase70] ansible-runner image build job missing and runner image is not present in the registry" >&2
      return 1
    fi
    echo "[phase70] ansible-runner image build job missing; existing image already present"
  fi

  "${kubectl_bin}" -n ansible rollout restart deploy/ansible-runner >/dev/null

  if ! bootstrap_wait_for_deployment_rollout \
    "${kubectl_bin}" ansible ansible-runner ansible-runner 'app=ansible-runner'; then
    echo "[phase70] ansible-runner deployment did not become ready after build completion" >&2
    return 1
  fi

  echo "[phase70] reconciled ansible-runner registry secret and restarted deployment after image build"
}

run_gitops_realization_checks_phase70() {
  local phase60_script="${repo_root}/ansible/ansible-scripts/bootstrap/Phase-60/run-phase60.sh"
  if [[ ! -x "${phase60_script}" ]]; then
    echo "[phase70] missing Phase 60 handoff script at ${phase60_script}" >&2
    return 1
  fi
  # Phase 70 is the release gate for the handoff. Keep its observation path
  # non-authoritative, but make failed requirements fatal and bounded. A health
  # proof must not restart Gitea or wait through installer-length retries.
  PHASE60_MODE=realize \
    PHASE60_RECONCILE_ONLY=1 \
    PHASE60_WARNING_ONLY=0 \
    PHASE60_GITEA_ROLLOUT_TIMEOUT="${PHASE70_GITEA_ROLLOUT_TIMEOUT:-120s}" \
    PHASE60_GITEA_GITOPS_SETTLE_TIMEOUT_SECONDS="${PHASE70_GITEA_GITOPS_SETTLE_TIMEOUT_SECONDS:-180}" \
    PHASE60_GITEA_ROLLOUT_ATTEMPTS=1 \
    PHASE60_GITEA_ROLLOUT_RESTART_ON_FAIL=0 \
    bash "${phase60_script}"
}

verify_secret_delivery_foundation_phase70() {
  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase70] kubectl is unavailable; cannot verify secret-delivery foundation" >&2
    return 1
  fi
  BOOTSTRAP_SECRET_DELIVERY_FOUNDATION_TIMEOUT_SECONDS="${PHASE70_SECRET_DELIVERY_FOUNDATION_TIMEOUT_SECONDS:-300}" \
    bootstrap_wait_for_secret_delivery_foundation \
      "${kubectl_bin}" "secret-delivery-foundation"
}

verify_openbao_secret_delivery_phase70() {
  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase70] kubectl is unavailable; cannot verify OpenBao secret delivery" >&2
    return 1
  fi

  # A CSI status proves that kubelet authenticated this workload's dedicated
  # service account to OpenBao and mounted its least-privilege path. Checking
  # each consumer here turns a delivery fault into a quick, specific failure
  # instead of allowing later rollout waits to consume the run.
  bootstrap_wait_for_csi_secret_delivery \
    "${kubectl_bin}" observability grafana-openbao grafana
  bootstrap_wait_for_external_secret_delivery \
    "${kubectl_bin}" observability homepage-grafana-desired homepage-grafana-desired grafana
  bootstrap_wait_for_csi_secret_delivery \
    "${kubectl_bin}" observability apprise-openbao apprise
  bootstrap_wait_for_csi_secret_delivery \
    "${kubectl_bin}" homepage homepage-openbao homepage
  bootstrap_wait_for_csi_secret_delivery \
    "${kubectl_bin}" cloudflared cloudflared-openbao cloudflared
  bootstrap_wait_for_csi_secret_delivery \
    "${kubectl_bin}" ingress external-dns-openbao external-dns
  bootstrap_wait_for_csi_secret_delivery \
    "${kubectl_bin}" ansible ansible-runner-openbao ansible-runner
  bootstrap_wait_for_csi_secret_delivery \
    "${kubectl_bin}" authentik authentik-openbao authentik
  bootstrap_wait_for_external_secret_delivery \
    "${kubectl_bin}" authentik authentik-postgresql-desired authentik-postgresql-desired authentik-postgresql
  bootstrap_wait_for_csi_secret_delivery \
    "${kubectl_bin}" gitea gitea-openbao gitea
  bootstrap_wait_for_external_secret_delivery \
    "${kubectl_bin}" gitea gitea-postgresql-desired gitea-postgresql-desired gitea-postgresql
}

# Runtime rotations are explicit operator actions: setting this flag after an
# OpenBao update restarts only the listed owner workloads. There is no generic
# cluster-wide controller. A successful rollout alone is insufficient; the new
# pod must also publish a CSI mount status for its own identity and class.
restart_csi_workload_phase70() {
  local namespace="${1:?namespace}"
  local deployment="${2:?deployment}"
  local provider_class="${3:?SecretProviderClass}"
  local selector="${4:?pod selector}"
  local component="${5:-${deployment}}"
  local pod_name=""

  if ! "${kubectl_bin}" -n "${namespace}" get deployment "${deployment}" >/dev/null 2>&1; then
    echo "[phase70] ${component}: deployment is not installed; skipping requested CSI rotation"
    return 0
  fi
  echo "[phase70] ${component}: restarting owner deployment after requested OpenBao rotation"
  "${kubectl_bin}" -n "${namespace}" rollout restart "deployment/${deployment}" >/dev/null
  bootstrap_wait_for_deployment_rollout \
    "${kubectl_bin}" "${namespace}" "${deployment}" "${component}" "${selector}"
  pod_name="$("${kubectl_bin}" -n "${namespace}" get pods --selector="${selector}" \
    --sort-by=.metadata.creationTimestamp -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | tail -n 1)"
  if [[ -z "${pod_name}" ]]; then
    echo "[phase70] ${component}: rollout completed without a selected replacement pod" >&2
    return 1
  fi
  bootstrap_wait_for_csi_secret_delivery \
    "${kubectl_bin}" "${namespace}" "${provider_class}" "${component}" "${pod_name}"
  echo "[phase70] ${component}: replacement pod ${pod_name} mounted ${provider_class}"
}

reconcile_csi_runtime_rotations_phase70() {
  local enabled="${BOOTSTRAP_RECONCILE_SECRET_ROTATION:-0}"
  enabled="$(printf '%s' "${enabled}" | tr '[:upper:]' '[:lower:]')"
  if [[ "${enabled}" != "1" && "${enabled}" != "true" && "${enabled}" != "yes" && "${enabled}" != "on" ]]; then
    echo "[phase70] CSI runtime rotation reconcile not requested"
    return 0
  fi

  restart_csi_workload_phase70 observability grafana grafana-openbao 'app.kubernetes.io/name=grafana' grafana
  restart_csi_workload_phase70 observability apprise apprise-openbao 'app=apprise' apprise
  restart_csi_workload_phase70 homepage homepage homepage-openbao 'app.kubernetes.io/name=homepage' homepage
  restart_csi_workload_phase70 cloudflared cloudflared cloudflared-openbao 'app=cloudflared' cloudflared
  restart_csi_workload_phase70 ingress external-dns external-dns-openbao 'app=external-dns' external-dns
  restart_csi_workload_phase70 ansible ansible-runner ansible-runner-openbao 'app=ansible-runner' ansible-runner
  restart_csi_workload_phase70 authentik authentik-server authentik-openbao 'app.kubernetes.io/component=server' authentik-server
  restart_csi_workload_phase70 authentik authentik-worker authentik-openbao 'app.kubernetes.io/component=worker' authentik-worker
  restart_csi_workload_phase70 gitea gitea gitea-openbao 'app.kubernetes.io/name=gitea' gitea
}

mint_argocd_widget_key_phase70() {
  local admin_password=""
  local token=""
  local openbao_token="${1:-}"
  local readonly_capability=""

  if [[ -z "${kubectl_bin}" ]]; then
    return 1
  fi

  admin_password="${HOMEPAGE_ARGOCD_WIDGET_KEY_PASSWORD:-${ARGOCD_ADMIN_PASSWORD:-}}"
  if [[ -z "${admin_password}" ]]; then
    admin_password="$(read_openbao_app_field argocd/admin password "${openbao_token}")"
  fi
  if [[ -z "${admin_password}" ]]; then
    admin_password="$(read_phase70_bootstrap_field argocd_admin_password "${openbao_token}")"
  fi
  if [[ -z "${admin_password}" ]]; then
    echo "[phase70] Argo CD widget token mint skipped: admin password unavailable" >&2
    return 1
  fi

  if ! bootstrap_wait_for_deployment_rollout \
    "${kubectl_bin}" argocd argocd-server argocd-server 'app.kubernetes.io/name=argocd-server'; then
    echo "[phase70] Argo CD widget token mint failed: argocd-server not ready in time" >&2
    return 1
  fi

  readonly_capability="$("${kubectl_bin}" -n argocd get cm argocd-cm -o jsonpath='{.data.accounts\.readonly}' 2>/dev/null || true)"
  if [[ "${readonly_capability}" != *"apiKey"* ]]; then
    echo "[phase70] Argo CD widget token mint failed: readonly apiKey account not configured yet" >&2
    return 1
  fi

  token="$(
    "${kubectl_bin}" -n argocd exec -i deploy/argocd-server -- env \
      ARGOCD_WIDGET_ADMIN_PASSWORD="${admin_password}" \
      /bin/sh -lc '
        argocd login 127.0.0.1:8080 --plaintext --username admin --password "$ARGOCD_WIDGET_ADMIN_PASSWORD" >/dev/null 2>&1 &&
        argocd account generate-token --account readonly
      ' 2>/dev/null | tr -d '\r\n'
  )"

  if [[ -z "${token}" ]]; then
    echo "[phase70] Argo CD widget token mint failed: unable to generate readonly account token" >&2
    return 1
  fi

  if ! validate_argocd_widget_key_phase70 "${token}"; then
    echo "[phase70] Argo CD widget token mint failed: minted token did not pass API validation" >&2
    return 1
  fi

  printf '%s' "${token}"
}

mint_gitea_widget_auth_phase70() {
  local openbao_token="${1:-}"
  local admin_username="${2:-}"
  local admin_password="${3:-}"
  local gitea_base_url="${4:-}"
  local token=""

  if [[ -z "${kubectl_bin}" ]]; then
    return 1
  fi

  if [[ -z "${admin_username}" ]]; then
    admin_username="gitea-admin"
  fi
  if [[ -z "${admin_password}" || -z "${gitea_base_url}" ]]; then
    echo "[phase70] Gitea widget token mint failed: admin validation inputs unavailable" >&2
    return 1
  fi

  if ! bootstrap_wait_for_deployment_rollout \
    "${kubectl_bin}" gitea gitea gitea 'app.kubernetes.io/name=gitea'; then
    echo "[phase70] Gitea widget token mint failed: gitea deployment not ready in time" >&2
    return 1
  fi

  token="$("${kubectl_bin}" -n gitea exec deploy/gitea -c gitea -- sh -lc '
    gitea admin user generate-access-token \
      --username "'"${admin_username}"'" \
      --token-name "homepage-widget" \
      --scopes "read:notification,read:repository,read:issue" \
      --raw 2>/dev/null \
    || gitea admin user generate-access-token \
      --username "'"${admin_username}"'" \
      --token-name "homepage-widget-$(date +%s)" \
      --scopes "read:notification,read:repository,read:issue" \
      --raw 2>/dev/null
  ' 2>/dev/null | tr -d '\r\n' || true)"

  if [[ -z "${token}" ]]; then
    echo "[phase70] Gitea widget token mint failed: unable to generate API token" >&2
    return 1
  fi
  if ! validate_gitea_widget_auth_phase70 "${token}" \
    || ! gitea_widget_token_has_required_scopes \
      "${gitea_base_url}" "${admin_username}" "${admin_password}" "${token}"; then
    echo "[phase70] Gitea widget token mint failed: token or read-only scope validation failed" >&2
    return 1
  fi

  printf '%s' "${token}"
}

resolve_grafana_admin_password_phase70() {
  local openbao_token="${1:-}"
  local password=""
  password="$(read_openbao_app_field observability/grafana admin_password "${openbao_token}")"
  if [[ -z "${password}" ]]; then
    password="$(read_phase70_bootstrap_field grafana_admin_password "${openbao_token}")"
  fi
  printf '%s' "${password}"
}

ensure_grafana_admin_password_phase70() {
  local openbao_token="${1:-}"
  local desired_password=""

  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase70] kubectl not available; cannot reconcile Grafana admin password" >&2
    return 1
  fi

  desired_password="$(resolve_grafana_admin_password_phase70 "${openbao_token}")"
  if [[ -z "${desired_password}" ]]; then
    echo "[phase70] Grafana admin password unavailable from bootstrap backup/OpenBao" >&2
    return 1
  fi

  if ! bootstrap_wait_for_deployment_rollout \
    "${kubectl_bin}" observability grafana grafana 'app.kubernetes.io/name=grafana'; then
    echo "[phase70] Grafana deployment did not become ready in time" >&2
    return 1
  fi

  "${kubectl_bin}" -n observability exec deploy/grafana -- env \
    TARGET_PW="${desired_password}" \
    /bin/sh -lc '/usr/share/grafana/bin/grafana cli --homepath /usr/share/grafana admin reset-admin-password "$TARGET_PW"' >/dev/null

  if ! validate_grafana_admin_password_phase70 "${desired_password}"; then
    echo "[phase70] Grafana admin password reconcile failed API validation" >&2
    return 1
  fi

  echo "[phase70] reconciled Grafana admin password to bootstrap/OpenBao value"
}

ensure_homepage_widget_secrets_phase70() {
  local openbao_token=""
  local argocd_widget_key=""
  local gitea_widget_auth=""
  local prior_argocd_widget_key=""
  local prior_gitea_widget_auth=""
  local gitea_admin_username=""
  local gitea_admin_password=""
  local gitea_service_host=""
  local gitea_base_url=""
  local secret_manifest=""
  local failure=0

  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase70] kubectl not available; skipping Homepage widget reconcile" >&2
    return 0
  fi

  if ! "${kubectl_bin}" get namespace homepage >/dev/null 2>&1; then
    echo "[phase70] homepage namespace missing; skipping Homepage widget reconcile"
    return 0
  fi

  if "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token >/dev/null 2>&1; then
    openbao_token="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token \
        -o jsonpath='{.data.token}' 2>/dev/null | base64 -d 2>/dev/null || true
    )"
  fi

  gitea_admin_username="$(read_openbao_app_field gitea/admin username "${openbao_token}")"
  if [[ -z "${gitea_admin_username}" ]]; then
    gitea_admin_username="$(read_phase70_bootstrap_field gitea_admin_username "${openbao_token}")"
  fi
  gitea_admin_username="${gitea_admin_username:-gitea-admin}"
  gitea_admin_password="$(read_openbao_app_field gitea/admin password "${openbao_token}")"
  if [[ -z "${gitea_admin_password}" ]]; then
    gitea_admin_password="$(read_phase70_bootstrap_field gitea_admin_password "${openbao_token}")"
  fi
  gitea_service_host="$(service_cluster_ip_phase70 gitea gitea-http)"
  if [[ -n "${gitea_service_host}" ]]; then
    gitea_base_url="http://${gitea_service_host}:3000"
  fi

  prior_argocd_widget_key="$(read_openbao_app_field homepage/widgets HOMEPAGE_ARGOCD_WIDGET_KEY "${openbao_token}")"
  argocd_widget_key="${prior_argocd_widget_key}"
  if validate_argocd_widget_key_phase70 "${argocd_widget_key}"; then
    log_widget_field_phase70 homepage_argocd_widget_key reused "validated from OpenBao"
  else
    argocd_widget_key="$(mint_argocd_widget_key_phase70 "${openbao_token}" || true)"
    if [[ -n "${argocd_widget_key}" ]]; then
      log_widget_field_phase70 homepage_argocd_widget_key minted "no valid stored credential; validated via Argo CD API"
    else
      argocd_widget_key=""
      log_widget_field_phase70 homepage_argocd_widget_key invalid "no validated token available"
      failure=1
    fi
  fi

  prior_gitea_widget_auth="$(read_openbao_app_field homepage/widgets HOMEPAGE_GITEA_WIDGET_AUTH "${openbao_token}")"
  gitea_widget_auth="${prior_gitea_widget_auth}"
  if validate_gitea_widget_auth_phase70 "${gitea_widget_auth}" \
    && gitea_widget_token_has_required_scopes \
      "${gitea_base_url}" "${gitea_admin_username}" "${gitea_admin_password}" "${gitea_widget_auth}"; then
    log_widget_field_phase70 homepage_gitea_widget_auth reused "validated read-only token from OpenBao"
  else
    gitea_widget_auth="$(mint_gitea_widget_auth_phase70 \
      "${openbao_token}" "${gitea_admin_username}" "${gitea_admin_password}" "${gitea_base_url}" || true)"
    if [[ -n "${gitea_widget_auth}" ]]; then
      log_widget_field_phase70 homepage_gitea_widget_auth minted "no valid stored credential; validated via Gitea API"
    else
      gitea_widget_auth=""
      log_widget_field_phase70 homepage_gitea_widget_auth invalid "no validated token available"
      failure=1
    fi
  fi

  persist_widget_field_phase70 homepage_argocd_widget_key "${argocd_widget_key}" "${openbao_token}"
  persist_widget_field_phase70 homepage_gitea_widget_auth "${gitea_widget_auth}" "${openbao_token}"

  # A valid OpenBao value needs no restart. A minted replacement is consumed
  # only by the next pod through homepage-openbao; never recreate a native
  # delivery Secret from this reconciliation path.
  if [[ "${argocd_widget_key}" != "${prior_argocd_widget_key}" || \
        "${gitea_widget_auth}" != "${prior_gitea_widget_auth}" ]]; then
    "${kubectl_bin}" -n homepage rollout restart deploy/homepage >/dev/null
    if ! bootstrap_wait_for_deployment_rollout \
      "${kubectl_bin}" homepage homepage homepage 'app.kubernetes.io/name=homepage'; then
      echo "[phase70] Homepage restart did not become ready; diagnostics were captured" >&2
    fi
    bootstrap_wait_for_csi_secret_delivery \
      "${kubectl_bin}" homepage homepage-openbao homepage
    echo "[phase70] restarted Homepage deployment after OpenBao widget reconciliation"
  fi

  if [[ -n "${gitea_widget_auth}" ]]; then
    if revoke_stale_gitea_widget_tokens \
      "${gitea_base_url}" "${gitea_admin_username}" "${gitea_admin_password}" "${gitea_widget_auth}"; then
      log_widget_field_phase70 homepage_gitea_widget_auth revoked "removed superseded Homepage tokens"
    else
      log_widget_field_phase70 homepage_gitea_widget_auth invalid "could not prove stale-token revocation"
      failure=1
    fi
  fi

  if (( failure > 0 )); then
    echo "[phase70] Homepage widget reconciliation failed validation for one or more critical fields" >&2
    return 1
  fi

  echo "[phase70] reconciled Homepage widget secrets from validated live app state"
}

ensure_authentik_admin_password_phase70() {
  local deployment_name="authentik-server"
  local openbao_token=""
  local admin_username=""
  local admin_password=""
  local attempt=0
  local reconcile_output=""

  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase70] kubectl not available; cannot reconcile Authentik admin password" >&2
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
      echo "[phase70] restored Authentik admin password from OpenBao platform secret"
    fi
  fi

  if [[ -z "${admin_username}" ]]; then
    admin_username="akadmin"
  fi
  if [[ -z "${admin_password}" ]]; then
    echo "[phase70] Authentik admin password missing from bootstrap backup and OpenBao" >&2
    return 1
  fi

  echo "[phase70] waiting for deploy/${deployment_name} before applying final Authentik admin password"
  for attempt in $(seq 1 60); do
    if "${kubectl_bin}" -n authentik get deployment "${deployment_name}" >/dev/null 2>&1; then
      break
    fi
    sleep 10
  done
  if ! "${kubectl_bin}" -n authentik get deployment "${deployment_name}" >/dev/null 2>&1; then
    echo "[phase70] Authentik deployment ${deployment_name} was not created in time" >&2
    return 1
  fi

  if ! bootstrap_wait_for_deployment_rollout \
    "${kubectl_bin}" authentik "${deployment_name}" authentik 'app.kubernetes.io/name=authentik'; then
    echo "[phase70] Authentik deployment ${deployment_name} did not become ready in time" >&2
    return 1
  fi

  echo "[phase70] applying final Authentik admin password for ${admin_username}"
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
    echo "[phase70] Authentik admin password reconcile did not emit success marker for ${admin_username}" >&2
    return 1
  fi
  return 0
}

normalize_phase70_repo_url() {
  local repo_url="${1:-}"
  python3 - <<'PY' "${repo_url}"
import re
import sys
from urllib.parse import urlparse

repo_url = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
if not repo_url:
    print("", end="")
    raise SystemExit(0)

parsed = urlparse(repo_url)
host = (parsed.hostname or "").strip().lower()
path = (parsed.path or "").strip()

is_ipv4 = bool(re.fullmatch(r"\d+\.\d+\.\d+\.\d+", host))
is_giteaish = (
    host.startswith("gitea.")
    or ".gitea.svc" in host
    or host == "127.0.0.1"
    or is_ipv4
)

if is_giteaish and path.endswith(".git"):
    print(f"gitea:{path}", end="")
else:
    print(repo_url, end="")
PY
}

verify_phase70_gitops_handoff() {
  local repo_url=""
  local repo_branch=""
  local app_repo_url=""
  local set_repo_url=""
  local secret_repo_url=""
  local pre_openbao_repo_url=""
  local pre_openbao_repo_path=""
  local repo_url_norm=""
  local app_repo_url_norm=""
  local set_repo_url_norm=""
  local secret_repo_url_norm=""
  local pre_openbao_repo_url_norm=""

  if [[ -z "${kubectl_bin}" ]]; then
    echo "[phase70] kubectl not available; skipping GitOps handoff verification" >&2
    return 0
  fi

  app_repo_url="$("${kubectl_bin}" -n argocd get application app-of-apps -o jsonpath='{.spec.source.repoURL}' 2>/dev/null || true)"
  repo_branch="$("${kubectl_bin}" -n argocd get application app-of-apps -o jsonpath='{.spec.source.targetRevision}' 2>/dev/null || true)"
  set_repo_url="$("${kubectl_bin}" -n argocd get applicationset apps -o jsonpath='{.spec.generators[0].git.repoURL}' 2>/dev/null || true)"
  secret_repo_url="$("${kubectl_bin}" -n argocd get secret argocd-repository-bootstrap -o jsonpath='{.stringData.url}' 2>/dev/null || true)"
  if [[ -z "${secret_repo_url}" ]]; then
    secret_repo_url="$("${kubectl_bin}" -n argocd get secret argocd-repository-bootstrap -o jsonpath='{.data.url}' 2>/dev/null | base64 -d 2>/dev/null || true)"
  fi
  pre_openbao_repo_url="$("${kubectl_bin}" -n argocd get application platform-pre-openbao -o jsonpath='{.spec.source.repoURL}' 2>/dev/null || true)"
  pre_openbao_repo_path="$("${kubectl_bin}" -n argocd get application platform-pre-openbao -o jsonpath='{.spec.source.path}' 2>/dev/null || true)"

  repo_url="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_url")"
  if [[ -z "${repo_url}" ]]; then
    repo_url="${secret_repo_url:-${app_repo_url}}"
  fi
  if [[ -z "${repo_branch}" ]]; then
    repo_branch="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_branch")"
  fi
  if [[ -z "${repo_branch}" ]]; then
    repo_branch="HEAD"
  fi
  if [[ -z "${repo_url}" ]]; then
    echo "[phase70] GitOps handoff verification failed: unable to determine expected repo URL from cluster state" >&2
    return 1
  fi

  repo_url_norm="$(normalize_phase70_repo_url "${repo_url}")"
  app_repo_url_norm="$(normalize_phase70_repo_url "${app_repo_url}")"
  set_repo_url_norm="$(normalize_phase70_repo_url "${set_repo_url}")"
  secret_repo_url_norm="$(normalize_phase70_repo_url "${secret_repo_url}")"
  pre_openbao_repo_url_norm="$(normalize_phase70_repo_url "${pre_openbao_repo_url}")"

  if [[ "${app_repo_url_norm}" != "${repo_url_norm}" ]]; then
    echo "[phase70] GitOps handoff verification failed: app-of-apps repoURL=${app_repo_url:-<empty>} expected=${repo_url}" >&2
    return 1
  fi
  if [[ "${set_repo_url_norm}" != "${repo_url_norm}" ]]; then
    echo "[phase70] GitOps handoff verification failed: applicationset repoURL=${set_repo_url:-<empty>} expected=${repo_url}" >&2
    return 1
  fi
  if [[ -n "${secret_repo_url}" && "${secret_repo_url_norm}" != "${repo_url_norm}" ]]; then
    echo "[phase70] GitOps handoff verification failed: argocd-repository-bootstrap url=${secret_repo_url:-<empty>} expected=${repo_url}" >&2
    return 1
  fi
  if [[ -n "${pre_openbao_repo_url}" && "${pre_openbao_repo_url_norm}" != "${repo_url_norm}" ]]; then
    echo "[phase70] GitOps handoff verification failed: platform-pre-openbao repoURL=${pre_openbao_repo_url} expected=${repo_url}" >&2
    return 1
  fi
  if [[ -n "${pre_openbao_repo_path}" && "${pre_openbao_repo_path}" != "pods/argocd/platform/pre-openbao" ]]; then
    echo "[phase70] GitOps handoff verification failed: platform-pre-openbao path=${pre_openbao_repo_path} expected=pods/argocd/platform/pre-openbao" >&2
    return 1
  fi

  echo "[phase70] verified GitOps handoff to Gitea repo ${repo_url} (${repo_branch})"
}

echo "[phase70] repo: ${repo_root}"
echo "[phase70] running post-bootstrap healthcheck playbook"

mode_raw="$(printf '%s' "${BOOTSTRAP_BREAKGLASS}" | tr '[:upper:]' '[:lower:]')"
local_only_raw="$(printf '%s' "${BOOTSTRAP_PHASE70_LOCAL_ONLY}" | tr '[:upper:]' '[:lower:]')"
if [[ "${local_only_raw}" == "1" || "${local_only_raw}" == "true" || "${local_only_raw}" == "yes" || "${local_only_raw}" == "on" ]]; then
  ansible-playbook -i "${ANSIBLE_INVENTORY}" "${ANSIBLE_PLAYBOOK}" -e break_glass=true
elif [[ "${mode_raw}" == "1" || "${mode_raw}" == "true" || "${mode_raw}" == "yes" || "${mode_raw}" == "on" ]]; then
  ansible-playbook -i "${ANSIBLE_INVENTORY}" "${ANSIBLE_PLAYBOOK}" -e break_glass=true
else
  ansible-playbook -i "${ANSIBLE_INVENTORY}" "${ANSIBLE_PLAYBOOK}"
fi

run_phase70_step "verify GitOps handoff" critical verify_phase70_gitops_handoff
if (( phase70_critical_failures > 0 )); then
  echo "[phase70] GitOps handoff verification failed; stopping before realization checks" >&2
  exit 1
fi
run_phase70_step "verify secret-delivery foundation" critical verify_secret_delivery_foundation_phase70
if (( phase70_critical_failures > 0 )); then
  echo "[phase70] secret-delivery foundation failed; stopping before workload realization checks" >&2
  exit 1
fi
run_phase70_step "run GitOps realization checks" critical run_gitops_realization_checks_phase70
if (( phase70_critical_failures > 0 )); then
  echo "[phase70] GitOps realization checks failed; stopping before secret-delivery reconciliation" >&2
  exit 1
fi
if ! verify_openbao_secret_delivery_phase70; then
  echo "[phase70] required OpenBao-backed workload secrets did not synchronize; stopping before realization work" >&2
  exit 1
fi
run_phase70_step "reconcile requested CSI runtime rotations" critical reconcile_csi_runtime_rotations_phase70
run_phase70_step "reconcile ansible-runner" critical ensure_ansible_runner_phase70

if (( phase70_failures > 0 )); then
  echo "[phase70] completed with ${phase70_failures} failed step(s); critical=${phase70_critical_failures}" >&2
  exit 1
fi

cat <<'INFO'

Phase 70 complete (GitOps realization gate).

INFO
