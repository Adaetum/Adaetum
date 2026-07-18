#!/usr/bin/env bash
set -euo pipefail

# Phase 40 promotes OpenBao from an installed workload to the authority for
# bootstrap secrets. Earlier phases may create temporary local files; later
# phases should read durable secret material from OpenBao instead.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck disable=SC1091
. "${script_dir}/diagnostics.sh"

# Controls are grouped by responsibility: local bootstrap state, OpenBao
# readiness, optional repair actions, then profile-derived public routing.
BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR:-/var/lib/bootstrap-secrets}"
OPENBAO_NAMESPACE="${OPENBAO_NAMESPACE:-openbao}"
OPENBAO_POD="${OPENBAO_POD:-openbao-0}"
BOOTSTRAP_LOG_TAIL="${BOOTSTRAP_LOG_TAIL:-50}"
BOOTSTRAP_EVENT_TAIL="${BOOTSTRAP_EVENT_TAIL:-50}"
OPENBAO_ROLLOUT_TIMEOUT="${OPENBAO_ROLLOUT_TIMEOUT:-5m}"
PHASE40_CONNECT_TIMEOUT="${PHASE40_CONNECT_TIMEOUT:-5m}"
PHASE40_CALICO_READY_TIMEOUT="${PHASE40_CALICO_READY_TIMEOUT:-10m}"
PHASE40_CALICO_READY_DELAY="${PHASE40_CALICO_READY_DELAY:-10}"
PHASE40_CALICO_STABLE_PASSES="${PHASE40_CALICO_STABLE_PASSES:-3}"
PHASE40_CALICO_REQUEST_TIMEOUT="${PHASE40_CALICO_REQUEST_TIMEOUT:-15s}"
PHASE40_JOB_DISCOVERY_TIMEOUT="${PHASE40_JOB_DISCOVERY_TIMEOUT:-10m}"
PHASE40_JOB_COMPLETE_TIMEOUT="${PHASE40_JOB_COMPLETE_TIMEOUT:-10m}"
PHASE40_FAIL_ON_CONFIG_JOB_TIMEOUT="${PHASE40_FAIL_ON_CONFIG_JOB_TIMEOUT:-0}"
LONGHORN_NAMESPACE="${LONGHORN_NAMESPACE:-longhorn-system}"
BOOTSTRAP_INTRODUCE_OPENBAO="${BOOTSTRAP_INTRODUCE_OPENBAO:-}"
BOOTSTRAP_OPENBAO_CREATE_TOKEN_SECRET="${BOOTSTRAP_OPENBAO_CREATE_TOKEN_SECRET:-}"
BOOTSTRAP_DISABLE_PRE_OPENBAO="${BOOTSTRAP_DISABLE_PRE_OPENBAO:-}"
BOOTSTRAP_OPENBAO_AUTO_UNSEAL="${BOOTSTRAP_OPENBAO_AUTO_UNSEAL:-}"
BOOTSTRAP_LONGHORN_REPAIR="${BOOTSTRAP_LONGHORN_REPAIR:-true}"
BOOTSTRAP_LONGHORN_REPAIR_MODE="${BOOTSTRAP_LONGHORN_REPAIR_MODE:-safe}"
BOOTSTRAP_LONGHORN_FORCE_NODE_RESET="${BOOTSTRAP_LONGHORN_FORCE_NODE_RESET:-}"
BOOTSTRAP_PHASE40_MODE="${BOOTSTRAP_PHASE40_MODE:-all}"
BOOTSTRAP_BREAKGLASS_STRICT="${BOOTSTRAP_BREAKGLASS_STRICT:-0}"
CLUSTER_DOMAIN="${CLUSTER_DOMAIN:-example.services}"
CLUSTER_LOCAL_DOMAIN="${CLUSTER_LOCAL_DOMAIN:-}"
GITEA_CANONICAL_URL="${GITEA_CANONICAL_URL:-}"
KUBE_SERVICE_DOMAIN="${KUBE_SERVICE_DOMAIN:-cluster.local}"
GITEA_INTERNAL_SERVICE_NAME="${GITEA_INTERNAL_SERVICE_NAME:-gitea-http}"
GITEA_INTERNAL_URL="${GITEA_INTERNAL_URL:-http://${GITEA_INTERNAL_SERVICE_NAME}.gitea.svc.${KUBE_SERVICE_DOMAIN}:3000/}"
openbao_diag_captured=false
phase40_log_root="${BOOTSTRAP_PHASE_LOG_DIR:-/var/log/bootstrap}"
PHASE40_LOG_FILE="${PHASE40_LOG_FILE:-${phase40_log_root}/phase40.log}"
BOOTSTRAP_DIAG_PHASE="phase40"
BOOTSTRAP_DIAG_LOG_PATH="${PHASE40_LOG_FILE}"
bootstrap_diag_init
phase40_start_ts="$(date +%s)"
phase40_last_error_cmd=""
phase40_last_error_line=""

if [[ -n "${PHASE40_LOG_FILE}" ]]; then
  mkdir -p "$(dirname "${PHASE40_LOG_FILE}")"
  exec >>"${PHASE40_LOG_FILE}" 2>&1
fi

# ``--force`` and the environment switch allow an explicit recovery operator to
# retry the OpenBao introduction path; normal reruns retain conservative mode.
force=false
if [[ "${1:-}" == "--force" ]]; then
  force=true
fi
if [[ "${BOOTSTRAP_INTRODUCE_OPENBAO}" == "1" || "${BOOTSTRAP_INTRODUCE_OPENBAO}" == "true" ]]; then
  force=true
fi

phase40_mode="$(printf '%s' "${BOOTSTRAP_PHASE40_MODE}" | tr '[:upper:]' '[:lower:]')"
if [[ -z "${phase40_mode}" ]]; then
  phase40_mode="all"
fi
case "${phase40_mode}" in
  all|pre|openbao) ;;
  *)
    echo "Invalid BOOTSTRAP_PHASE40_MODE=${BOOTSTRAP_PHASE40_MODE} (expected all|pre|openbao)" >&2
    exit 1
    ;;
esac

bool_is_true() {
  [[ "${1:-}" == "1" || "${1:-}" == "true" || "${1:-}" == "yes" ]]
}

derive_local_domain() {
  local domain="${1:-}"
  python3 - <<'PY' "${domain}"
import sys
d=(sys.argv[1] if len(sys.argv) > 1 else "").strip().lower().strip(".")
if not d:
  print("")
elif d.endswith(".cloud") and d.count(".") >= 2:
  print(d[:-6] + ".local")
else:
  parts=d.split(".")
  if len(parts) > 1:
    parts[-1]="local"
    print(".".join(parts))
  else:
    print(d + ".local")
PY
}

normalize_gitea_repo_url() {
  local repo_url="${1:-}"

  if [[ -z "${repo_url}" ]] || ! printf '%s' "${repo_url}" | grep -q '/gitea-admin/cluster\.git'; then
    printf '%s' "${repo_url}"
    return 0
  fi

  printf '%sgitea-admin/cluster.git' "${GITEA_INTERNAL_URL}"
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

read_secret_file() {
  local path="$1"
  if [[ -f "${path}" ]]; then
    tr -d '\n' <"${path}"
  fi
}

write_secret_file() {
  local name="$1"
  local value="$2"
  if [[ -z "${BOOTSTRAP_SECRET_DIR:-}" ]]; then
    return 0
  fi
  mkdir -p "${BOOTSTRAP_SECRET_DIR}"
  chmod 0700 "${BOOTSTRAP_SECRET_DIR}" 2>/dev/null || true
  printf '%s\n' "${value}" > "${BOOTSTRAP_SECRET_DIR}/${name}"
  chmod 0600 "${BOOTSTRAP_SECRET_DIR}/${name}" 2>/dev/null || true
}

read_k8s_secret_key() {
  local namespace="$1"
  local secret_name="$2"
  local key="$3"
  local raw=""
  raw="$("${kubectl_bin}" -n "${namespace}" get secret "${secret_name}" -o "jsonpath={.data.${key}}" 2>/dev/null || true)"
  if [[ -n "${raw}" ]]; then
    printf '%s' "${raw}" | base64 -d 2>/dev/null || true
  fi
}

# Phase 40 is a one-way authority handoff. It may fill an application field
# that has never been promoted, but a rerun must not replace a value already
# owned (and possibly rotated) in OpenBao with an older bootstrap copy.
seed_openbao_app_fields() {
  local path="${1:?OpenBao application path is required}"
  local token="${2:?OpenBao token is required}"
  shift 2
  local pair=""
  local field=""
  local existing=""
  local -a missing=()

  for pair in "$@"; do
    field="${pair%%=*}"
    existing="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${token}" \
        bao kv get -field="${field}" "secret/apps/${path}" 2>/dev/null || true
    )"
    if [[ -z "${existing}" ]]; then
      missing+=("${pair}")
    fi
  done

  if [[ "${#missing[@]}" -eq 0 ]]; then
    echo "[phase40] preserving OpenBao-owned application fields at secret/apps/${path}"
    return 0
  fi

  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${token}" \
    bao kv patch "secret/apps/${path}" "${missing[@]}" >/dev/null 2>&1 || \
    retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${token}" \
      bao kv put "secret/apps/${path}" "${missing[@]}" >/dev/null
}

