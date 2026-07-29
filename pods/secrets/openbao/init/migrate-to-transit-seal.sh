#!/usr/bin/env bash
set -euo pipefail

# This is an explicit, one-time seal migration. It is intentionally separate
# from routine reconciliation because a seal migration restarts OpenBao and
# requires both the old Shamir shares and the new external Transit service.
BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR:-/var/lib/bootstrap-secrets}"
INIT_FILE="${OPENBAO_INIT_FILE:-${BOOTSTRAP_SECRET_DIR}/openbao-init.json}"
SNAPSHOT_FILE="${OPENBAO_TRANSIT_MIGRATION_SNAPSHOT:-${BOOTSTRAP_SECRET_DIR}/openbao-pre-transit-migration.snap}"
NAMESPACE="${OPENBAO_NAMESPACE:-openbao}"
POD="${OPENBAO_POD:-openbao-0}"
STATEFULSET="${OPENBAO_STATEFULSET:-openbao}"
KUBECTL_BIN="${KUBECTL_BIN:-}"

if [[ "${OPENBAO_TRANSIT_SEAL_MIGRATE:-}" != "1" && "${OPENBAO_TRANSIT_SEAL_MIGRATE:-}" != "true" ]]; then
  echo "Refusing seal migration without OPENBAO_TRANSIT_SEAL_MIGRATE=1." >&2
  exit 2
fi

if [[ -z "${KUBECTL_BIN}" ]]; then
  if command -v kubectl >/dev/null 2>&1; then
    KUBECTL_BIN="$(command -v kubectl)"
  elif [[ -x /var/lib/rancher/rke2/bin/kubectl ]]; then
    KUBECTL_BIN="/var/lib/rancher/rke2/bin/kubectl"
  else
    echo "kubectl not found; set KUBECTL_BIN." >&2
    exit 1
  fi
fi

if [[ ! -f "${INIT_FILE}" ]]; then
  echo "Missing ${INIT_FILE}; restore openbao-init.json from the encrypted emergency kit." >&2
  exit 1
fi

if ! "${KUBECTL_BIN}" -n "${NAMESPACE}" get secret openbao-transit-seal >/dev/null 2>&1; then
  echo "Missing ${NAMESPACE}/openbao-transit-seal; run platform bootstrap with the Transit seal values first." >&2
  exit 1
fi

seal_type="$(${KUBECTL_BIN} -n "${NAMESPACE}" exec "${POD}" -- bao status -format=json 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("type", "unknown"))' || true)"
if [[ "${seal_type}" == "transit" ]]; then
  echo "OpenBao already uses the Transit seal; no migration required."
  exit 0
fi
if [[ "${seal_type}" != "shamir" ]]; then
  echo "Expected the current seal type to be shamir; found ${seal_type:-unknown}." >&2
  exit 1
fi

# Prove the external root of trust can both wrap and unwrap data before the
# current Shamir process is stopped. A successful health check alone would not
# establish that the scoped token can use the configured key.
probe_pod="openbao-transit-seal-preflight"
openbao_image="$("${KUBECTL_BIN}" -n "${NAMESPACE}" get pod "${POD}" -o jsonpath='{.spec.containers[?(@.name=="openbao")].image}')"
cleanup_probe() {
  "${KUBECTL_BIN}" -n "${NAMESPACE}" delete pod "${probe_pod}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup_probe EXIT
cleanup_probe
cat <<YAML | "${KUBECTL_BIN}" apply -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: ${probe_pod}
  namespace: ${NAMESPACE}
spec:
  restartPolicy: Never
  containers:
    - name: transit-preflight
      image: ${openbao_image}
      envFrom:
        - secretRef:
            name: openbao-transit-seal
      command: ["sh", "-ec"]
      args:
        - |
          plaintext="\$(printf '%s' adaetum-transit-preflight | base64 | tr -d '\\n')"
          mount_path="\${VAULT_TRANSIT_SEAL_MOUNT_PATH%/}"
          ciphertext="\$(bao write -field=ciphertext "\${mount_path}/encrypt/\${VAULT_TRANSIT_SEAL_KEY_NAME}" plaintext="\${plaintext}")"
          recovered="\$(bao write -field=plaintext "\${mount_path}/decrypt/\${VAULT_TRANSIT_SEAL_KEY_NAME}" ciphertext="\${ciphertext}" | base64 -d)"
          test "\${recovered}" = adaetum-transit-preflight
      volumeMounts:
        - name: transit-ca
          mountPath: /openbao/transit-seal
          readOnly: true
  volumes:
    - name: transit-ca
      secret:
        secretName: openbao-transit-seal-ca
        optional: true
YAML

probe_deadline=$((SECONDS + 120))
while (( SECONDS < probe_deadline )); do
  probe_phase="$("${KUBECTL_BIN}" -n "${NAMESPACE}" get pod "${probe_pod}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  case "${probe_phase}" in
    Succeeded) break ;;
    Failed)
      "${KUBECTL_BIN}" -n "${NAMESPACE}" logs "${probe_pod}" >&2 || true
      echo "External Transit encrypt/decrypt preflight failed; OpenBao was not restarted." >&2
      exit 1
      ;;
  esac
  sleep 2
