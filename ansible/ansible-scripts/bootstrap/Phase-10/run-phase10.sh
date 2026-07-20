#!/usr/bin/env bash
set -euo pipefail

# Phase 10 runs from the bundle copied onto an already-installed host. Source
# validation belongs to maintainer hooks, CI, and artifact publication; this
# phase only rejects a malformed profile or unusable first-boot runtime payload
# before Phase 20 creates bootstrap-local state.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
cd "${repo_root}"

echo "[phase10] repo: ${repo_root}"

run_phase10_check() {
  local label="${1:?label}"
  shift
  echo "[phase10] ${label}"
  "$@"
}

if ! command -v task >/dev/null 2>&1; then
  echo "[phase10] task is required for the supported Phase 10 path" >&2
  exit 1
fi

if [[ "${ADAETUM_CONFIG_CONTRACT:-platform/v1alpha1}" == "platform/v1alpha1" ]]; then
  run_phase10_check "validating fork platform profile" task platform:validate
fi

run_phase10_check "running task bootstrap:phase10:validate-runtime" task bootstrap:phase10:validate-runtime

cat <<'INFO'

Phase 10 complete.

The copied platform profile and first-boot runtime payload passed their intake
checks. Repository hooks and installer artifact checks run before publication.

INFO