timeout_to_seconds() {
  local value="${1:-}"
  if [[ "${value}" =~ ^([0-9]+)s$ ]]; then
    echo "${BASH_REMATCH[1]}"
    return
  fi
  if [[ "${value}" =~ ^([0-9]+)m$ ]]; then
    echo "$((BASH_REMATCH[1] * 60))"
    return
  fi
  if [[ "${value}" =~ ^([0-9]+)h$ ]]; then
    echo "$((BASH_REMATCH[1] * 3600))"
    return
  fi
  echo "1200"
}

openbao_status_field() {
  local field="$1"
  local raw=""
  local tmp=""
  raw="$("${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- bao status -format=json 2>/dev/null || true)"
  if [[ -z "${raw}" ]]; then
    printf '%s\n' "unknown"
    return 0
  fi
  tmp="$(mktemp)"
  printf '%s' "${raw}" > "${tmp}"
  python3 -c 'import json,sys; field=sys.argv[1]; path=sys.argv[2]; 
try:
  data=json.load(open(path,"r",encoding="utf-8"))
except Exception:
  print("unknown"); sys.exit(0)
value=data.get(field)
if value is None:
  print("unknown")
elif isinstance(value,bool):
  print("true" if value else "false")
else:
  print(value)
' "${field}" "${tmp}"
  rm -f "${tmp}"
}

get_node_count() {
  "${kubectl_bin}" get nodes --no-headers 2>/dev/null | wc -l | tr -d '[:space:]'
}

find_longhorn_unschedulable_disk() {
  local node_name="$1"
  local out=""
  set +o pipefail
  out="$("${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get nodes.longhorn.io "${node_name}" -o json 2>/dev/null | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin)
except Exception:
  print("")
  raise SystemExit(0)

disk_status = data.get("status", {}).get("diskStatus", {}) or {}
for name, disk in disk_status.items():
  for condition in disk.get("conditions", []):
    if condition.get("type") == "Schedulable" and condition.get("status") == "False":
      print(name)
      raise SystemExit(0)
print("")
PY
)" || true
  set -o pipefail
  printf '%s\n' "${out}"
}

get_longhorn_disk_state() {
  local node_name="$1"
  local out=""
  set +o pipefail
  out="$("${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get nodes.longhorn.io "${node_name}" -o json 2>/dev/null | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin)
except Exception:
  print("unknown")
  raise SystemExit(0)

spec_disks = data.get("spec", {}).get("disks", {}) or {}
status_disks = data.get("status", {}).get("diskStatus", {}) or {}
if not spec_disks:
  print("unknown")
  raise SystemExit(0)
if not status_disks:
  print("mismatch")
  raise SystemExit(0)
if set(spec_disks.keys()) != set(status_disks.keys()):
  print("mismatch")
  raise SystemExit(0)

for name, disk in status_disks.items():
  for condition in disk.get("conditions", []):
    if condition.get("type") == "Schedulable" and condition.get("status") == "False":
      print(f"unschedulable:{name}")
      raise SystemExit(0)

print("ready")
PY
)" || true
  set -o pipefail
  printf '%s\n' "${out}"
}

