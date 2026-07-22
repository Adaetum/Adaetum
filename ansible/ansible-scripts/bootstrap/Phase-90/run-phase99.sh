#!/usr/bin/env bash
set -euo pipefail

# Phase 99 exports recovery material and, for a break-glass primary, removes
# temporary local bootstrap authority. It deliberately skips that destructive
# action on joining nodes: their local files are not the cluster's authority.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck disable=SC1091
. "${script_dir}/diagnostics.sh"
phase99_log_root="${BOOTSTRAP_PHASE_LOG_DIR:-/var/log/bootstrap}"
PHASE99_LOG_FILE="${PHASE99_LOG_FILE:-${phase99_log_root}/phase99.log}"
BOOTSTRAP_DIAG_PHASE="phase99"
BOOTSTRAP_DIAG_LOG_PATH="${PHASE99_LOG_FILE}"
bootstrap_diag_init
phase99_start_ts="$(date +%s)"
phase99_last_error_cmd=""
phase99_last_error_line=""

if [[ -z "${BUNDLE_BOOTSTRAP_LOG_FILE:-}" ]]; then
  mkdir -p "$(dirname "${PHASE99_LOG_FILE}")"
  exec >>"${PHASE99_LOG_FILE}" 2>&1
fi

phase99_exit_trap() {
  local rc=$?
  local end_ts duration
  end_ts="$(date +%s)"
  duration="$((end_ts - phase99_start_ts))"
  if [[ "${rc}" -eq 0 ]]; then
    bootstrap_diag_record \
      "phase=phase99" \
      "step=phase99" \
      "component=phase99" \
      "operation=run-complete" \
      "severity=info" \
      "exit_code=0" \
      "duration_seconds=${duration}" \
      "summary=phase99 complete" \
      "log_path=${BOOTSTRAP_DIAG_LOG_PATH}"
  else
    bootstrap_diag_record \
      "phase=phase99" \
      "step=phase99" \
      "component=phase99" \
      "operation=run-failed" \
      "severity=error" \
      "exit_code=${rc}" \
      "duration_seconds=${duration}" \
      "failure_kind=$(bootstrap_diag_classify_failure_kind "${phase99_last_error_cmd}")" \
      "summary=phase99 failed at line ${phase99_last_error_line:-unknown}: ${phase99_last_error_cmd:-unknown}" \
      "log_path=${BOOTSTRAP_DIAG_LOG_PATH}"
  fi
  exit "${rc}"
}
trap 'phase99_exit_trap' EXIT
trap 'phase99_last_error_line="${BASH_LINENO[0]:-unknown}"; phase99_last_error_cmd="${BASH_COMMAND:-unknown}"' ERR

bootstrap_diag_record \
  "phase=phase99" \
  "step=phase99" \
  "component=phase99" \
  "operation=run-start" \
  "severity=info" \
  "summary=phase99 starting" \
  "log_path=${BOOTSTRAP_DIAG_LOG_PATH}"

# Backup settings describe the recovery artifact, not normal runtime state.
# The encrypted export is created before any eligible local-secret cleanup.
BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR:-/var/lib/bootstrap-secrets}"
BOOTSTRAP_BURN_LADDER="${BOOTSTRAP_BURN_LADDER:-}"
BOOTSTRAP_BACKUP_TO_R2="${BOOTSTRAP_BACKUP_TO_R2:-}"
BOOTSTRAP_BACKUP_URL="${BOOTSTRAP_BACKUP_URL:-}"
BOOTSTRAP_SHARED_TOKEN="${BOOTSTRAP_SHARED_TOKEN:-}"
BOOTSTRAP_BACKUP_PASSPHRASE="${BOOTSTRAP_BACKUP_PASSPHRASE:-}"
BOOTSTRAP_BACKUP_PASSPHRASE_B64="${BOOTSTRAP_BACKUP_PASSPHRASE_B64:-}"
BOOTSTRAP_BACKUP_FILE="${BOOTSTRAP_BACKUP_FILE:-bootstrap-emergency-kit.tar.gz.enc}"  # when BOOTSTRAP_BACKUP_FORMAT=openssl|7z
BOOTSTRAP_BACKUP_FILE_OPENSSL="${BOOTSTRAP_BACKUP_FILE_OPENSSL:-bootstrap-emergency-kit.tar.gz.enc}" # when BOOTSTRAP_BACKUP_FORMAT=both
BOOTSTRAP_BACKUP_FILE_7Z="${BOOTSTRAP_BACKUP_FILE_7Z:-bootstrap-emergency-kit.7z}"  # when BOOTSTRAP_BACKUP_FORMAT=both
BOOTSTRAP_OPENBAO_BACKUP_FILE="${BOOTSTRAP_OPENBAO_BACKUP_FILE:-bootstrap-openbao-backup.json.enc}"
BOOTSTRAP_BACKUP_FORMAT="${BOOTSTRAP_BACKUP_FORMAT:-openssl}" # openssl|7z|both
BOOTSTRAP_INSTALL_7Z="${BOOTSTRAP_INSTALL_7Z:-1}" # 1 => attempt to install 7z when BOOTSTRAP_BACKUP_FORMAT needs it
OPENBAO_NAMESPACE="${OPENBAO_NAMESPACE:-openbao}"
OPENBAO_POD="${OPENBAO_POD:-openbao-0}"
BOOTSTRAP_REQUIRE_OPENBAO_INIT_BACKUP="${BOOTSTRAP_REQUIRE_OPENBAO_INIT_BACKUP:-1}"
BOOTSTRAP_SKIP_BURN_ON_JOIN="${BOOTSTRAP_SKIP_BURN_ON_JOIN:-1}"
BOOTSTRAP_BREAKGLASS_STRICT="${BOOTSTRAP_BREAKGLASS_STRICT:-0}"

force=true

if [[ ! -d "${BOOTSTRAP_SECRET_DIR}" ]]; then
  echo "Missing secrets dir: ${BOOTSTRAP_SECRET_DIR}" >&2
  exit 1
fi

# Join nodes (agent or joining server) should not burn local bootstrap secrets.
# We detect join mode from rke2 config: joiners include a remote `server:` endpoint.
rke2_cfg="/etc/rancher/rke2/config.yaml"
is_join_node=0
if [[ -f "${rke2_cfg}" ]] && grep -Eq '^[[:space:]]*server:[[:space:]]*https?://' "${rke2_cfg}"; then
  is_join_node=1
