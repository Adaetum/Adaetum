#!/usr/bin/env bash
set -euo pipefail

BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR:-/var/lib/bootstrap-secrets}"
OUTFILE="${BOOTSTRAP_SECRET_DIR}/openbao-init.json"
NAMESPACE="${OPENBAO_NAMESPACE:-openbao}"
POD="${OPENBAO_POD:-openbao-0}"
KUBECTL_BIN="${KUBECTL_BIN:-}"

if [[ -n "${KUBECTL_BIN}" ]]; then
  if [[ ! -x "${KUBECTL_BIN}" ]]; then
    echo "KUBECTL_BIN is set but not executable: ${KUBECTL_BIN}" >&2
    exit 1
  fi
else
  if command -v kubectl >/dev/null 2>&1; then
    KUBECTL_BIN="$(command -v kubectl)"
  elif [[ -x /var/lib/rancher/rke2/bin/kubectl ]]; then
    KUBECTL_BIN="/var/lib/rancher/rke2/bin/kubectl"
  else
    echo "kubectl not found; set KUBECTL_BIN or ensure kubectl is on PATH." >&2
    exit 1
  fi
fi

mkdir -p "${BOOTSTRAP_SECRET_DIR}"
chmod 0700 "${BOOTSTRAP_SECRET_DIR}"

if [[ -f "${OUTFILE}" ]]; then
  echo "OpenBao already initialized (found ${OUTFILE})."
  exit 0
fi

seal_type="$(${KUBECTL_BIN} -n "${NAMESPACE}" exec "${POD}" -- \
  bao status -format=json 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("type", "shamir"))' 2>/dev/null || true)"
if [[ "${seal_type}" == "transit" ]]; then
  init_key_args=(-recovery-shares=5 -recovery-threshold=3)
else
  init_key_args=(-key-shares=5 -key-threshold=3)
fi

"${KUBECTL_BIN}" -n "${NAMESPACE}" exec -i "${POD}" -- \
  bao operator init "${init_key_args[@]}" -format=json >"${OUTFILE}"

chmod 0600 "${OUTFILE}"

key_kind="$(python3 - "${OUTFILE}" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as stream:
    data = json.load(stream)
print("recovery" if data.get("recovery_keys_b64") or data.get("recovery_keys") else "unseal")
PY
)"

cat <<INFO

OpenBao initialized.
- Store ${key_kind} keys offline.
- Store the root token offline.
- Apply post-openbao config via Argo CD.

INFO
