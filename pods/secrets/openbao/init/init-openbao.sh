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

"${KUBECTL_BIN}" -n "${NAMESPACE}" exec -i "${POD}" -- \
  bao operator init -key-shares=5 -key-threshold=3 -format=json | tee "${OUTFILE}"

chmod 0600 "${OUTFILE}"

cat <<'INFO'

OpenBao initialized.
- Store unseal keys offline.
- Store the root token offline.
- Apply post-openbao config via Argo CD.

INFO