relax_longhorn_disk_pressure_if_needed() {
  local node_count node_name disk_name

  node_count="$(get_node_count)"
  if [[ "${node_count}" != "1" ]]; then
    return 0
  fi

  if ! "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get nodes.longhorn.io >/dev/null 2>&1; then
    return 0
  fi

  node_name="$("${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get nodes.longhorn.io -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
  if [[ -z "${node_name}" ]]; then
    return 0
  fi

  disk_name="$(find_longhorn_unschedulable_disk "${node_name}")"
  if [[ -z "${disk_name}" ]]; then
    return 0
  fi

  echo "[phase40] Longhorn disk ${disk_name} on ${node_name} is unschedulable; relaxing disk pressure settings" >&2
  "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" patch settings.longhorn.io storage-reserved-percentage-for-default-disk \
    --type=merge -p '{"value":"0"}' >/dev/null 2>&1 || true
  "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" patch settings.longhorn.io storage-minimal-available-percentage \
    --type=merge -p '{"value":"0"}' >/dev/null 2>&1 || true
  "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" patch settings.longhorn.io storage-over-provisioning-percentage \
    --type=merge -p '{"value":"200"}' >/dev/null 2>&1 || true
  "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" patch nodes.longhorn.io "${node_name}" --type=merge \
    -p "{\"spec\":{\"disks\":{\"${disk_name}\":{\"storageReserved\":0,\"allowScheduling\":true}}}}" >/dev/null 2>&1 || true
}

restart_longhorn_components() {
  "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" rollout restart daemonset/longhorn-manager >/dev/null 2>&1 || true
  "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" delete pod -l longhorn.io/component=instance-manager --wait=false >/dev/null 2>&1 || true
  "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" rollout status daemonset/longhorn-manager --timeout=5m >/dev/null 2>&1 || true
}

wait_for_longhorn_disk_ready() {
  local node_name="$1"
  local timeout="${2:-300}"
  local deadline
  deadline="$((SECONDS + timeout))"
  while (( SECONDS < deadline )); do
    local state
    state="$(get_longhorn_disk_state "${node_name}")"
    if [[ "${state}" == "ready" ]]; then
      return 0
    fi
    sleep 10
  done
  return 1
}

repair_longhorn_if_unready() {
  local node_count node_name state mode

  node_count="$(get_node_count)"
  if [[ "${node_count}" != "1" ]]; then
    return 0
  fi

  if ! "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get nodes.longhorn.io >/dev/null 2>&1; then
    return 0
  fi

  node_name="$("${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get nodes.longhorn.io -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
  if [[ -z "${node_name}" ]]; then
    return 0
  fi

  mode="${BOOTSTRAP_LONGHORN_REPAIR_MODE}"

  state="$(get_longhorn_disk_state "${node_name}")"
  if [[ "${state}" == "ready" ]]; then
    return 0
  fi

  if [[ "${state}" == "unknown" && "${mode}" != "restart" && "${mode}" != "reset" ]]; then
    echo "[phase40] Longhorn disk state unknown; skipping recovery (mode=${mode})." >&2
    return 0
  fi

  echo "[phase40] Longhorn disk state ${state}; attempting recovery (mode=${mode})" >&2
  relax_longhorn_disk_pressure_if_needed
  if [[ "${mode}" == "restart" || "${mode}" == "reset" ]]; then
    restart_longhorn_components
  else
    return 0
  fi

  if [[ "${state}" == "mismatch" && "${mode}" == "reset" ]] && bool_is_true "${BOOTSTRAP_LONGHORN_FORCE_NODE_RESET}"; then
    echo "[phase40] Forcing Longhorn node reset for ${node_name}" >&2
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" delete nodes.longhorn.io "${node_name}" --wait=false >/dev/null 2>&1 || true
    sleep 5
    restart_longhorn_components
  fi

  if wait_for_longhorn_disk_ready "${node_name}" 300; then
    echo "[phase40] Longhorn disk ready after recovery" >&2
  else
    state="$(get_longhorn_disk_state "${node_name}")"
    echo "[phase40] Longhorn disk still not ready after recovery: ${state}" >&2
  fi
}

ensure_openbao_volume_replicas() {
  local ns="${OPENBAO_NAMESPACE}"
  local pod="${OPENBAO_POD}"
  local pvc_name=""
  local volume_name=""
  local replicas=""
  local node_count=""
  local desired_replicas="1"

  node_count="$(get_node_count)"
  if [[ "${node_count}" != "1" ]]; then
    return 0
  fi

  pvc_name="$("${kubectl_bin}" -n "${ns}" get pod "${pod}" -o jsonpath='{.spec.volumes[?(@.persistentVolumeClaim)].persistentVolumeClaim.claimName}' 2>/dev/null || true)"
  if [[ -z "${pvc_name}" ]]; then
    return 0
  fi

  volume_name="$("${kubectl_bin}" -n "${ns}" get pvc "${pvc_name}" -o jsonpath='{.spec.volumeName}' 2>/dev/null || true)"
  if [[ -z "${volume_name}" ]]; then
    return 0
  fi

  replicas="$("${kubectl_bin}" -n longhorn-system get volumes.longhorn.io "${volume_name}" -o jsonpath='{.spec.numberOfReplicas}' 2>/dev/null || true)"
  if [[ -n "${replicas}" && "${replicas}" != "${desired_replicas}" ]]; then
    echo "[phase40] single-node detected; patching Longhorn volume ${volume_name} replicas ${replicas} -> ${desired_replicas}" >&2
    "${kubectl_bin}" -n longhorn-system patch volumes.longhorn.io "${volume_name}" \
      --type=merge -p "{\"spec\":{\"numberOfReplicas\":${desired_replicas}}}" >/dev/null 2>&1 || true
  fi
}

wait_for_cluster() {
  local timeout_seconds
  local deadline
  timeout_seconds="$(timeout_to_seconds "${PHASE40_CONNECT_TIMEOUT}")"
  deadline="$((SECONDS + timeout_seconds))"

  while (( SECONDS < deadline )); do
    if "${kubectl_bin}" get ns --request-timeout=10s >/dev/null 2>&1; then
      return 0
    fi
    sleep 5
  done

  echo "[phase40] cluster connectivity check timed out after ${PHASE40_CONNECT_TIMEOUT}" >&2
  return 1
}

pod_is_ready() {
  local namespace="$1"
  local selector="$2"
  local pod_json=""

  pod_json="$("${kubectl_bin}" -n "${namespace}" get pods -l "${selector}" -o json 2>/dev/null || true)"
  if [[ -z "${pod_json}" ]]; then
    return 1
  fi

  python3 - <<'PY' "${pod_json}"
import json
import sys

try:
    data = json.loads(sys.argv[1])
except Exception:
    raise SystemExit(1)

items = data.get("items") or []
if not items:
    raise SystemExit(1)

for pod in items:
    conditions = pod.get("status", {}).get("conditions") or []
    if not any(c.get("type") == "Ready" and c.get("status") == "True" for c in conditions):
        raise SystemExit(1)

raise SystemExit(0)
PY
}

wait_for_calico_readiness() {
  local timeout_seconds
  local deadline
  local calico_ns="calico-system"
  local stable_passes_required
  local stable_passes=0
  local attempts=0
  local request_timeout="${PHASE40_CALICO_REQUEST_TIMEOUT}"

  if ! "${kubectl_bin}" get ns "${calico_ns}" >/dev/null 2>&1; then
    return 0
  fi

  timeout_seconds="$(timeout_to_seconds "${PHASE40_CALICO_READY_TIMEOUT}")"
  deadline="$((SECONDS + timeout_seconds))"
  stable_passes_required="${PHASE40_CALICO_STABLE_PASSES}"
  if ! [[ "${stable_passes_required}" =~ ^[0-9]+$ ]] || (( stable_passes_required < 1 )); then
    stable_passes_required=3
  fi

  while (( SECONDS < deadline )); do
    attempts=$((attempts + 1))

    if ! "${kubectl_bin}" get ns "${calico_ns}" --request-timeout="${request_timeout}" >/dev/null 2>&1; then
      stable_passes=0
      sleep "${PHASE40_CALICO_READY_DELAY}"
      continue
    fi

    if ! pod_is_ready "${calico_ns}" 'k8s-app=calico-node'; then
      stable_passes=0
      sleep "${PHASE40_CALICO_READY_DELAY}"
      continue
    fi

    if ! pod_is_ready "${calico_ns}" 'k8s-app=calico-kube-controllers'; then
      stable_passes=0
      sleep "${PHASE40_CALICO_READY_DELAY}"
      continue
    fi

    if "${kubectl_bin}" -n "${calico_ns}" get deploy calico-typha >/dev/null 2>&1; then
      if ! "${kubectl_bin}" -n "${calico_ns}" rollout status deploy/calico-typha --timeout="${request_timeout}" >/dev/null 2>&1; then
        stable_passes=0
        sleep "${PHASE40_CALICO_READY_DELAY}"
        continue
      fi
    fi

    if ! "${kubectl_bin}" get --raw=/readyz >/dev/null 2>&1; then
      stable_passes=0
      sleep "${PHASE40_CALICO_READY_DELAY}"
      continue
    fi

    stable_passes=$((stable_passes + 1))
    echo "[phase40] calico readiness sample ${stable_passes}/${stable_passes_required} passed" >&2
    if (( stable_passes >= stable_passes_required )); then
      echo "[phase40] calico and kube API readiness checks passed" >&2
      return 0
    fi

    sleep "${PHASE40_CALICO_READY_DELAY}"
  done

  echo "[phase40] calico readiness check timed out after ${PHASE40_CALICO_READY_TIMEOUT}" >&2
  return 1
}

capture_openbao_diagnostics() {
  openbao_diag_captured=true
  local ns="${OPENBAO_NAMESPACE}"
  local pod="${OPENBAO_POD}"
  local pvc_name=""
  local volume_name=""
  local evidence_path=""
  evidence_path="$(bootstrap_diag_capture_evidence_path "openbao" "debug")"

  {
    echo "[phase40] diagnostics: cluster summary"
    "${kubectl_bin}" get nodes -o wide || true
    "${kubectl_bin}" get pods -A -o wide || true

    echo "[phase40] diagnostics: pods"
    "${kubectl_bin}" -n "${ns}" get pods -o wide || true
    echo "[phase40] diagnostics: pod describe (${ns}/${pod})"
    "${kubectl_bin}" -n "${ns}" describe pod "${pod}" || true
    echo "[phase40] diagnostics: pod logs (${ns}/${pod})"
    "${kubectl_bin}" -n "${ns}" logs "${pod}" --all-containers --tail="${BOOTSTRAP_LOG_TAIL}" || true
    echo "[phase40] diagnostics: pvc list (${ns})"
    "${kubectl_bin}" -n "${ns}" get pvc -o wide || true

    pvc_name="$("${kubectl_bin}" -n "${ns}" get pod "${pod}" -o jsonpath='{.spec.volumes[?(@.persistentVolumeClaim)].persistentVolumeClaim.claimName}' 2>/dev/null || true)"
    if [[ -n "${pvc_name}" ]]; then
      echo "[phase40] diagnostics: pvc describe (${ns}/${pvc_name})"
      "${kubectl_bin}" -n "${ns}" describe pvc "${pvc_name}" || true
      volume_name="$("${kubectl_bin}" -n "${ns}" get pvc "${pvc_name}" -o jsonpath='{.spec.volumeName}' 2>/dev/null || true)"
      if [[ -n "${volume_name}" ]]; then
        echo "[phase40] diagnostics: longhorn volume (${volume_name})"
        "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get volumes.longhorn.io "${volume_name}" -o yaml || true
        "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" describe volumes.longhorn.io "${volume_name}" || true
        echo "[phase40] diagnostics: longhorn replicas (${volume_name})"
        "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get replicas.longhorn.io -l "longhornvolume=${volume_name}" -o wide || true
        "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" describe replicas.longhorn.io -l "longhornvolume=${volume_name}" || true
      fi
    fi

    echo "[phase40] diagnostics: events (${ns})"
    "${kubectl_bin}" -n "${ns}" get events --sort-by=.lastTimestamp | tail -n "${BOOTSTRAP_EVENT_TAIL}" || true
    echo "[phase40] diagnostics: events (longhorn-system)"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get events --sort-by=.lastTimestamp | tail -n "${BOOTSTRAP_EVENT_TAIL}" || true

    echo "[phase40] diagnostics: longhorn pods"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get pods -o wide || true
    echo "[phase40] diagnostics: longhorn nodes"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get nodes.longhorn.io -o yaml || true
    echo "[phase40] diagnostics: longhorn engine images"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get engineimages -o wide || true
    echo "[phase40] diagnostics: longhorn settings"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get settings.longhorn.io -o wide || true
    echo "[phase40] diagnostics: longhorn volumes"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" get volumes.longhorn.io -o wide || true
    echo "[phase40] diagnostics: longhorn manager logs"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" logs -l app=longhorn-manager --all-containers --tail="${BOOTSTRAP_LOG_TAIL}" || true
    echo "[phase40] diagnostics: longhorn driver deployer logs"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" logs -l app=longhorn-driver-deployer --all-containers --tail="${BOOTSTRAP_LOG_TAIL}" || true
    echo "[phase40] diagnostics: longhorn csi plugin logs"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" logs -l app=longhorn-csi-plugin --all-containers --tail="${BOOTSTRAP_LOG_TAIL}" || true
    echo "[phase40] diagnostics: longhorn instance manager logs"
    "${kubectl_bin}" -n "${LONGHORN_NAMESPACE}" logs -l longhorn.io/component=instance-manager --all-containers --tail="${BOOTSTRAP_LOG_TAIL}" || true

    echo "[phase40] diagnostics: calico status"
    "${kubectl_bin}" -n calico-system get pods -o wide || \
      "${kubectl_bin}" -n kube-system get pods -o wide || true
    "${kubectl_bin}" -n calico-system logs -l k8s-app=calico-node --all-containers --tail="${BOOTSTRAP_LOG_TAIL}" || \
      "${kubectl_bin}" -n kube-system logs -l k8s-app=calico-node --all-containers --tail="${BOOTSTRAP_LOG_TAIL}" || true
  } | tee -a "${evidence_path}" >&2

  bootstrap_diag_record_file_event \
    "openbao" \
    "openbao-debug" \
    "captured OpenBao and Longhorn diagnostics" \
    "${evidence_path}" \
    "warning" \
    "kubernetes-api"
}

capture_openbao_status() {
  local ns="${OPENBAO_NAMESPACE}"
  echo "[phase40] diagnostics: openbao status summary (${ns})" >&2
  "${kubectl_bin}" -n "${ns}" get statefulset -o wide >&2 || true
  "${kubectl_bin}" -n "${ns}" get pods -o wide >&2 || true
  "${kubectl_bin}" -n "${ns}" get svc,ep -o wide >&2 || true
  "${kubectl_bin}" -n "${ns}" get pvc -o wide >&2 || true
  "${kubectl_bin}" -n "${ns}" get events --sort-by=.lastTimestamp >&2 || true
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
cd "${repo_root}"

manifest_dir="${repo_root}/pods/secrets/openbao/manifests"
config_dir="${repo_root}/pods/secrets/openbao/config"
init_script="${repo_root}/pods/secrets/openbao/init/init-openbao.sh"

strict_breakglass="$(printf '%s' "${BOOTSTRAP_BREAKGLASS_STRICT}" | tr '[:upper:]' '[:lower:]')"
if [[ ! -d "${manifest_dir}" || ! -d "${config_dir}" || ! -f "${init_script}" ]]; then
  echo "[phase40] OpenBao manifests not found in repo bundle." >&2
  echo "[phase40] Expected: ${manifest_dir}" >&2
  echo "[phase40] Missing init script or manifests; cannot initialize OpenBao." >&2
  if [[ "${strict_breakglass}" == "1" || "${strict_breakglass}" == "true" ]]; then
    echo "[phase40] Strict break-glass mode is enabled; failing Phase 40." >&2
    exit 1
  fi
  echo "[phase40] Skipping Phase 40 in non-strict mode; run from full repo checkout or ensure pods/ is bundled." >&2
  exit 0
fi

if [[ -z "${KUBECONFIG:-}" ]]; then
  if [[ -f /etc/rancher/rke2/rke2.yaml ]]; then
    export KUBECONFIG=/etc/rancher/rke2/rke2.yaml
  fi
fi

kubectl_bin=""
if command -v kubectl >/dev/null 2>&1; then
  kubectl_bin="$(command -v kubectl)"
elif [[ -x /var/lib/rancher/rke2/bin/kubectl ]]; then
  kubectl_bin="/var/lib/rancher/rke2/bin/kubectl"
else
  echo "kubectl not found; Phase 40 requires kubectl access to the cluster." >&2
  exit 1
fi

if [[ -z "${CLUSTER_LOCAL_DOMAIN}" ]]; then
  CLUSTER_LOCAL_DOMAIN="$(derive_local_domain "${CLUSTER_DOMAIN}")"
fi

phase40_exit_trap() {
  local rc=$?
  local end_ts duration
  end_ts="$(date +%s)"
  duration="$((end_ts - phase40_start_ts))"
  # Never let diagnostics override the original failure.
  set +e
  set +o pipefail
  if [[ "${rc}" -ne 0 ]]; then
    if [[ "${openbao_diag_captured}" != "true" ]] && "${kubectl_bin}" get ns "${OPENBAO_NAMESPACE}" >/dev/null 2>&1; then
      echo "[phase40] failure detected; capturing OpenBao diagnostics..." >&2
      capture_openbao_diagnostics
      capture_openbao_status
    fi
  fi
  if [[ "${rc}" -ne 0 ]]; then
    echo "[phase40] exiting with rc=${rc}" >&2
    bootstrap_diag_record \
      "phase=phase40" \
      "step=phase40" \
      "component=openbao" \
      "operation=run-failed" \
      "severity=error" \
      "exit_code=${rc}" \
      "duration_seconds=${duration}" \
      "failure_kind=$(bootstrap_diag_classify_failure_kind "${phase40_last_error_cmd}")" \
      "summary=phase40 failed at line ${phase40_last_error_line:-unknown}: ${phase40_last_error_cmd:-unknown}" \
      "log_path=${PHASE40_LOG_FILE}" \
      "evidence_paths=${BOOTSTRAP_DIAGNOSTICS_RUN_DIR}"
  else
    bootstrap_diag_record \
      "phase=phase40" \
      "step=phase40" \
      "component=openbao" \
      "operation=run-complete" \
      "severity=info" \
      "exit_code=0" \
      "duration_seconds=${duration}" \
      "summary=phase40 complete" \
      "log_path=${PHASE40_LOG_FILE}"
  fi
  exit "${rc}"
}
trap 'phase40_exit_trap' EXIT

phase40_err_trap() {
  local rc=$?
  local line_no="${BASH_LINENO[0]:-unknown}"
  local cmd="${BASH_COMMAND:-unknown}"
  phase40_last_error_line="${line_no}"
  phase40_last_error_cmd="${cmd}"
  echo "[phase40] error: command failed at line ${line_no} (rc=${rc}): ${cmd}" >&2
  return "${rc}"
}
trap 'phase40_err_trap' ERR

wait_for_argo_application_crd() {
  local timeout_seconds
  local deadline
  timeout_seconds="$(timeout_to_seconds "${1:-120s}")"
  deadline="$((SECONDS + timeout_seconds))"
  while (( SECONDS < deadline )); do
    if "${kubectl_bin}" get crd applications.argoproj.io >/dev/null 2>&1; then
      return 0
    fi
    sleep 5
  done
  return 1
}

wait_for_namespace() {
  local namespace="$1"
  local timeout_seconds
  local deadline
  timeout_seconds="$(timeout_to_seconds "${2:-120s}")"
  deadline="$((SECONDS + timeout_seconds))"
  while (( SECONDS < deadline )); do
    if "${kubectl_bin}" get ns "${namespace}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 5
  done
  return 1
}

wait_for_optional_service_endpoints() {
  local namespace="$1"
  local service_name="$2"
  local timeout_seconds
  local deadline
  local endpoint_ip=""
  timeout_seconds="$(timeout_to_seconds "${3:-180s}")"

  if ! "${kubectl_bin}" -n "${namespace}" get service "${service_name}" >/dev/null 2>&1; then
    return 0
  fi

  deadline="$((SECONDS + timeout_seconds))"
  while (( SECONDS < deadline )); do
    endpoint_ip="$("${kubectl_bin}" -n "${namespace}" get endpoints "${service_name}" -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null || true)"
    if [[ -n "${endpoint_ip}" ]]; then
      return 0
    fi
    sleep 5
  done
  return 1
}

apply_openbao_manifests_direct() {
  local apply_log=""
  local attempt=""
  local max_attempts=6
  apply_log="$(mktemp /tmp/openbao-apply.XXXXXX.log)"
  trap 'rm -f "${apply_log}" >/dev/null 2>&1 || true' RETURN

  for attempt in $(seq 1 "${max_attempts}"); do
    if "${kubectl_bin}" apply -k "${manifest_dir}" >"${apply_log}" 2>&1; then
      cat "${apply_log}"
      return 0
    fi

    cat "${apply_log}" >&2 || true
    if grep -q 'failed calling webhook "rancher.cattle.io.namespaces.create-non-kubesystem"' "${apply_log}" || \
       grep -q 'no endpoints available for service "rancher-webhook"' "${apply_log}"; then
      echo "[phase40] rancher-webhook not ready during OpenBao apply (attempt ${attempt}/${max_attempts}); waiting before retry" >&2
      wait_for_optional_service_endpoints cattle-system rancher-webhook "180s" || true
      sleep 5
      continue
    fi

    return 1
  done

  return 1
}

echo "[phase40] repo: ${repo_root}"
echo "[phase40] bootstrap secrets: ${BOOTSTRAP_SECRET_DIR}"
bootstrap_diag_record \
  "phase=phase40" \
  "step=phase40" \
  "component=openbao" \
  "operation=run-start" \
  "severity=info" \
  "summary=phase40 starting" \
  "log_path=${PHASE40_LOG_FILE}"

echo "[phase40] checking cluster connectivity"
"${kubectl_bin}" version --client >/dev/null
wait_for_cluster
wait_for_calico_readiness
relax_longhorn_disk_pressure_if_needed
if bool_is_true "${BOOTSTRAP_LONGHORN_REPAIR}"; then
  repair_longhorn_if_unready
fi

use_argo=true
if ! "${kubectl_bin}" get ns argocd >/dev/null 2>&1; then
  use_argo=false
  echo "[phase40] argocd namespace not found; using direct OpenBao manifests." >&2
elif ! wait_for_argo_application_crd "30s"; then
  use_argo=false
  echo "[phase40] Argo CD Application CRD not ready; using direct OpenBao manifests." >&2
elif ! "${kubectl_bin}" -n argocd get application platform-pre-openbao >/dev/null 2>&1; then
  use_argo=false
  echo "[phase40] platform-pre-openbao application not found; using direct OpenBao manifests." >&2
fi

if ! "${kubectl_bin}" get ns "${OPENBAO_NAMESPACE}" >/dev/null 2>&1; then
  if [[ "${phase40_mode}" == "openbao" ]]; then
    echo "openbao namespace not found; OpenBao-only mode requires a preflight run." >&2
    echo "Run: BOOTSTRAP_PHASE40_MODE=pre ${0}" >&2
    exit 1
  fi
  if [[ "${use_argo}" == true ]]; then
    echo "[phase40] waiting for pre-openbao Argo CD app to create namespace ${OPENBAO_NAMESPACE}" >&2
    if ! wait_for_namespace "${OPENBAO_NAMESPACE}" "180s"; then
      echo "[phase40] pre-openbao Argo CD app did not create namespace ${OPENBAO_NAMESPACE} in time; using direct OpenBao manifests." >&2
      use_argo=false
    fi
  fi
  if [[ "${use_argo}" != true ]]; then
    echo "[phase40] applying OpenBao manifests directly"
    if ! wait_for_optional_service_endpoints cattle-system rancher-webhook "180s"; then
      echo "[phase40] warning: rancher-webhook service still has no endpoints after waiting; attempting OpenBao apply anyway" >&2
    fi
    apply_openbao_manifests_direct
  fi
fi

ensure_openbao_volume_replicas

echo "[phase40] waiting for OpenBao pod to start (not readiness-gated by seal): ${OPENBAO_NAMESPACE}/${OPENBAO_POD}"
start_deadline="$((SECONDS + $(timeout_to_seconds "${OPENBAO_ROLLOUT_TIMEOUT}")))"
while (( SECONDS < start_deadline )); do
  if ! "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get pod "${OPENBAO_POD}" >/dev/null 2>&1; then
    sleep 5
    continue
  fi
  pod_phase="$("${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get pod "${OPENBAO_POD}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  pod_running="$("${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get pod "${OPENBAO_POD}" -o jsonpath='{.status.containerStatuses[0].state.running.startedAt}' 2>/dev/null || true)"
  if [[ "${pod_phase}" == "Running" && -n "${pod_running}" ]]; then
    break
  fi
  sleep 5
done
if (( SECONDS >= start_deadline )); then
  echo "[phase40] OpenBao pod did not start within ${OPENBAO_ROLLOUT_TIMEOUT}. Capturing diagnostics..." >&2
  capture_openbao_diagnostics
  if [[ "${strict_breakglass}" == "1" || "${strict_breakglass}" == "true" ]]; then
    exit 1
  fi
  echo "[phase40] OpenBao startup timeout in non-strict mode; continuing bootstrap (best effort)." >&2
  exit 0
fi

if [[ "${phase40_mode}" == "pre" ]]; then
  echo "[phase40] preflight complete; skipping OpenBao init/unseal (mode=pre)" >&2
  exit 0
fi

cat <<'INFO'

Phase 40 introduces OpenBao as the secrets authority:
1) Initialize OpenBao (one-time) and store unseal keys offline.
2) Unseal OpenBao (threshold number of keys).
3) Create the bootstrap token secret in-cluster.
4) Apply post-OpenBao config (policies/auth/roles).

INFO

if [[ "${force}" != true ]]; then
  echo "Refusing to proceed without explicit confirmation flag." >&2
  echo "Re-run with: BOOTSTRAP_INTRODUCE_OPENBAO=1 ${0}  (or pass --force)" >&2
  exit 2
fi

echo "[phase40] initializing OpenBao (writes ${BOOTSTRAP_SECRET_DIR}/openbao-init.json)"
BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR}" OPENBAO_NAMESPACE="${OPENBAO_NAMESPACE}" OPENBAO_POD="${OPENBAO_POD}" KUBECTL_BIN="${kubectl_bin}" \
  bash "${init_script}"