fi
skip_burn_on_join="$(printf '%s' "${BOOTSTRAP_SKIP_BURN_ON_JOIN}" | tr '[:upper:]' '[:lower:]')"
if [[ "${is_join_node}" -eq 1 ]] && [[ "${skip_burn_on_join}" == "1" || "${skip_burn_on_join}" == "true" ]]; then
  echo "[phase99] join node detected (${rke2_cfg} has remote server:). Skipping burn-the-ladder." >&2
  exit 0
fi

# Phase 99 is an export-then-destroy transaction. Operators can postpone the
# transaction by disabling Phase 99, but a Phase 99 run may never report
# success after skipping its only off-node recovery copy.
backup_to_r2_normalized="$(printf '%s' "${BOOTSTRAP_BACKUP_TO_R2}" | tr '[:upper:]' '[:lower:]')"
case "${backup_to_r2_normalized}" in
  1|true|yes|on) ;;
  *)
    echo "Phase 99 requires BOOTSTRAP_BACKUP_TO_R2=1 before bootstrap authority can be destroyed." >&2
    echo "Disable BOOTSTRAP_RUN_PHASE99 to postpone both recovery export and burn-the-ladder." >&2
    exit 22
    ;;
esac

# Phase 99 always removes the bootstrap-token Secret after the recovery export.
# Resolve kubectl before the optional backup branch so the final destructive
# step has the same explicit dependency regardless of backup format.
kubectl_bin=""
if command -v kubectl >/dev/null 2>&1; then
  kubectl_bin="$(command -v kubectl)"
elif [[ -x /var/lib/rancher/rke2/bin/kubectl ]]; then
  kubectl_bin="/var/lib/rancher/rke2/bin/kubectl"
else
  echo "kubectl not found; cannot remove the OpenBao bootstrap-token Secret safely." >&2
  exit 10
fi

# Phase 99 may be invoked directly as well as through bundle-bootstrap. RKE2's
# kubectl otherwise defaults to localhost:8080 and can neither verify nor remove
# the temporary bootstrap authority.
export KUBECONFIG="${KUBECONFIG:-/etc/rancher/rke2/rke2.yaml}"

redact_url() {
  # Redact token query param in logs.
  python3 - <<'PY' "${1:-}"
import sys, urllib.parse
url = sys.argv[1] if len(sys.argv) > 1 else ""
try:
  p = urllib.parse.urlparse(url)
except Exception:
  print(url)
  raise SystemExit(0)
q = urllib.parse.parse_qsl(p.query, keep_blank_values=True)
q2 = []
for k,v in q:
  if k.lower() == "token":
    q2.append((k,"REDACTED"))
  else:
    q2.append((k,v))
query = urllib.parse.urlencode(q2)
print(urllib.parse.urlunparse((p.scheme,p.netloc,p.path,p.params,query,p.fragment)))
PY
}

read_local_secret_file() {
  local secret_path="${1:-}"
  if [[ -z "${secret_path}" || ! -f "${secret_path}" ]]; then
    return 0
  fi
  tr -d '\r' <"${secret_path}" | tr -d '\n'
}

extract_ingress_service_vip() {
  local service_json="${1:-}"
  python3 - <<'PY' "${service_json}"
import json, sys

try:
    data = json.loads(sys.argv[1])
except Exception:
    print("")
    raise SystemExit(0)

spec = data.get("spec", {}) or {}
metadata = data.get("metadata", {}) or {}
annotations = metadata.get("annotations", {}) or {}
status = data.get("status", {}) or {}

value = spec.get("loadBalancerIP")
if isinstance(value, str) and value.strip():
    print(value.strip())
    raise SystemExit(0)

for key in ("kube-vip.io/loadbalancerIPs", "kube-vip.io/loadbalancerIP"):
    value = annotations.get(key)
    if isinstance(value, str) and value.strip():
        print(value.strip().split(",")[0].strip())
        raise SystemExit(0)

for item in (status.get("loadBalancer", {}) or {}).get("ingress", []) or []:
    if isinstance(item, dict):
        for key in ("ip", "hostname"):
            value = item.get(key)
            if isinstance(value, str) and value.strip():
                print(value.strip())
                raise SystemExit(0)

print("")
PY
}

write_ingress_vip_backup_configmap() {
  local dest_path="${1:?dest_path}"
  local config_json=""
  local service_json=""
  local internal_vip=""
  local external_vip=""
  local controller_service=""
  local service_type=""
  local actual_service_ref=""
  local actual_service_type=""
  local actual_service_vip=""

  if "${kubectl_bin}" -n ingress get configmap ingress-vip-config >/dev/null 2>&1; then
    config_json="$("${kubectl_bin}" -n ingress get configmap ingress-vip-config -o json 2>/dev/null || true)"
  fi

  if [[ -n "${config_json}" ]]; then
    internal_vip="$(python3 - <<'PY' "${config_json}"
import json,sys
raw = sys.argv[1]
try:
  data = json.loads(raw).get("data", {})
  value = data.get("ingress_internal_vip", "")
  if value:
    sys.stdout.write(value)
except Exception:
  pass
PY
)"
    external_vip="$(python3 - <<'PY' "${config_json}"
import json,sys
raw = sys.argv[1]
try:
  data = json.loads(raw).get("data", {})
  value = data.get("ingress_external_vip", "")
  if value:
    sys.stdout.write(value)
except Exception:
  pass
PY
)"
    controller_service="$(python3 - <<'PY' "${config_json}"
import json,sys
raw = sys.argv[1]
try:
  data = json.loads(raw).get("data", {})
  value = data.get("ingress_controller_service", "")
  if value:
    sys.stdout.write(value)
except Exception:
  pass
PY
)"
    service_type="$(python3 - <<'PY' "${config_json}"
import json,sys
raw = sys.argv[1]
try:
  data = json.loads(raw).get("data", {})
  value = data.get("ingress_service_type", "")
  if value:
    sys.stdout.write(value)
except Exception:
  pass