done
if [[ "${probe_phase:-}" != "Succeeded" ]]; then
  "${KUBECTL_BIN}" -n "${NAMESPACE}" describe pod "${probe_pod}" >&2 || true
  echo "External Transit encrypt/decrypt preflight timed out; OpenBao was not restarted." >&2
  exit 1
fi
cleanup_probe
trap - EXIT

mapfile -t init_values < <(python3 - "${INIT_FILE}" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as stream:
    data = json.load(stream)
keys = data.get("unseal_keys_b64") or data.get("unseal_keys") or []
token = data.get("root_token") or ""
if len(keys) < 3 or not token:
    raise SystemExit("init output must contain at least three Shamir shares and the root token")
print(token)
for key in keys[:3]:
    print(key)
PY
)
root_token="${init_values[0]}"
unseal_keys=("${init_values[1]}" "${init_values[2]}" "${init_values[3]}")

mkdir -p "$(dirname "${SNAPSHOT_FILE}")"
chmod 0700 "$(dirname "${SNAPSHOT_FILE}")" 2>/dev/null || true
remote_snapshot="/tmp/openbao-pre-transit-migration.snap"
printf '%s\n' "${root_token}" | "${KUBECTL_BIN}" -n "${NAMESPACE}" exec -i "${POD}" -- sh -c \
  'IFS= read -r BAO_TOKEN; export BAO_TOKEN; bao operator raft snapshot save /tmp/openbao-pre-transit-migration.snap'
"${KUBECTL_BIN}" -n "${NAMESPACE}" exec "${POD}" -- cat "${remote_snapshot}" >"${SNAPSHOT_FILE}"
"${KUBECTL_BIN}" -n "${NAMESPACE}" exec "${POD}" -- rm -f "${remote_snapshot}"
chmod 0600 "${SNAPSHOT_FILE}"
if [[ ! -s "${SNAPSHOT_FILE}" ]]; then
  echo "Pre-migration Raft snapshot is empty; refusing to restart OpenBao." >&2
  exit 1
fi
unset root_token

echo "Saved pre-migration Raft snapshot to ${SNAPSHOT_FILE}."
"${KUBECTL_BIN}" -n "${NAMESPACE}" rollout restart "statefulset/${STATEFULSET}"
"${KUBECTL_BIN}" -n "${NAMESPACE}" rollout status "statefulset/${STATEFULSET}" --timeout=10m

for key in "${unseal_keys[@]}"; do
  printf '%s\n' "${key}" | "${KUBECTL_BIN}" -n "${NAMESPACE}" exec -i "${POD}" -- sh -c \
    'IFS= read -r unseal_key; bao operator unseal -migrate "${unseal_key}" >/dev/null'
done
unset unseal_keys init_values

status_json="$(${KUBECTL_BIN} -n "${NAMESPACE}" exec "${POD}" -- bao status -format=json)"
python3 - "${SNAPSHOT_FILE}" "${status_json}" <<'PY'
import json
import sys

status = json.loads(sys.argv[2])
if status.get("sealed") is not False or status.get("type") != "transit":
    raise SystemExit(f"migration verification failed: seal={status.get('type')} sealed={status.get('sealed')}")
print(f"OpenBao migrated to Transit auto-unseal. Recovery snapshot: {sys.argv[1]}")
PY