init_file="${BOOTSTRAP_SECRET_DIR}/openbao-init.json"
if [[ ! -f "${init_file}" ]]; then
  echo "Missing init output: ${init_file}" >&2
  exit 1
fi

sealed="$(openbao_status_field sealed)"

if [[ "${sealed}" == "true" || "${sealed}" == "unknown" ]]; then
  if bool_is_true "${BOOTSTRAP_OPENBAO_AUTO_UNSEAL}"; then
    echo "[phase40] OpenBao sealed; attempting automatic unseal using local init output"
    python3 - <<PY
import json, subprocess

init_file="${init_file}"
ns="${OPENBAO_NAMESPACE}"
pod="${OPENBAO_POD}"

with open(init_file, "r", encoding="utf-8") as f:
    data=json.load(f)

keys = data.get("unseal_keys_b64") or data.get("unseal_keys") or []
if not isinstance(keys, list) or len(keys) < 3:
    raise SystemExit("missing unseal keys in init output")

for key in keys[:3]:
    subprocess.check_call(["${kubectl_bin}","-n",ns,"exec","-i",pod,"--","bao","operator","unseal",key])
PY

    sealed2="$(openbao_status_field sealed)"
    if [[ "${sealed2}" != "false" ]]; then
      echo "[phase40] OpenBao still sealed after auto-unseal attempt." >&2
      exit 3
    fi
    echo "[phase40] OpenBao unsealed"
  else
    cat <<'INFO'

