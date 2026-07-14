#!/usr/bin/env bash
set -euo pipefail

# Phase 10 is intentionally "no secrets": validate the repo and prepare public
# bootstrap artifacts (KS templates, ISO modifications, etc.).
#
# This script is a convenience wrapper around existing repo tooling.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
cd "${repo_root}"

echo "[phase10] repo: ${repo_root}"

run_phase10_check() {
  local label="${1:?label}"
  shift
  echo "[phase10] ${label}"
  "$@"
}

if command -v prek >/dev/null 2>&1; then
  run_phase10_check "running hooks via prek (Rust pre-commit runner)" prek run --all-files
elif command -v pre-commit >/dev/null 2>&1; then
  run_phase10_check "running hooks via pre-commit" pre-commit run --all-files
else
  echo "[phase10] no hook runner found; skipping (install and run: prek run --all-files)"
fi

if ! command -v task >/dev/null 2>&1; then
  echo "[phase10] task is required for the supported Phase 10 path" >&2
  exit 1
fi

if [[ "${ADAETUM_CONFIG_CONTRACT:-platform/v1alpha1}" == "platform/v1alpha1" ]]; then
  run_phase10_check "validating fork platform profile" task platform:validate
fi

run_phase10_check "running task bootstrap:phase10:check-ks" task bootstrap:phase10:check-ks
run_phase10_check "running task bootstrap:phase10:compile-ks" task bootstrap:phase10:compile-ks
run_phase10_check "running task bootstrap:phase10:validate-runtime" task bootstrap:phase10:validate-runtime
run_phase10_check "running task bootstrap:phase10:validate-pods-contract" task bootstrap:phase10:validate-pods-contract

cat <<'INFO'

Phase 10 complete.

This phase is the fast-fail gate. If Phase 10 passes, the repo hook run,
kickstart validation, runtime validation, and pods contract checks all succeeded.

INFO
