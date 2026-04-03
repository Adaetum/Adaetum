#!/usr/bin/env bash

# Minimal shared helpers for bootstrap scripts.
# Intended to be sourced from other scripts (do not set -e/-u here).

bootstrap_bool_is_true() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

bootstrap_state_dir() {
  # State is intentionally local-only. It speeds re-runs after partial failure by
  # skipping phases that already completed successfully.
  printf '%s' "${BOOTSTRAP_STATE_DIR:-/var/lib/bootstrap-phase-state}"
}

bootstrap_state_path() {
  local key="${1:-}"
  key="$(printf '%s' "${key}" | tr '/:' '__' | tr -cd '[:alnum:]_.-')"
  printf '%s/%s.done' "$(bootstrap_state_dir)" "${key}"
}

bootstrap_state_has() {
  local key="${1:?key}"
  local path
  path="$(bootstrap_state_path "${key}")"
  test -f "${path}"
}

bootstrap_state_mark() {
  local key="${1:?key}"
  local path
  path="$(bootstrap_state_path "${key}")"
  mkdir -p "$(dirname "${path}")"
  # Leave a small breadcrumb for debugging.
  printf '%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >"${path}"
}

bootstrap_state_reset() {
  local dir
  dir="$(bootstrap_state_dir)"
  rm -rf "${dir}"
}