OpenBao is still sealed.

Unseal OpenBao (repeat until unsealed; requires threshold number of keys):
  "${kubectl_bin}" -n openbao exec -it openbao-0 -- bao operator unseal

Then re-run Phase 40 to finish enabling post-OpenBao config.

INFO
    exit 3
  fi
fi

root_token="$(python3 - <<PY
import json
with open("${init_file}", "r", encoding="utf-8") as f:
    print(json.load(f).get("root_token",""))
PY
)"

if [[ -z "${root_token}" ]]; then
  echo "Could not read root_token from ${init_file}" >&2
  exit 1
fi

if "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token >/dev/null 2>&1; then
  echo "[phase40] openbao-bootstrap-token already exists; skipping secret creation"
else
  if bool_is_true "${BOOTSTRAP_OPENBAO_CREATE_TOKEN_SECRET}" || [[ "${force}" == true ]]; then
    "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" create secret generic openbao-bootstrap-token \
      --from-literal=token="${root_token}"
    echo "[phase40] created openbao-bootstrap-token"
  else
    echo "[phase40] openbao-bootstrap-token is missing."
    echo "[phase40] Set BOOTSTRAP_OPENBAO_CREATE_TOKEN_SECRET=1 (or pass --force) to create it automatically."
    exit 4
  fi
fi

rancher_hostname="${RANCHER_PUBLIC_DOMAIN:-${RANCHER_HOSTNAME:-}}"
if [[ -z "${rancher_hostname}" ]]; then
  rancher_hostname="$(
    "${kubectl_bin}" -n cattle-system get ingress rancher -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || true
  )"
fi
# Normalize potential URL input (e.g. https://rancher.example.com/path) to hostname only.
rancher_hostname="${rancher_hostname#http://}"
rancher_hostname="${rancher_hostname#https://}"
rancher_hostname="${rancher_hostname%%/*}"

rancher_password="${RANCHER_ADMIN_PASSWORD:-}"
if [[ -z "${rancher_password}" && -n "${BOOTSTRAP_SECRET_DIR:-}" ]]; then
  rancher_password="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/rancher_admin_password")"
fi
if [[ -z "${rancher_password}" ]]; then
  if "${kubectl_bin}" -n cattle-system get deployment rancher >/dev/null 2>&1; then
    echo "[phase40] waiting for Rancher deployment to be available"
    "${kubectl_bin}" -n cattle-system rollout status deployment/rancher --timeout=10m || true
  fi
  for _ in {1..30}; do
    rancher_password_b64="$(
      "${kubectl_bin}" -n cattle-system get secret bootstrap-secret -o jsonpath='{.data.bootstrapPassword}' 2>/dev/null || true
    )"
    if [[ -n "${rancher_password_b64}" ]]; then
      rancher_password="$(printf '%s' "${rancher_password_b64}" | base64 -d)"
      break
    fi
    sleep 10
  done
fi

if [[ -n "${rancher_password}" ]]; then
  echo "[phase40] storing Rancher bootstrap credentials in OpenBao"
  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao secrets enable -path=secret kv-v2 >/dev/null 2>&1 || \
    retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao secrets tune -version=2 secret >/dev/null 2>&1 || true

  rancher_kv_args=("bootstrap_password=${rancher_password}")
  if [[ -n "${rancher_hostname}" ]]; then
    rancher_kv_args+=("hostname=${rancher_hostname}" "url=https://${rancher_hostname}")
  fi

  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao kv put secret/bootstrap/rancher "${rancher_kv_args[@]}"
  seed_openbao_app_fields rancher/admin "${root_token}" \
    "${rancher_kv_args[@]}"
  echo "[phase40] Rancher bootstrap credentials stored in OpenBao"
else
  echo "[phase40] Rancher bootstrap credentials missing; skipping OpenBao write."
fi

argocd_admin_password="${ARGOCD_ADMIN_PASSWORD:-}"
if [[ -z "${argocd_admin_password}" && -n "${BOOTSTRAP_SECRET_DIR:-}" ]]; then
  argocd_admin_password="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_admin_password")"
fi

argocd_repo_url="${ARGOCD_REPO_URL:-}"
argocd_repo_username="${ARGOCD_REPO_USERNAME:-}"
argocd_repo_token="${ARGOCD_REPO_TOKEN:-}"
argocd_repo_branch="${ARGOCD_REPO_BRANCH:-}"
argocd_repo_source="custom"
gitea_repo_username_effective="$(read_k8s_secret_key gitea gitea-admin-secret username)"
gitea_repo_password_effective="$(read_k8s_secret_key gitea gitea-admin-secret password)"

if [[ -z "${gitea_repo_username_effective}" ]]; then
  gitea_repo_username_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_admin_username")"
