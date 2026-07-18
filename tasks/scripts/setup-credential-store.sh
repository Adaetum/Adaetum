#!/usr/bin/env bash
set -euo pipefail

# Store first-run resume credentials only in an OS-protected credential store.
# Secret values enter through stdin and are never accepted as arguments.

action="${1:-}"
namespace="${2:-}"
key="${3:-}"
service="io.adaetum.setup"
account="${namespace}:${key}"

backend() {
  case "$(uname -s 2>/dev/null || true)" in
    Darwin)
      command -v security >/dev/null 2>&1 && { printf 'macOS Keychain'; return 0; }
      ;;
    Linux)
      command -v secret-tool >/dev/null 2>&1 && { printf 'Secret Service'; return 0; }
      ;;
    MINGW*|MSYS*|CYGWIN*)
      command -v powershell.exe >/dev/null 2>&1 && { printf 'Windows DPAPI'; return 0; }
      ;;
  esac
  return 1
}

windows_helper() {
  local helper="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)/setup-credential-store-windows.ps1"
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "${helper}"
  else
    printf '%s' "${helper}"
  fi
}

[ -n "${action}" ] || { echo "usage: $0 available|get|set|delete [namespace] [key]" >&2; exit 2; }

case "${action}" in
  available)
    backend
    ;;
  get|set|delete)
    [ -n "${namespace}" ] && [ -n "${key}" ] || { echo "namespace and key are required" >&2; exit 2; }
    selected_backend="$(backend)" || exit 1
    case "${selected_backend}" in
      "macOS Keychain")
        case "${action}" in
          get) security find-generic-password -s "${service}" -a "${account}" -w 2>/dev/null ;;
          set)
            IFS= read -r secret
            [ -n "${secret}" ] || exit 2
            security add-generic-password -U -s "${service}" -a "${account}" -w "${secret}" >/dev/null
            ;;
          delete) security delete-generic-password -s "${service}" -a "${account}" >/dev/null 2>&1 || true ;;
        esac
        ;;
      "Secret Service")
        case "${action}" in
          get) secret-tool lookup service "${service}" account "${account}" ;;
          set)
            IFS= read -r secret
            [ -n "${secret}" ] || exit 2
            printf '%s' "${secret}" | secret-tool store --label "Adaetum setup resume (${namespace})" service "${service}" account "${account}"
            ;;
          delete) secret-tool clear service "${service}" account "${account}" >/dev/null 2>&1 || true ;;
        esac
        ;;
      "Windows DPAPI")
        powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass \
          -File "$(windows_helper)" "${action}" "${namespace}" "${key}"
        ;;
    esac
    ;;
  *)
    echo "unsupported action: ${action}" >&2
    exit 2
    ;;
esac
