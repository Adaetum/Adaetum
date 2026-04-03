#!/usr/bin/env bash

BOOTSTRAP_DIAGNOSTICS_ENABLED="${BOOTSTRAP_DIAGNOSTICS_ENABLED:-1}"
BOOTSTRAP_DIAGNOSTICS_JSON_ENABLED="${BOOTSTRAP_DIAGNOSTICS_JSON_ENABLED:-1}"
BOOTSTRAP_DIAGNOSTICS_STDOUT_EXCERPT_MAX="${BOOTSTRAP_DIAGNOSTICS_STDOUT_EXCERPT_MAX:-1200}"
BOOTSTRAP_DIAGNOSTICS_STDERR_EXCERPT_MAX="${BOOTSTRAP_DIAGNOSTICS_STDERR_EXCERPT_MAX:-1200}"

bootstrap_diag_is_true() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

bootstrap_diag_init() {
  if ! bootstrap_diag_is_true "${BOOTSTRAP_DIAGNOSTICS_ENABLED}"; then
    return 0
  fi

  local log_root=""
  local host_name=""
  local run_stamp=""

  log_root="${BOOTSTRAP_PHASE_LOG_DIR:-}"
  if [[ -z "${log_root}" ]]; then
    if [[ -n "${BUNDLE_BOOTSTRAP_LOG_FILE:-}" ]]; then
      log_root="$(dirname "${BUNDLE_BOOTSTRAP_LOG_FILE}")/bootstrap-phases"
    elif [[ -n "${PHASE40_LOG_FILE:-}" ]]; then
      log_root="$(dirname "${PHASE40_LOG_FILE}")"
    else
      log_root="/var/log/bootstrap"
    fi
  fi

  BOOTSTRAP_DIAGNOSTICS_DIR="${BOOTSTRAP_DIAGNOSTICS_DIR:-${log_root}/diagnostics}"
  mkdir -p "${BOOTSTRAP_DIAGNOSTICS_DIR}" 2>/dev/null || true

  if [[ -z "${BOOTSTRAP_RUN_ID:-}" ]]; then
    host_name="$(hostname 2>/dev/null || echo unknown-host)"
    run_stamp="$(date -u +%Y%m%dT%H%M%SZ 2>/dev/null || date +%s)"
    BOOTSTRAP_RUN_ID="${run_stamp}-${host_name}-$$"
    export BOOTSTRAP_RUN_ID
  fi

  BOOTSTRAP_DIAGNOSTICS_JSONL="${BOOTSTRAP_DIAGNOSTICS_JSONL:-${BOOTSTRAP_DIAGNOSTICS_DIR}/${BOOTSTRAP_RUN_ID}.jsonl}"
  BOOTSTRAP_DIAGNOSTICS_RUN_DIR="${BOOTSTRAP_DIAGNOSTICS_RUN_DIR:-${BOOTSTRAP_DIAGNOSTICS_DIR}/${BOOTSTRAP_RUN_ID}}"
  mkdir -p "${BOOTSTRAP_DIAGNOSTICS_RUN_DIR}" 2>/dev/null || true
  export BOOTSTRAP_DIAGNOSTICS_DIR BOOTSTRAP_DIAGNOSTICS_JSONL BOOTSTRAP_DIAGNOSTICS_RUN_DIR
}

bootstrap_diag_python() {
  local script_dir=""
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
  printf '%s/diagnostics_json.py' "${script_dir}"
}

bootstrap_diag_classify_failure_kind() {
  local text="${1:-}"
  local lower=""
  lower="$(printf '%s' "${text}" | tr '[:upper:]' '[:lower:]')"
  case "${lower}" in
    *imagepullbackoff*|*errimagepull*|*"trying and failing to pull image"*|*"image pull"*)
      printf 'image-pull'
      ;;
    *release-assets.githubusercontent.com*|*"failed to fetch"*|*"helm repo"*|*"index.yaml"* )
      printf 'chart-fetch'
      ;;
    *"no such host"*|*nxdomain*|*"does not resolve"*)
      printf 'dns'
      ;;
    *"http error"*|*"http=4"*|*"http=5"*|*forbidden*|*unauthorized*)
      printf 'http'
      ;;
    *"authentication failed"*|*forbidden*|*unauthorized*|*"access denied"*)
      printf 'auth'
      ;;
    *timeout*|*"timed out"*|*"deadline exceeded"*)
      printf 'timeout'
      ;;
    *rollout*|*"did not become ready"*|*"waiting for the condition"*)
      printf 'rollout'
      ;;
    *kubectl*|*"no matches for kind"*|*"the server doesn't have a resource type"* )
      printf 'kubernetes-api'
      ;;
    *"connection refused"*|*"connection reset"*|*"tls handshake timeout"*|*"temporary failure in name resolution"*)
      printf 'network'
      ;;
    *secret* )
      printf 'secret'
      ;;
    *missing*|*invalid*|*"not found"*|*unsupported*)
      printf 'config'
      ;;
    *)
      printf 'unknown'
      ;;
  esac
}

bootstrap_diag_record() {
  if ! bootstrap_diag_is_true "${BOOTSTRAP_DIAGNOSTICS_ENABLED}" || ! bootstrap_diag_is_true "${BOOTSTRAP_DIAGNOSTICS_JSON_ENABLED}"; then
    return 0
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    return 0
  fi

  bootstrap_diag_init

  local helper=""
  local args=()
  helper="$(bootstrap_diag_python)"
  if [[ ! -f "${helper}" ]]; then
    return 0
  fi

  args+=(--output "${BOOTSTRAP_DIAGNOSTICS_JSONL}")
  args+=(--field "run_id=${BOOTSTRAP_RUN_ID}")
  for kv in "$@"; do
    args+=(--field "${kv}")
  done
  python3 "${helper}" "${args[@]}" >/dev/null 2>&1 || true
}

bootstrap_diag_capture_evidence_path() {
  bootstrap_diag_init
  local component="${1:-component}"
  local name="${2:-evidence}"
  local sanitized_component="" sanitized_name="" ts=""
  sanitized_component="$(printf '%s' "${component}" | tr -cs '[:alnum:]._-:' '_')"
  sanitized_name="$(printf '%s' "${name}" | tr -cs '[:alnum:]._-:' '_')"
  ts="$(date -u +%Y%m%dT%H%M%SZ 2>/dev/null || date +%s)"
  printf '%s/%s-%s-%s.log' "${BOOTSTRAP_DIAGNOSTICS_RUN_DIR}" "${ts}" "${sanitized_component}" "${sanitized_name}"
}

bootstrap_diag_record_file_event() {
  local component="${1:-unknown}"
  local step="${2:-unknown}"
  local summary="${3:-event captured}"
  local evidence_path="${4:-}"
  local severity="${5:-warning}"
  local failure_kind="${6:-unknown}"
  bootstrap_diag_record \
    "phase=${BOOTSTRAP_DIAG_PHASE:-unknown}" \
    "step=${step}" \
    "component=${component}" \
    "operation=diagnostic-capture" \
    "severity=${severity}" \
    "failure_kind=${failure_kind}" \
    "summary=${summary}" \
    "log_path=${BOOTSTRAP_DIAG_LOG_PATH:-}" \
    "evidence_paths=${evidence_path}"
}