fi
if [[ -z "${gitea_repo_username_effective}" ]]; then
  gitea_repo_username_effective="gitea-admin"
fi
if [[ -z "${gitea_repo_password_effective}" ]]; then
  gitea_repo_password_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_admin_password")"
fi

if [[ -n "${gitea_repo_username_effective}" ]]; then
  write_secret_file gitea_admin_username "${gitea_repo_username_effective}"
fi
if [[ -n "${gitea_repo_password_effective}" ]]; then
  write_secret_file gitea_admin_password "${gitea_repo_password_effective}"
fi

# If Phase 30 seeded Gitea (break-glass), it will write Argo repo settings into
# ${BOOTSTRAP_SECRET_DIR}/argocd_repo_*. Prefer those over GitHub defaults so
# Argo CD points at Gitea.
if [[ -n "${BOOTSTRAP_SECRET_DIR:-}" ]]; then
  if [[ -z "${argocd_repo_url}" ]]; then
    seed_repo_url="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_url")"
    if [[ -n "${seed_repo_url}" ]]; then
      argocd_repo_url="${seed_repo_url}"
      argocd_repo_source="seed"
    fi
  fi
  if [[ -z "${argocd_repo_username}" ]]; then
    seed_repo_user="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_username")"
    if [[ -n "${seed_repo_user}" ]]; then
      argocd_repo_username="${seed_repo_user}"
      argocd_repo_source="seed"
    fi
  fi
  if [[ -z "${argocd_repo_token}" ]]; then
    seed_repo_token="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_token")"
    if [[ -n "${seed_repo_token}" ]]; then
      argocd_repo_token="${seed_repo_token}"
      argocd_repo_source="seed"
    fi
  fi
  if [[ -z "${argocd_repo_branch}" ]]; then
    seed_repo_branch="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_branch")"
    if [[ -n "${seed_repo_branch}" ]]; then
      argocd_repo_branch="${seed_repo_branch}"
      argocd_repo_source="seed"
    fi
  fi
  if [[ -n "${argocd_repo_url}" ]] && printf '%s' "${argocd_repo_url}" | grep -q '/gitea-admin/cluster\.git'; then
    argocd_repo_url="$(normalize_gitea_repo_url "${argocd_repo_url}")"
    if [[ -n "${gitea_repo_username_effective}" ]]; then
      argocd_repo_username="${gitea_repo_username_effective}"
      argocd_repo_source="seed"
    fi
  fi
fi

if [[ -n "${argocd_repo_url}" ]] && [[ "${argocd_repo_source}" == "seed" ]]; then
  write_secret_file argocd_repo_url "${argocd_repo_url}"
  if [[ -n "${argocd_repo_username}" ]]; then
    write_secret_file argocd_repo_username "${argocd_repo_username}"
  fi
  if [[ -n "${argocd_repo_token}" ]]; then
    write_secret_file argocd_repo_token "${argocd_repo_token}"
  fi
  if [[ -n "${argocd_repo_branch}" ]]; then
    write_secret_file argocd_repo_branch "${argocd_repo_branch}"
  fi
fi

if [[ -z "${argocd_repo_url}" ]]; then
  echo "[phase40] no Gitea-backed ARGOCD_REPO_URL is available yet; skipping Argo CD repo URL persistence." >&2
fi

if [[ "${argocd_repo_source}" == "github" ]]; then
  echo "[phase40] refusing to persist GitHub as the Argo CD repo source; bootstrap must use the seeded Gitea mirror." >&2
  argocd_repo_url=""
  argocd_repo_username=""
  argocd_repo_token=""
  argocd_repo_branch=""
  argocd_repo_source="custom"
fi

if [[ -n "${argocd_repo_token}" && -z "${argocd_repo_username}" ]]; then
  argocd_repo_username="oauth2"
fi

argocd_kv_args=()
if [[ -n "${argocd_admin_password}" ]]; then
  argocd_kv_args+=("admin_password=${argocd_admin_password}")
fi
if [[ -n "${argocd_repo_url}" ]]; then
  argocd_kv_args+=("repo_url=${argocd_repo_url}")
fi
if [[ -n "${argocd_repo_username}" ]]; then
  argocd_kv_args+=("repo_username=${argocd_repo_username}")
fi
if [[ -n "${argocd_repo_token}" ]]; then
  argocd_kv_args+=("repo_token=${argocd_repo_token}")
fi
if [[ -n "${argocd_repo_branch}" ]]; then
  argocd_kv_args+=("repo_branch=${argocd_repo_branch}")
fi

if [[ "${#argocd_kv_args[@]}" -gt 0 ]]; then
  echo "[phase40] storing Argo CD bootstrap credentials in OpenBao"
  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao secrets enable -path=secret kv-v2 >/dev/null 2>&1 || \
    retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao secrets tune -version=2 secret >/dev/null 2>&1 || true

  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao kv put secret/bootstrap/argocd "${argocd_kv_args[@]}"
  if [[ -n "${argocd_repo_url}" && -n "${argocd_repo_token}" ]]; then
    seed_openbao_app_fields argocd/repository "${root_token}" \
      "url=${argocd_repo_url}" \
      "username=${argocd_repo_username}" \
      "password=${argocd_repo_token}" \
      "branch=${argocd_repo_branch:-HEAD}"
  fi
  if [[ -n "${argocd_admin_password}" ]]; then
    seed_openbao_app_fields argocd/admin "${root_token}" \
      "password=${argocd_admin_password}"
  fi
  echo "[phase40] Argo CD bootstrap credentials stored in OpenBao"
else
  echo "[phase40] Argo CD bootstrap credentials missing; skipping OpenBao write."
fi

# Argo CD's session-signing key is arbitrary runtime material. Seed it before
# installation so recovery never depends on a controller-generated value that
# existed only in the cluster's argocd-secret.
argocd_server_secret_key_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_server_secret_key")"
argocd_redis_password_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_redis_password")"
if [[ -n "${argocd_server_secret_key_val}" || -n "${argocd_redis_password_val}" ]]; then
  argocd_runtime_args=()
  if [[ -n "${argocd_server_secret_key_val}" ]]; then
    argocd_runtime_args+=("server_secret_key=${argocd_server_secret_key_val}")
  fi
  if [[ -n "${argocd_redis_password_val}" ]]; then
    argocd_runtime_args+=("redis_password=${argocd_redis_password_val}")
  fi
  seed_openbao_app_fields argocd/runtime "${root_token}" \
    "${argocd_runtime_args[@]}"
fi

# Store auto-generated bootstrap secrets in OpenBao so Phase 60 can burn local copies.
# These are generated by Phase 20/30 and are used to bootstrap initial admin access.
platform_kv_args=()
rke2_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/rke2_token")"
if [[ -n "${rke2_token_val}" ]]; then
  platform_kv_args+=("rke2_token=${rke2_token_val}")
fi
gitea_admin_password_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_admin_password")"
if [[ -n "${gitea_admin_password_val}" ]]; then
  platform_kv_args+=("gitea_admin_password=${gitea_admin_password_val}")
fi
gitea_runner_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_runner_token")"
if [[ -n "${gitea_runner_token_val}" ]]; then
  platform_kv_args+=("gitea_runner_token=${gitea_runner_token_val}")
fi
gitea_secret_key_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_secret_key")"
if [[ -n "${gitea_secret_key_val}" ]]; then
  platform_kv_args+=("gitea_secret_key=${gitea_secret_key_val}")
fi
gitea_internal_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_internal_token")"
if [[ -n "${gitea_internal_token_val}" ]]; then
  platform_kv_args+=("gitea_internal_token=${gitea_internal_token_val}")
fi
gitea_jwt_secret_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_jwt_secret")"
if [[ -n "${gitea_jwt_secret_val}" ]]; then
  platform_kv_args+=("gitea_jwt_secret=${gitea_jwt_secret_val}")
fi
gitea_registry_host_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_registry_host")"
if [[ -n "${gitea_registry_host_val}" ]]; then
  platform_kv_args+=("gitea_registry_host=${gitea_registry_host_val}")
fi
gitea_registry_namespace_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_registry_namespace")"
if [[ -n "${gitea_registry_namespace_val}" ]]; then
  platform_kv_args+=("gitea_registry_namespace=${gitea_registry_namespace_val}")
fi
gitea_registry_username_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_registry_username")"
if [[ -n "${gitea_registry_username_val}" ]]; then
  platform_kv_args+=("gitea_registry_username=${gitea_registry_username_val}")
fi
gitea_registry_image_name_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_registry_image_name")"
if [[ -n "${gitea_registry_image_name_val}" ]]; then
  platform_kv_args+=("gitea_registry_image_name=${gitea_registry_image_name_val}")
fi
gitea_registry_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_registry_token")"
if [[ -n "${gitea_registry_token_val}" ]]; then
  platform_kv_args+=("gitea_registry_token=${gitea_registry_token_val}")