PY
)"
  fi

  if [[ -z "${internal_vip}" ]]; then
    internal_vip="$(read_local_secret_file "${BOOTSTRAP_SECRET_DIR}/ingress_internal_vip")"
  fi
  if [[ -z "${external_vip}" ]]; then
    external_vip="$(read_local_secret_file "${BOOTSTRAP_SECRET_DIR}/ingress_external_vip")"
  fi

  for svc_ref in kube-system/rke2-ingress-nginx-controller ingress-nginx/ingress-nginx-controller; do
    local svc_ns="${svc_ref%%/*}"
    local svc_name="${svc_ref##*/}"
    if "${kubectl_bin}" -n "${svc_ns}" get svc "${svc_name}" >/dev/null 2>&1; then
      actual_service_ref="${svc_ref}"
      actual_service_type="$("${kubectl_bin}" -n "${svc_ns}" get svc "${svc_name}" -o jsonpath='{.spec.type}' 2>/dev/null || true)"
      service_json="$("${kubectl_bin}" -n "${svc_ns}" get svc "${svc_name}" -o json 2>/dev/null || true)"
      break
    fi
  done

  if [[ -n "${service_json}" ]]; then
    actual_service_vip="$(extract_ingress_service_vip "${service_json}")"
  fi

  if [[ -n "${actual_service_ref}" && -z "${actual_service_vip}" ]]; then
    local svc_ns="${actual_service_ref%%/*}"
    local svc_name="${actual_service_ref##*/}"
    local vip_wait_timeout="${PHASE60_INGRESS_VIP_WAIT_TIMEOUT:-60}"
    local vip_wait_delay="${PHASE60_INGRESS_VIP_WAIT_DELAY:-5}"
    local waited="0"

    "${kubectl_bin}" -n "${svc_ns}" patch svc "${svc_name}" --type=merge \
      -p '{"spec":{"type":"LoadBalancer","loadBalancerIP":"0.0.0.0"},"metadata":{"annotations":{"kube-vip.io/loadbalancerIP":"0.0.0.0","kube-vip.io/loadbalancerIPs":null}}}' >/dev/null 2>&1 || true

    while [[ "${waited}" -lt "${vip_wait_timeout}" ]]; do
      service_json="$("${kubectl_bin}" -n "${svc_ns}" get svc "${svc_name}" -o json 2>/dev/null || true)"
      if [[ -n "${service_json}" ]]; then
        actual_service_vip="$(extract_ingress_service_vip "${service_json}")"
      fi
      if [[ -n "${actual_service_vip}" && "${actual_service_vip}" != "0.0.0.0" ]]; then
        break
      fi
      sleep "${vip_wait_delay}"
      waited="$((waited + vip_wait_delay))"
    done
  fi

  if [[ -z "${controller_service}" ]]; then
    controller_service="${actual_service_ref}"
  fi
  if [[ -z "${service_type}" ]]; then
    service_type="${actual_service_type}"
  fi
  if [[ -z "${internal_vip}" ]]; then
    internal_vip="${actual_service_vip}"
  fi
  if [[ -z "${external_vip}" ]]; then
    external_vip="${actual_service_vip}"
  fi

  if [[ -z "${internal_vip}" && -n "${external_vip}" ]]; then
    internal_vip="${external_vip}"
  fi
  if [[ -z "${external_vip}" && -n "${internal_vip}" ]]; then
    external_vip="${internal_vip}"
  fi

  python3 - <<'PY' "${dest_path}" "${internal_vip}" "${external_vip}" "${controller_service}" "${service_type}"
import json
import sys

dest, internal_vip, external_vip, controller_service, service_type = sys.argv[1:6]

with open(dest, "w", encoding="utf-8") as fh:
    fh.write("apiVersion: v1\n")
    fh.write("kind: ConfigMap\n")
    fh.write("metadata:\n")
    fh.write("  name: ingress-vip-config\n")
    fh.write("  namespace: ingress\n")
    fh.write("data:\n")
    fh.write(f"  ingress_internal_vip: {json.dumps(internal_vip)}\n")
    fh.write(f"  ingress_external_vip: {json.dumps(external_vip)}\n")
    if controller_service:
        fh.write(f"  ingress_controller_service: {json.dumps(controller_service)}\n")
    if service_type:
        fh.write(f"  ingress_service_type: {json.dumps(service_type)}\n")
PY
  chmod 0600 "${dest_path}" 2>/dev/null || true
}

cat <<'INFO'
This step is destructive. It will remove local bootstrap secrets.
Make sure OpenBao is fully initialized and workloads can authenticate.

Manual steps still required:
- Rotate any temporary tokens (Cloudflare, Tailscale, etc.)
- Remove bootstrap secrets from any CI or manifests
- Confirm GitHub cannot deploy anything
- Confirm Gitea/CI requires OpenBao credentials

INFO

if [[ "${backup_to_r2_normalized}" == "1" || "${backup_to_r2_normalized}" == "true" || \
      "${backup_to_r2_normalized}" == "yes" || "${backup_to_r2_normalized}" == "on" ]]; then
  if [[ -z "${BOOTSTRAP_BACKUP_URL}" ]]; then
    echo "BOOTSTRAP_BACKUP_URL is required when BOOTSTRAP_BACKUP_TO_R2=1" >&2
    exit 5
  fi
  if [[ -z "${BOOTSTRAP_BACKUP_PASSPHRASE}" && -n "${BOOTSTRAP_BACKUP_PASSPHRASE_B64}" ]]; then
    # Base64 avoids shell/systemd quoting issues when passphrases contain special characters.
    if command -v python3 >/dev/null 2>&1; then
      BOOTSTRAP_BACKUP_PASSPHRASE="$(python3 - <<'PY' "${BOOTSTRAP_BACKUP_PASSPHRASE_B64}"
import base64,sys
try:
  b64=sys.argv[1].strip()
  sys.stdout.write(base64.b64decode(b64.encode()).decode("utf-8"))
except Exception:
  sys.exit(1)