fi
# The recovery mirror is consumed by an early Argo sync wave. Seed its
# application path before that wave runs so the ExternalSecret never waits on
# the later Gitea hook configuration step.
gitea_push_mirror_repo_url_val="${GITEA_PUSH_MIRROR_REPO_URL:-}"
gitea_push_mirror_username_val="${GITEA_PUSH_MIRROR_USERNAME:-}"
gitea_push_mirror_token_val="${GITEA_PUSH_MIRROR_TOKEN:-}"
if [[ -z "${gitea_push_mirror_repo_url_val}" ]]; then
  gitea_push_mirror_repo_url_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_push_mirror_repo_url")"
fi
if [[ -z "${gitea_push_mirror_username_val}" ]]; then
  gitea_push_mirror_username_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_push_mirror_username")"
fi
if [[ -z "${gitea_push_mirror_token_val}" ]]; then
  gitea_push_mirror_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_push_mirror_token")"
fi
cloudflare_zone_api_token_val="${CLOUDFLARE_ZONE_API_TOKEN:-}"
if [[ -z "${cloudflare_zone_api_token_val}" ]]; then
  cloudflare_zone_api_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/cloudflare_zone_api_token")"
fi
if [[ -n "${cloudflare_zone_api_token_val}" ]]; then
  platform_kv_args+=("cloudflare_zone_api_token=${cloudflare_zone_api_token_val}")
fi
cloudflare_api_token_val="${CLOUDFLARE_API_TOKEN:-}"
if [[ -z "${cloudflare_api_token_val}" ]]; then
  cloudflare_api_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/cloudflare_api_token")"
fi
if [[ -z "${cloudflare_api_token_val}" ]]; then
  cloudflare_api_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/external_dns_cloudflare_token")"
fi
if [[ -n "${cloudflare_api_token_val}" ]]; then
  platform_kv_args+=("cloudflare_api_token=${cloudflare_api_token_val}")
fi
cloudflare_account_id_val="${CLOUDFLARE_ACCOUNT_ID:-}"
if [[ -z "${cloudflare_account_id_val}" ]]; then
  cloudflare_account_id_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/cloudflare_account_id")"
fi
if [[ -n "${cloudflare_account_id_val}" ]]; then
  platform_kv_args+=("cloudflare_account_id=${cloudflare_account_id_val}")
fi
rancher_cloudflared_tunnel_id_val="${RANCHER_CLOUDFLARED_TUNNEL_ID:-}"
if [[ -z "${rancher_cloudflared_tunnel_id_val}" ]]; then
  rancher_cloudflared_tunnel_id_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/rancher_cloudflared_tunnel_id")"
fi
if [[ -n "${rancher_cloudflared_tunnel_id_val}" ]]; then
  platform_kv_args+=("rancher_cloudflared_tunnel_id=${rancher_cloudflared_tunnel_id_val}")
fi
rancher_cloudflared_tunnel_token_val="${RANCHER_CLOUDFLARED_TUNNEL_TOKEN:-}"
if [[ -z "${rancher_cloudflared_tunnel_token_val}" ]]; then
  rancher_cloudflared_tunnel_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/rancher_cloudflared_tunnel_token")"
fi
if [[ -n "${rancher_cloudflared_tunnel_token_val}" ]]; then
  # The live Deployment is reconciled from this OpenBao field after bootstrap;
  # retaining only the tunnel ID would make token rotation a rebuild operation.
  platform_kv_args+=("rancher_cloudflared_tunnel_token=${rancher_cloudflared_tunnel_token_val}")
fi
grafana_admin_password_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/grafana_admin_password")"
if [[ -n "${grafana_admin_password_val}" ]]; then
  platform_kv_args+=("grafana_admin_password=${grafana_admin_password_val}")
fi
grafana_secret_key_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/grafana_secret_key")"
if [[ -n "${grafana_secret_key_val}" ]]; then
  platform_kv_args+=("grafana_secret_key=${grafana_secret_key_val}")
fi
homepage_grafana_password_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/homepage_grafana_password")"
authentik_admin_username_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_admin_username")"
if [[ -n "${authentik_admin_username_val}" ]]; then
  platform_kv_args+=("authentik_admin_username=${authentik_admin_username_val}")
fi
authentik_admin_password_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_admin_password")"
if [[ -n "${authentik_admin_password_val}" ]]; then
  platform_kv_args+=("authentik_admin_password=${authentik_admin_password_val}")
fi
authentik_bootstrap_token_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_bootstrap_token")"
if [[ -n "${authentik_bootstrap_token_val}" ]]; then
  platform_kv_args+=("authentik_bootstrap_token=${authentik_bootstrap_token_val}")
fi
authentik_secret_key_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_secret_key")"
if [[ -n "${authentik_secret_key_val}" ]]; then
  platform_kv_args+=("authentik_secret_key=${authentik_secret_key_val}")
fi
authentik_postgresql_password_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_postgresql_password")"
if [[ -n "${authentik_postgresql_password_val}" ]]; then
  platform_kv_args+=("authentik_postgresql_password=${authentik_postgresql_password_val}")
fi
tailscale_oauth_client_id_val="${TAILSCALE_OAUTH_CLIENT_ID:-}"
if [[ -z "${tailscale_oauth_client_id_val}" ]]; then
  tailscale_oauth_client_id_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/tailscale_oauth_client_id")"
fi
tailscale_oauth_client_secret_val="${TAILSCALE_OAUTH_CLIENT_SECRET:-}"
if [[ -z "${tailscale_oauth_client_secret_val}" ]]; then
  tailscale_oauth_client_secret_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/tailscale_oauth_client_secret")"
fi
if [[ -n "${tailscale_oauth_client_id_val}" ]]; then
  platform_kv_args+=("tailscale_oauth_client_id=${tailscale_oauth_client_id_val}")
fi
if [[ -n "${tailscale_oauth_client_secret_val}" ]]; then
  platform_kv_args+=("tailscale_oauth_client_secret=${tailscale_oauth_client_secret_val}")
fi
ingress_internal_vip_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/ingress_internal_vip")"
if [[ -n "${ingress_internal_vip_val}" ]]; then
  platform_kv_args+=("ingress_internal_vip=${ingress_internal_vip_val}")
fi
ingress_external_vip_val="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/ingress_external_vip")"
if [[ -n "${ingress_external_vip_val}" ]]; then
  platform_kv_args+=("ingress_external_vip=${ingress_external_vip_val}")
fi

rke2_node_token_path="/var/lib/rancher/rke2/server/node-token"
rke2_node_token_val=""
if [[ -f "${rke2_node_token_path}" ]]; then
  rke2_node_token_val="$(tr -d '\n' <"${rke2_node_token_path}" 2>/dev/null || true)"
fi
if [[ -n "${rke2_node_token_val}" ]]; then
  platform_kv_args+=("rke2_node_token=${rke2_node_token_val}")
fi