PY
)" || true
    elif command -v base64 >/dev/null 2>&1; then
      BOOTSTRAP_BACKUP_PASSPHRASE="$(printf '%s' "${BOOTSTRAP_BACKUP_PASSPHRASE_B64}" | base64 -d 2>/dev/null || true)"
    fi
    export BOOTSTRAP_BACKUP_PASSPHRASE
  fi

  if [[ -z "${BOOTSTRAP_BACKUP_PASSPHRASE}" ]]; then
    echo "BOOTSTRAP_BACKUP_PASSPHRASE (or BOOTSTRAP_BACKUP_PASSPHRASE_B64) is required when BOOTSTRAP_BACKUP_TO_R2=1" >&2
    exit 6
  fi

  if [[ -z "${KUBECONFIG:-}" && -f /etc/rancher/rke2/rke2.yaml ]]; then
    export KUBECONFIG=/etc/rancher/rke2/rke2.yaml
  fi
  if [[ -z "${KUBECONFIG:-}" || ! -f "${KUBECONFIG}" ]]; then
    echo "KUBECONFIG is missing; expected /etc/rancher/rke2/rke2.yaml" >&2
    echo "Refusing to burn secrets without a recoverable export." >&2
    exit 11
  fi

  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "${tmp_dir}"' EXIT

  kit_dir="${tmp_dir}/emergency-kit"
  mkdir -p "${kit_dir}/cluster" "${kit_dir}/openbao" "${kit_dir}/local"
  chmod 0700 "${kit_dir}"

  archive="${tmp_dir}/emergency-kit.tar.gz"
  encrypted="${tmp_dir}/emergency-kit.tar.gz.enc"
  openbao_backup_json="${tmp_dir}/bootstrap-openbao-backup.json"
  openbao_backup_encrypted="${tmp_dir}/bootstrap-openbao-backup.json.enc"

  cat >"${kit_dir}/README.txt" <<'TXT'
This is an encrypted emergency kit produced by Phase 99 (final recovery export).

Contents:
- cluster/: kubeconfig + join token + Rancher bootstrap password (if available)
- openbao/: bootstrap token + exported OpenBao bootstrap paths (JSON)
- local/: pre-burn /var/lib/bootstrap-secrets snapshot (includes openbao-init.json if it existed)

Decrypt + extract (macOS/Linux):

  export BOOTSTRAP_BACKUP_PASSPHRASE='...'
  # If you only have BOOTSTRAP_BACKUP_PASSPHRASE_B64, decode it first:
  #   export BOOTSTRAP_BACKUP_PASSPHRASE="$(python3 -c 'import base64,os; print(base64.b64decode(os.environ[\"BOOTSTRAP_BACKUP_PASSPHRASE_B64\"]).decode())')"
  openssl enc -d -aes-256-gcm -salt -pbkdf2 -iter 200000 \
    -in bootstrap-emergency-kit.tar.gz.enc -out emergency-kit.tar.gz \
    -pass env:BOOTSTRAP_BACKUP_PASSPHRASE \
  || openssl enc -d -aes-256-cbc -salt -pbkdf2 -iter 200000 \
    -in bootstrap-emergency-kit.tar.gz.enc -out emergency-kit.tar.gz \
    -pass env:BOOTSTRAP_BACKUP_PASSPHRASE

  mkdir -p emergency-kit
  tar -xzf emergency-kit.tar.gz -C emergency-kit

Windows:
- Recommended: use WSL (Ubuntu) and run the same commands as Linux.
- Or install OpenSSL for Windows (or Git for Windows, which includes OpenSSL),
  then run the same commands in Git Bash / PowerShell.

To confirm OpenBao unseal keys are present:
- Look for: local/openbao-init.json

To log into OpenBao quickly:
- Use: openbao/login-token
- Also present: openbao/bootstrap-token
- If Phase 40 init output existed, the root token is also copied to:
  openbao/root-login-token

Alternate format:
- If you uploaded `bootstrap-emergency-kit.7z`, it is a passworded 7z archive.
  Most desktop archive tools will prompt for the password when opening it.
TXT
  chmod 0600 "${kit_dir}/README.txt"

  # Copy kubeconfig (this is what you need to recover cluster access off-node).
  cp -f "${KUBECONFIG}" "${kit_dir}/cluster/kubeconfig.yaml"
  chmod 0600 "${kit_dir}/cluster/kubeconfig.yaml"

  # Capture join token (handy for adding nodes later).
  if [[ -f /var/lib/rancher/rke2/server/node-token ]]; then
    tr -d '\n' </var/lib/rancher/rke2/server/node-token >"${kit_dir}/cluster/rke2-node-token"
    chmod 0600 "${kit_dir}/cluster/rke2-node-token"
  fi

  # Capture Rancher bootstrap password from cluster (source of truth).
  rancher_pw="$("${kubectl_bin}" -n cattle-system get secret bootstrap-secret -o jsonpath='{.data.bootstrapPassword}' 2>/dev/null | base64 -d || true)"
  if [[ -n "${rancher_pw}" ]]; then
    printf '%s\n' "${rancher_pw}" >"${kit_dir}/cluster/rancher-bootstrap-password"
    chmod 0600 "${kit_dir}/cluster/rancher-bootstrap-password"
  fi

  # Capture Argo CD initial admin password from cluster (when present).
  # If you override Argo CD admin password via Helm values, this secret may not exist.
  argocd_pw="$("${kubectl_bin}" -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || true)"
  if [[ -n "${argocd_pw}" ]]; then
    printf '%s\n' "${argocd_pw}" >"${kit_dir}/cluster/argocd-initial-admin-password"
    chmod 0600 "${kit_dir}/cluster/argocd-initial-admin-password"
  fi

  # Capture Gitea admin password from the in-cluster secret (when present).
  # This is the source of truth if the chart is configured with existingSecret=gitea-admin-secret.
  gitea_pw_b64="$("${kubectl_bin}" -n gitea get secret gitea-admin-secret -o jsonpath='{.data.password}' 2>/dev/null || true)"
  if [[ -n "${gitea_pw_b64}" ]]; then
    gitea_pw="$(printf '%s' "${gitea_pw_b64}" | base64 -d 2>/dev/null || true)"
  if [[ -n "${gitea_pw}" ]]; then
      printf '%s\n' "${gitea_pw}" >"${kit_dir}/cluster/gitea-admin-password"
      chmod 0600 "${kit_dir}/cluster/gitea-admin-password"
    else
      echo "[phase99] warning: gitea-admin-secret password is empty; not writing cluster/gitea-admin-password into the kit" >&2
    fi
  fi

  # Capture Headlamp admin token when it exists.
  headlamp_token="$("${kubectl_bin}" -n headlamp get secret headlamp-admin-token -o jsonpath='{.data.token}' 2>/dev/null | base64 -d || true)"
  if [[ -n "${headlamp_token}" ]]; then
    printf '%s\n' "headlamp" >"${kit_dir}/cluster/headlamp-admin-username"
    printf '%s\n' "${headlamp_token}" >"${kit_dir}/cluster/headlamp-admin-token"
    chmod 0600 "${kit_dir}/cluster/headlamp-admin-username" "${kit_dir}/cluster/headlamp-admin-token" 2>/dev/null || true
  fi

  # Capture ingress VIP configuration and current Service state.
  write_ingress_vip_backup_configmap "${kit_dir}/cluster/ingress-vip-config.yaml"
  # Authoritative public front door: nginx ingress controller Service state.
  for svc_ref in kube-system/rke2-ingress-nginx-controller ingress-nginx/ingress-nginx-controller; do
    svc_ns="${svc_ref%%/*}"
    svc_name="${svc_ref##*/}"
    if "${kubectl_bin}" -n "${svc_ns}" get svc "${svc_name}" >/dev/null 2>&1; then
      "${kubectl_bin}" -n "${svc_ns}" get svc "${svc_name}" -o yaml >"${kit_dir}/cluster/nginx-controller-service.yaml" || true
      chmod 0600 "${kit_dir}/cluster/nginx-controller-service.yaml" 2>/dev/null || true
      break
    fi
  done

  # Capture OpenBao bootstrap token from cluster (if present).
  openbao_token="$("${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token -o jsonpath='{.data.token}' 2>/dev/null | base64 -d || true)"

  # As a fallback (pre-burn only), read root token from init output if it exists locally.
  init_file="${BOOTSTRAP_SECRET_DIR}/openbao-init.json"
  require_init_backup="$(printf '%s' "${BOOTSTRAP_REQUIRE_OPENBAO_INIT_BACKUP}" | tr '[:upper:]' '[:lower:]')"
  if [[ "${require_init_backup}" == "1" || "${require_init_backup}" == "true" ]]; then
    if [[ ! -f "${init_file}" ]]; then
      echo "OpenBao init output is missing: ${init_file}" >&2
      echo "Cannot continue burn-the-ladder: emergency kit would not include unseal keys." >&2
      echo "No local secrets were deleted. Fix: ensure Phase 40 created ${init_file} (or set BOOTSTRAP_REQUIRE_OPENBAO_INIT_BACKUP=0)." >&2
      strict="$(printf '%s' "${BOOTSTRAP_BREAKGLASS_STRICT}" | tr '[:upper:]' '[:lower:]')"
      if [[ "${strict}" == "1" || "${strict}" == "true" ]]; then
        exit 13
      fi
      exit 0
    fi
  fi
  if [[ -z "${openbao_token}" && -f "${init_file}" ]]; then
    openbao_token="$(python3 - <<'PY' "${init_file}"
import json,sys
path=sys.argv[1]
try:
  with open(path,"r",encoding="utf-8") as f:
    print(json.load(f).get("root_token",""))
except Exception:
  print("")
PY
)"
  fi
  if [[ -n "${openbao_token}" ]]; then
    printf '%s\n' "${openbao_token}" >"${kit_dir}/openbao/bootstrap-token"
    printf '%s\n' "${openbao_token}" >"${kit_dir}/openbao/login-token"
    chmod 0600 "${kit_dir}/openbao/bootstrap-token"
    chmod 0600 "${kit_dir}/openbao/login-token"
  fi
  openbao_root_token=""
  if [[ -f "${init_file}" ]]; then
    openbao_root_token="$(python3 - <<'PY' "${init_file}"
import json,sys
path=sys.argv[1]
try:
  with open(path,"r",encoding="utf-8") as f:
    print(json.load(f).get("root_token",""))
except Exception:
  print("")
PY
)"
    if [[ -n "${openbao_root_token}" ]]; then
      printf '%s\n' "${openbao_root_token}" >"${kit_dir}/openbao/root-login-token"
      chmod 0600 "${kit_dir}/openbao/root-login-token" 2>/dev/null || true
    fi
  fi

  # Export the bootstrap paths from OpenBao (the intended post-burn source of truth).
  # We treat this as required: if OpenBao export fails, we do not burn local secrets.
  if [[ -z "${openbao_token}" ]]; then
    echo "OpenBao bootstrap token is missing (secret ${OPENBAO_NAMESPACE}/openbao-bootstrap-token not found and no local init output)." >&2
    echo "Refusing to burn secrets without a recoverable export." >&2
    exit 12
  fi

  # Discover every workload path instead of maintaining a second inventory in
  # this recovery script. A credential may have rotated only under secret/apps;
  # burning the node-local copies without exporting that current value would
  # make OpenBao a single point of loss rather than the secret authority.
  discovered_app_paths=()
  discover_openbao_leaf_paths() {
    local prefix="${1:?OpenBao KV prefix is required}"
    local listing=""
    local entry=""
    local -a entries=()

    if ! listing="$("${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv list -format=json "${prefix}" 2>/dev/null)"; then
      return 1
    fi
    mapfile -t entries < <(python3 -c '
import json, sys
value = json.load(sys.stdin)
if isinstance(value, dict):
    value = value.get("keys", [])
for item in value:
    print(item)
' <<<"${listing}")
    for entry in "${entries[@]}"; do
      if [[ "${entry}" == */ ]]; then
        discover_openbao_leaf_paths "${prefix}/${entry%/}" || return 1
      elif [[ -n "${entry}" ]]; then
        discovered_app_paths+=("${prefix}/${entry}")
      fi
    done
  }

  if ! discover_openbao_leaf_paths secret/apps || [[ "${#discovered_app_paths[@]}" -eq 0 ]]; then
    echo "Unable to inventory OpenBao application secrets under secret/apps." >&2
    echo "Refusing to burn local secrets without a complete workload-secret export." >&2
    exit 12
  fi

  # Package bootstrap recovery records and every discovered workload path both
  # inside the emergency kit and as a standalone encrypted OpenBao backup.
  exported_openbao_paths=()
  openbao_paths=(
    secret/bootstrap/rancher
    secret/bootstrap/argocd
    secret/bootstrap/platform
    "${discovered_app_paths[@]}"
  )
  app_export_failed=0
  for p in "${openbao_paths[@]}"; do
    out="${kit_dir}/openbao/$(printf '%s' "${p}" | tr '/' '_').json"
    if "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv get -format=json "${p}" >"${out}" 2>/dev/null; then
      chmod 0600 "${out}"
      exported_openbao_paths+=("${p}")
    else
      rm -f "${out}" 2>/dev/null || true
      if [[ "${p}" == secret/apps/* ]]; then
        app_export_failed=1
      fi
    fi
  done
  if [[ "${app_export_failed}" -ne 0 ]]; then
    echo "One or more OpenBao application secrets changed or became unreadable during export." >&2
    echo "Refusing to burn local secrets; rerun Phase 99 after OpenBao is stable." >&2
    exit 12
  fi

  headlamp_admin_username="$(read_local_secret_file "${BOOTSTRAP_SECRET_DIR}/headlamp_admin_username")"
  headlamp_admin_token="$(read_local_secret_file "${BOOTSTRAP_SECRET_DIR}/headlamp_admin_token")"
  if [[ -z "${headlamp_admin_username}" && -f "${kit_dir}/cluster/headlamp-admin-username" ]]; then
    headlamp_admin_username="$(read_local_secret_file "${kit_dir}/cluster/headlamp-admin-username")"
  fi
  if [[ -z "${headlamp_admin_token}" && -f "${kit_dir}/cluster/headlamp-admin-token" ]]; then
    headlamp_admin_token="$(read_local_secret_file "${kit_dir}/cluster/headlamp-admin-token")"
  fi

  python3 - <<'PY' "${openbao_backup_json}" "${kit_dir}" "${HOSTNAME:-$(hostname -s 2>/dev/null || echo node-bootstrap)}" "${OPENBAO_NAMESPACE}" "${OPENBAO_POD}" "${openbao_token}" "${openbao_root_token}" "${headlamp_admin_username}" "${headlamp_admin_token}" "${exported_openbao_paths[@]}"
import json
import os
import socket
import sys
from pathlib import Path

out_path = Path(sys.argv[1])
kit_dir = Path(sys.argv[2]) / "openbao"
host = sys.argv[3] or socket.gethostname()
namespace = sys.argv[4]
pod = sys.argv[5]
token = sys.argv[6]
root_token = sys.argv[7]
headlamp_admin_username = sys.argv[8]
headlamp_admin_token = sys.argv[9]
exported_paths = sys.argv[10:]

doc = {
    "kind": "bootstrap-openbao-backup",
    "version": 1,
    "generated_by": "phase99",
    "generated_at_utc": __import__("datetime").datetime.utcnow().replace(microsecond=0).isoformat() + "Z",
    "host": host,
    "openbao_namespace": namespace,
    "openbao_pod": pod,
    "bootstrap_token_present": bool(token),
    "bootstrap_token": token,
    "login_token": token,
    "root_login_token_present": bool(root_token),
    "root_login_token": root_token,
    "paths": {},
    "recovery_credentials": {
        "headlamp_admin_username": headlamp_admin_username,
        "headlamp_admin_token": headlamp_admin_token,
    },
}

for path_name in exported_paths:
    file_name = path_name.replace("/", "_") + ".json"
    file_path = kit_dir / file_name
    try:
        doc["paths"][path_name] = json.loads(file_path.read_text(encoding="utf-8"))
    except Exception as exc:
        doc["paths"][path_name] = {"_export_error": str(exc)}

out_path.write_text(json.dumps(doc, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
  chmod 0600 "${openbao_backup_json}" 2>/dev/null || true
  cp -f "${openbao_backup_json}" "${kit_dir}/openbao/bootstrap-openbao-backup.json"
  chmod 0600 "${kit_dir}/openbao/bootstrap-openbao-backup.json" 2>/dev/null || true

  # Preserve the pre-burn local secrets directory as a last-resort recovery artifact.
  # (Still encrypted at rest in R2. This makes the backup complete even if OpenBao paths were incomplete.)
  cp -a "${BOOTSTRAP_SECRET_DIR}/." "${kit_dir}/local/" 2>/dev/null || true
  chmod -R go-rwx "${kit_dir}/local" 2>/dev/null || true

  # Simple manifest (helps confirm what was captured without extracting everything).
  (cd "${kit_dir}" && find . -type f -maxdepth 3 | LC_ALL=C sort) >"${kit_dir}/MANIFEST.txt"
  chmod 0600 "${kit_dir}/MANIFEST.txt"

  format="$(printf '%s' "${BOOTSTRAP_BACKUP_FORMAT}" | tr '[:upper:]' '[:lower:]')"
  if [[ -z "${format}" ]]; then
    format="openssl"
  fi
  case "${format}" in
    openssl|7z|both) ;;
    *)
      echo "Invalid BOOTSTRAP_BACKUP_FORMAT=${BOOTSTRAP_BACKUP_FORMAT} (expected openssl|7z|both)" >&2
      exit 14
      ;;
  esac

  openssl_encrypt() {
    local out_path="${1:?out}"
    echo "[phase99] creating openssl-encrypted kit: $(basename "${out_path}")"
    tar -C "${kit_dir}" -czf "${archive}" .

    # Encrypt using a passphrase provided out-of-band. This keeps secrets out of R2 in plaintext.
    # Prefer AES-256-GCM; fall back to AES-256-CBC when OpenSSL enc lacks AEAD support.
    local encrypt_err=""
    if ! encrypt_err="$(openssl enc -aes-256-gcm -salt -pbkdf2 -iter 200000 \
      -in "${archive}" -out "${out_path}" \
      -pass env:BOOTSTRAP_BACKUP_PASSPHRASE 2>&1)"; then
      if printf '%s' "${encrypt_err}" | grep -qi "AEAD ciphers not supported"; then
        echo "[phase99] OpenSSL enc does not support AEAD ciphers; falling back to aes-256-cbc"
        rm -f "${out_path}" 2>/dev/null || true
        openssl enc -aes-256-cbc -salt -pbkdf2 -iter 200000 \
          -in "${archive}" -out "${out_path}" \
          -pass env:BOOTSTRAP_BACKUP_PASSPHRASE
      else
        echo "OpenSSL enc failed: ${encrypt_err}" >&2
        return 7
      fi
    fi
  }

  encrypt_openbao_backup() {
    local src_path="${1:?src}"
    local out_path="${2:?out}"
    local encrypt_err=""
    echo "[phase99] creating standalone OpenBao backup: $(basename "${out_path}")"
    if ! encrypt_err="$(openssl enc -aes-256-gcm -salt -pbkdf2 -iter 200000 \
      -in "${src_path}" -out "${out_path}" \
      -pass env:BOOTSTRAP_BACKUP_PASSPHRASE 2>&1)"; then
      if printf '%s' "${encrypt_err}" | grep -qi "AEAD ciphers not supported"; then
        echo "[phase99] OpenSSL enc does not support AEAD ciphers for OpenBao backup; falling back to aes-256-cbc"
        rm -f "${out_path}" 2>/dev/null || true
        openssl enc -aes-256-cbc -salt -pbkdf2 -iter 200000 \
          -in "${src_path}" -out "${out_path}" \
          -pass env:BOOTSTRAP_BACKUP_PASSPHRASE
      else
        echo "OpenBao backup encryption failed: ${encrypt_err}" >&2
        return 20
      fi
    fi
  }

  ensure_7z() {
    if command -v 7z >/dev/null 2>&1 || command -v 7za >/dev/null 2>&1; then
      return 0
    fi

    try_install_7z() {
      # Different repos/distros may package 7-Zip under different names.
      dnf -y install p7zip p7zip-plugins >/dev/null 2>&1 && return 0
      dnf -y install p7zip >/dev/null 2>&1 && return 0
      dnf -y install 7zip >/dev/null 2>&1 && return 0
      dnf -y install 7zip-plugins >/dev/null 2>&1 && return 0
      return 1
    }

    install_flag="$(printf '%s' "${BOOTSTRAP_INSTALL_7Z}" | tr '[:upper:]' '[:lower:]')"
    if [[ "${install_flag}" != "1" && "${install_flag}" != "true" ]]; then
      return 1
    fi
    if ! command -v dnf >/dev/null 2>&1; then
      return 1
    fi

    echo "[phase99] 7z not found; attempting to install p7zip"
    if try_install_7z; then
      return 0
    fi

    # Many EL-based distros provide p7zip via EPEL, and EPEL often requires CRB/PowerTools.
    echo "[phase99] p7zip install failed; attempting to enable CRB/PowerTools + EPEL"
    dnf -y install dnf-plugins-core >/dev/null 2>&1 || true
    dnf config-manager --set-enabled crb >/dev/null 2>&1 || true
    dnf config-manager --set-enabled powertools >/dev/null 2>&1 || true
    dnf config-manager --set-enabled PowerTools >/dev/null 2>&1 || true

    if dnf -y install epel-release >/dev/null 2>&1; then
      if try_install_7z; then
        return 0
      fi
    fi

    if command -v curl >/dev/null 2>&1; then
      major="$(cat /etc/dnf/vars/releasever 2>/dev/null || true)"
      if [[ -z "${major}" && -f /etc/os-release ]]; then
        # shellcheck disable=SC1091
        . /etc/os-release || true
        major="${VERSION_ID%%.*}"
      fi
      major="${major:-10}"
      epel_rpm_url="https://dl.fedoraproject.org/pub/epel/epel-release-latest-${major}.noarch.rpm"
      tmp_rpm="$(mktemp)"
      if curl -fsSL -o "${tmp_rpm}" "${epel_rpm_url}"; then
        dnf -y install "${tmp_rpm}" >/dev/null 2>&1 || true
        rm -f "${tmp_rpm}" >/dev/null 2>&1 || true
        if try_install_7z; then
          return 0
        fi
      else
        rm -f "${tmp_rpm}" >/dev/null 2>&1 || true
      fi
    fi

    return 1
  }

  create_7z() {
    local out_path="${1:?out}"
    local seven=""
    ensure_7z || true
    if command -v 7z >/dev/null 2>&1; then
      seven="$(command -v 7z)"
    elif command -v 7za >/dev/null 2>&1; then
      seven="$(command -v 7za)"
    fi
    if [[ -z "${seven}" ]]; then
      echo "7z is not installed; cannot create a .7z emergency kit." >&2
      echo "Fix: install 7-Zip/p7zip, or set BOOTSTRAP_BACKUP_FORMAT=openssl." >&2
      return 15
    fi

    echo "[phase99] creating passworded 7z kit: $(basename "${out_path}")"
    # Note: 7z receives the password via -p... which appears in the process args while running.
    # If you require strict handling, prefer BOOTSTRAP_BACKUP_FORMAT=openssl with OpenSSL in FIPS mode.
    (
      cd "${kit_dir}"
      # 7z format uses AES-256 internally; `-mem=AES256` can fail on some p7zip builds.
      "${seven}" a -t7z -mhe=on -mx=3 \
        -p"${BOOTSTRAP_BACKUP_PASSPHRASE}" \
        "${out_path}" \
        ./README.txt ./MANIFEST.txt ./cluster ./openbao ./local >/dev/null
    )
  }

  host="$(hostname -s 2>/dev/null || echo node-bootstrap)"

  backup_passphrase_fingerprint() {
    python3 - <<'PY' "${BOOTSTRAP_BACKUP_PASSPHRASE:-}"
import hashlib
import sys
value = sys.argv[1] if len(sys.argv) > 1 else ""
print(hashlib.sha256(value.encode("utf-8")).hexdigest(), end="")
PY
  }

  upload_one() {
    local file_name="${1:?name}"
    local src_path="${2:?path}"
    local upload_url="${BOOTSTRAP_BACKUP_URL}"
    local sep="?"
    local -a curl_auth=()
    if [[ -n "${BOOTSTRAP_SHARED_TOKEN}" ]]; then
      curl_auth=(-H "x-ks-token: ${BOOTSTRAP_SHARED_TOKEN}")
    fi
    if printf '%s' "${upload_url}" | grep -q '?'; then
      sep="&"
    fi
    upload_url="${upload_url}${sep}host=${host}&file=${file_name}"
    echo "[phase99] uploading backup to $(redact_url "${upload_url}")"
    curl -fsSL --connect-timeout 5 --max-time 60 --retry 3 -X POST \
      "${curl_auth[@]}" \
      --data-binary @"${src_path}" \
      "${upload_url}"
  }

  upload_backup_fingerprint() {
    local archive_name="${1:?name}"
    local fingerprint=""
    local sidecar=""
    fingerprint="$(backup_passphrase_fingerprint)"
    if [[ -z "${fingerprint}" ]]; then
      return 0
    fi
    sidecar="$(mktemp)"
    printf '%s\n' "${fingerprint}" > "${sidecar}"
    if ! upload_one "${archive_name}.passphrase.sha256" "${sidecar}"; then
      echo "[phase99] warning: failed to upload backup fingerprint sidecar for ${archive_name}; continuing without sidecar" >&2
    fi
    rm -f "${sidecar}" >/dev/null 2>&1 || true
  }

  scrub_bootstrap_env_passphrase() {
    # Best-effort: remove the passphrase from the on-node EnvironmentFile so it isn't left behind after burn-ladder.
    # Source of truth should be your operator-controlled secret store (e.g. workstation password manager).
    local env_file="/etc/ansible-bundle-bootstrap.env"
    if [[ ! -f "${env_file}" ]]; then
      return 0
    fi
    sed -i \
      -e 's/^BOOTSTRAP_BACKUP_PASSPHRASE=.*/BOOTSTRAP_BACKUP_PASSPHRASE=""/' \
      -e 's/^BOOTSTRAP_BACKUP_PASSPHRASE_B64=.*/BOOTSTRAP_BACKUP_PASSPHRASE_B64=""/' \
      "${env_file}" >/dev/null 2>&1 || true
    chmod 0600 "${env_file}" >/dev/null 2>&1 || true
  }

  if [[ "${format}" == "openssl" ]]; then
    file_name="${BOOTSTRAP_BACKUP_FILE}"
    if [[ ! "${file_name}" =~ \.enc$ ]]; then
      echo "BOOTSTRAP_BACKUP_FILE must end with .enc when BOOTSTRAP_BACKUP_FORMAT=openssl (got: ${file_name})." >&2
      exit 16
    fi
    openssl_encrypt "${encrypted}"
    encrypt_openbao_backup "${openbao_backup_json}" "${openbao_backup_encrypted}"
    upload_one "${file_name}" "${encrypted}"
    upload_backup_fingerprint "${file_name}"
    upload_one "${BOOTSTRAP_OPENBAO_BACKUP_FILE}" "${openbao_backup_encrypted}"
    upload_backup_fingerprint "${BOOTSTRAP_OPENBAO_BACKUP_FILE}"
    scrub_bootstrap_env_passphrase
  elif [[ "${format}" == "7z" ]]; then
    file_name="${BOOTSTRAP_BACKUP_FILE}"
    if [[ ! "${file_name}" =~ \.7z$ ]]; then
      echo "BOOTSTRAP_BACKUP_FILE must end with .7z when BOOTSTRAP_BACKUP_FORMAT=7z (got: ${file_name})." >&2
      exit 17
    fi
    seven_out="${tmp_dir}/emergency-kit.7z"
    create_7z "${seven_out}"
    upload_one "${file_name}" "${seven_out}"
    upload_backup_fingerprint "${file_name}"
    encrypt_openbao_backup "${openbao_backup_json}" "${openbao_backup_encrypted}"
    upload_one "${BOOTSTRAP_OPENBAO_BACKUP_FILE}" "${openbao_backup_encrypted}"
    upload_backup_fingerprint "${BOOTSTRAP_OPENBAO_BACKUP_FILE}"
    scrub_bootstrap_env_passphrase
  else
    file_name_openssl="${BOOTSTRAP_BACKUP_FILE_OPENSSL}"
    file_name_7z="${BOOTSTRAP_BACKUP_FILE_7Z}"
    if [[ ! "${file_name_openssl}" =~ \.enc$ ]]; then
      echo "BOOTSTRAP_BACKUP_FILE_OPENSSL must end with .enc (got: ${file_name_openssl})." >&2
      exit 18
    fi
    if [[ ! "${file_name_7z}" =~ \.7z$ ]]; then
      echo "BOOTSTRAP_BACKUP_FILE_7Z must end with .7z (got: ${file_name_7z})." >&2
      exit 19
    fi
    openssl_encrypt "${encrypted}"
    encrypt_openbao_backup "${openbao_backup_json}" "${openbao_backup_encrypted}"
    seven_out="${tmp_dir}/emergency-kit.7z"
    upload_one "${file_name_openssl}" "${encrypted}"
    upload_backup_fingerprint "${file_name_openssl}"
    upload_one "${BOOTSTRAP_OPENBAO_BACKUP_FILE}" "${openbao_backup_encrypted}"
    upload_backup_fingerprint "${BOOTSTRAP_OPENBAO_BACKUP_FILE}"
    if create_7z "${seven_out}"; then
      upload_one "${file_name_7z}" "${seven_out}"
      upload_backup_fingerprint "${file_name_7z}"
    else
      echo "[phase99] warning: skipping .7z upload (7z unavailable). OpenSSL kit uploaded successfully." >&2
      echo "[phase99] hint: you can download the .enc kit and repackage it into .7z on your workstation if desired." >&2
    fi
    scrub_bootstrap_env_passphrase
  fi
fi

# The declarative config Job and External Secrets now authenticate through
# scoped Kubernetes roles. Keeping the initial root token in Kubernetes after
# the encrypted recovery export would make that delivery copy a second secret
# authority and a standing cluster-escalation path.
"${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" delete secret openbao-bootstrap-token \
  --ignore-not-found >/dev/null

if [[ -d "${BOOTSTRAP_SECRET_DIR}" ]]; then
  find "${BOOTSTRAP_SECRET_DIR}" -type f -exec shred -u {} + 2>/dev/null || true
  find "${BOOTSTRAP_SECRET_DIR}" -depth -type d -empty -delete 2>/dev/null || true
  if [[ -d "${BOOTSTRAP_SECRET_DIR}" ]]; then
    rm -rf "${BOOTSTRAP_SECRET_DIR}" 2>/dev/null || true
  fi
fi

# Phase 99 can be run manually, outside the outer bundle orchestrator. Remove
# the downloaded secret payload and kickstart EnvironmentFile here as well so
# manual burn-the-ladder has the same residual-secret boundary. The outer
# orchestrator repeats this idempotently for join nodes, which skip Phase 99.
for first_boot_env_file in \
  "${BOOTSTRAP_RUNTIME_ENV_FILE:-/etc/bootstrap-runtime.env}" \
  /etc/ansible-bundle-bootstrap.env \
  /etc/tailscale-firstboot.env; do
  if [[ ! -e "${first_boot_env_file}" ]]; then
    continue
  fi
  if command -v shred >/dev/null 2>&1; then
    shred -u -- "${first_boot_env_file}" 2>/dev/null || rm -f -- "${first_boot_env_file}"
  else
    rm -f -- "${first_boot_env_file}"
  fi
  if [[ -e "${first_boot_env_file}" ]]; then
    echo "Failed to remove first-boot credential file: ${first_boot_env_file}" >&2
    exit 21
  fi
done

echo "Bootstrap secrets removed."
echo "If Argo CD is not deployed yet, run Phase 50 before burning secrets next time."