if [[ "${#platform_kv_args[@]}" -gt 0 ]]; then
  echo "[phase40] storing bootstrap-generated platform secrets in OpenBao"
  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao secrets enable -path=secret kv-v2 >/dev/null 2>&1 || \
    retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao secrets tune -version=2 secret >/dev/null 2>&1 || true
  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao kv put secret/bootstrap/platform "${platform_kv_args[@]}"
  echo "[phase40] platform bootstrap secrets stored in OpenBao (secret/bootstrap/platform)"

  # Promote workload-facing copies into app-owned paths. The bootstrap path is
  # recovery input; steady-state controllers should not need read access to the
  # entire bootstrap record just to materialize one workload credential.
  if [[ -n "${cloudflare_api_token_val}" ]]; then
    seed_openbao_app_fields ingress/external-dns "${root_token}" \
      "api_token=${cloudflare_api_token_val}"
  fi
  if [[ -n "${rancher_cloudflared_tunnel_token_val}" ]]; then
    seed_openbao_app_fields cloudflared/tunnel "${root_token}" \
      "token=${rancher_cloudflared_tunnel_token_val}"
  fi
  if [[ -n "${gitea_admin_password_val}" ]]; then
    seed_openbao_app_fields gitea/admin "${root_token}" \
      "username=${gitea_repo_username_effective:-gitea-admin}" \
      "password=${gitea_admin_password_val}" \
      "email=gitea-admin@example.com"
  fi
  if [[ -n "${gitea_runner_token_val}" ]]; then
    seed_openbao_app_fields gitea/actions-runner "${root_token}" \
      "token=${gitea_runner_token_val}"
  fi
  if [[ -n "${gitea_secret_key_val}" ]]; then
    seed_openbao_app_fields gitea/encryption "${root_token}" \
      "secret_key=${gitea_secret_key_val}"
  fi
  if [[ -n "${gitea_internal_token_val}" && -n "${gitea_jwt_secret_val}" ]]; then
    seed_openbao_app_fields gitea/runtime "${root_token}" \
      "internal_token=${gitea_internal_token_val}" \
      "jwt_secret=${gitea_jwt_secret_val}"
  fi
  if [[ -n "${gitea_registry_token_val}" && -n "${gitea_registry_host_val}" ]]; then
    seed_openbao_app_fields gitea/registry "${root_token}" \
      "host=${gitea_registry_host_val}" \
      "username=${gitea_registry_username_val:-gitea-admin}" \
      "token=${gitea_registry_token_val}"
  fi
  if [[ -n "${gitea_push_mirror_repo_url_val}" \
    && -n "${gitea_push_mirror_username_val}" \
    && -n "${gitea_push_mirror_token_val}" ]]; then
    seed_openbao_app_fields gitea/push-mirror "${root_token}" \
      "remote_url=${gitea_push_mirror_repo_url_val}" \
      "username=${gitea_push_mirror_username_val}" \
      "token=${gitea_push_mirror_token_val}"
  fi
  # The chart generated these credentials before OpenBao was available. Adopt
  # the live values once so OpenBao becomes their recovery authority and ESO
  # can maintain the existing Kubernetes delivery Secret without replacing it.
  gitea_postgres_password_val="$(read_k8s_secret_key gitea gitea-postgresql postgres-password)"
  gitea_app_password_val="$(read_k8s_secret_key gitea gitea-postgresql password)"
  gitea_replication_password_val="$(read_k8s_secret_key gitea gitea-postgresql replication-password)"
  if [[ -n "${gitea_postgres_password_val}" && -n "${gitea_app_password_val}" ]]; then
    gitea_postgresql_args=(
      "postgres-password=${gitea_postgres_password_val}"
      "password=${gitea_app_password_val}"
    )
    if [[ -n "${gitea_replication_password_val}" ]]; then
      gitea_postgresql_args+=("replication-password=${gitea_replication_password_val}")
    fi
    seed_openbao_app_fields gitea/postgresql "${root_token}" \
      "${gitea_postgresql_args[@]}"
  else
    echo "[phase40] Gitea PostgreSQL Secret is not ready; app-owned database credentials were not promoted." >&2
  fi
  if [[ -n "${authentik_secret_key_val}" && -n "${authentik_postgresql_password_val}" && -n "${authentik_admin_password_val}" && -n "${authentik_bootstrap_token_val}" ]]; then
    seed_openbao_app_fields authentik/encryption "${root_token}" \
      "secret_key=${authentik_secret_key_val}"
    seed_openbao_app_fields authentik/postgresql "${root_token}" \
      "postgresql_password=${authentik_postgresql_password_val}" \
      "password=${authentik_postgresql_password_val}" \
      "postgres-password=${authentik_postgresql_password_val}"
    seed_openbao_app_fields authentik/admin "${root_token}" \
      "admin_username=${authentik_admin_username_val:-akadmin}" \
      "bootstrap_password=${authentik_admin_password_val}" \
      "bootstrap_token=${authentik_bootstrap_token_val}"
  fi
  if [[ -n "${grafana_admin_password_val}" ]]; then
    seed_openbao_app_fields observability/grafana "${root_token}" \
      admin_user=admin \
      "admin_password=${grafana_admin_password_val}" \
      "secret_key=${grafana_secret_key_val}"
  fi
  if [[ -n "${homepage_grafana_password_val}" ]]; then
    seed_openbao_app_fields homepage/grafana "${root_token}" \
      username=homepage \
      "password=${homepage_grafana_password_val}"
  fi
  if [[ -n "${tailscale_oauth_client_id_val}" && -n "${tailscale_oauth_client_secret_val}" ]]; then
    seed_openbao_app_fields ansible/tailscale "${root_token}" \
      "oauth_client_id=${tailscale_oauth_client_id_val}" \
      "oauth_client_secret=${tailscale_oauth_client_secret_val}"
  fi
  if ! "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${root_token}" \
    bao kv get secret/apps/observability/apprise >/dev/null 2>&1; then
    apprise_default_config=$'version: 1\nurls: []'
    seed_openbao_app_fields observability/apprise "${root_token}" \
      "apprise_yml=${apprise_default_config}"
  fi
else
  echo "[phase40] no platform bootstrap secrets found to store in OpenBao; skipping."
fi

if [[ "${use_argo}" == true ]]; then
  if wait_for_argo_application_crd "180s"; then
    echo "[phase40] enabling post-OpenBao Argo CD app"
    "${kubectl_bin}" -n argocd apply -f pods/argocd/platform/post-openbao/application.yaml
  else
    echo "[phase40] Argo CD Application CRD not ready; falling back to direct OpenBao post-init config." >&2
    use_argo=false
  fi
fi

if [[ "${use_argo}" != true ]]; then
  echo "[phase40] applying OpenBao post-init config directly"
  policy_dir="${repo_root}/pods/secrets/openbao/policies"
  job_manifest="${config_dir}/job.yaml"
  if [[ -d "${policy_dir}" ]]; then
    "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" create configmap openbao-policies \
      --from-file=ci.hcl="${policy_dir}/ci.hcl" \
      --from-file=argo.hcl="${policy_dir}/argo.hcl" \
      --from-file=config.hcl="${policy_dir}/config.hcl" \
      --from-file=external-secrets.hcl="${policy_dir}/external-secrets.hcl" \
      --from-file=apprise.hcl="${policy_dir}/apprise.hcl" \
      --from-file=cloudflared.hcl="${policy_dir}/cloudflared.hcl" \
      --from-file=external-dns.hcl="${policy_dir}/external-dns.hcl" \
      --from-file=ansible-runner.hcl="${policy_dir}/ansible-runner.hcl" \
      --from-file=homepage.hcl="${policy_dir}/homepage.hcl" \
      --from-file=grafana.hcl="${policy_dir}/grafana.hcl" \
      --from-file=authentik.hcl="${policy_dir}/authentik.hcl" \
      --from-file=gitea.hcl="${policy_dir}/gitea.hcl" \
      --dry-run=client -o yaml | "${kubectl_bin}" apply -f -
  else
    echo "[phase40] warning: policy directory missing at ${policy_dir}; skipping policy configmap" >&2
  fi
  if [[ -f "${job_manifest}" ]]; then
    # A Job pod template is immutable. Configuration is idempotent, so replace
    # the completed runner whenever policy or auth-role desired state changes.
    "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" delete job openbao-bootstrap-config \
      --ignore-not-found --wait=true
    "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" apply -f "${job_manifest}"
  else
    echo "[phase40] warning: OpenBao config job manifest missing at ${job_manifest}" >&2
  fi
fi

echo "[phase40] waiting for OpenBao bootstrap config job (if/when created)"
job_seen=false
job_discovery_deadline="$((SECONDS + $(timeout_to_seconds "${PHASE40_JOB_DISCOVERY_TIMEOUT}")))"
while (( SECONDS < job_discovery_deadline )); do
  if "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get job openbao-bootstrap-config >/dev/null 2>&1; then
    job_seen=true
    echo "[phase40] detected OpenBao bootstrap config job; waiting for completion (${PHASE40_JOB_COMPLETE_TIMEOUT})"
    if ! "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" wait --for=condition=complete job/openbao-bootstrap-config --timeout="${PHASE40_JOB_COMPLETE_TIMEOUT}"; then
      echo "[phase40] OpenBao bootstrap config job did not complete in time." >&2
      echo "[phase40] diagnostics: job describe" >&2
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" describe job openbao-bootstrap-config >&2 || true
      echo "[phase40] diagnostics: job pods" >&2
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get pods -l job-name=openbao-bootstrap-config -o wide >&2 || true
      echo "[phase40] diagnostics: job logs" >&2
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" logs job/openbao-bootstrap-config --all-containers --tail="${BOOTSTRAP_LOG_TAIL}" >&2 || true
      if bool_is_true "${PHASE40_FAIL_ON_CONFIG_JOB_TIMEOUT}"; then
        exit 1
      fi
      echo "[phase40] continuing despite OpenBao bootstrap config timeout (set PHASE40_FAIL_ON_CONFIG_JOB_TIMEOUT=1 to fail hard)." >&2
    fi
    break
  fi
  sleep 10
done
if [[ "${job_seen}" != "true" ]]; then
  echo "[phase40] OpenBao bootstrap config job was not observed within ${PHASE40_JOB_DISCOVERY_TIMEOUT}; continuing." >&2
fi

if [[ "${use_argo}" == true ]] && bool_is_true "${BOOTSTRAP_DISABLE_PRE_OPENBAO}"; then
  "${kubectl_bin}" -n argocd delete application platform-pre-openbao --ignore-not-found >/dev/null 2>&1 || \
    echo "[phase40] warning: unable to delete platform-pre-openbao application; continuing" >&2
fi

cat <<'INFO'

Phase 40 complete (OpenBao introduced).

Next: Phase 50 deploy Argo CD:
  ansible/ansible-scripts/bootstrap/Phase-50/run-phase50.sh

Then: Phase 60 repo seed + GitOps handoff:
  sudo ansible/ansible-scripts/bootstrap/Phase-60/run-phase60.sh

Then: Phase 70 GitOps realization gate:
  sudo ansible/ansible-scripts/bootstrap/Phase-70/run-phase70.sh

Then: Phase 90 late live-state reconciliation:
  sudo ansible/ansible-scripts/bootstrap/Phase-90/run-phase90.sh

Finally: Phase 99 recovery export + destroy bootstrap authority:
  sudo ansible/ansible-scripts/bootstrap/Phase-90/run-phase99.sh

INFO
