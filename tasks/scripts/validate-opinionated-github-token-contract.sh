#!/usr/bin/env bash
set -euo pipefail

env_file="${1:-.env}"

normalize_value() {
  local value="${1:-}"
  printf '%s' "${value}" | tr -d '\r\n' | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//; s/^"(.*)"$/\1/; s/^\x27(.*)\x27$/\1/'
}

read_env_value() {
  local file="${1:-}"
  local key="${2:-}"
  local value=""
  if [ -f "${file}" ]; then
    value="$(awk -F= -v k="${key}" '$1==k {print substr($0, index($0, "=")+1); exit}' "${file}")"
  fi
  normalize_value "${value}"
}

github_token_looks_git_capable() {
  case "${1:-}" in
    github_pat_*|ghp_*|gho_*|ghu_*|ghs_*) return 0 ;;
    *) return 1 ;;
  esac
}

if [ ! -f "${env_file}" ]; then
  echo "Missing env file: ${env_file}" >&2
  exit 1
fi

sync_token="$(read_env_value "${env_file}" GITHUB_SYNC_TOKEN)"
argocd_token="$(read_env_value "${env_file}" ARGOCD_GITHUB_TOKEN)"
seed_token="$(read_env_value "${env_file}" GITEA_SEED_SOURCE_TOKEN)"
mirror_enabled="$(read_env_value "${env_file}" GITEA_PUSH_MIRROR_ENABLED)"
mirror_repo_url="$(read_env_value "${env_file}" GITEA_PUSH_MIRROR_REPO_URL)"
mirror_username="$(read_env_value "${env_file}" GITEA_PUSH_MIRROR_USERNAME)"
mirror_token="$(read_env_value "${env_file}" GITEA_PUSH_MIRROR_TOKEN)"

if ! github_token_looks_git_capable "${sync_token}"; then
  echo "Opinionated GitHub token contract failed: GITHUB_SYNC_TOKEN is missing or not git-capable." >&2
  exit 1
fi

if [ -z "${argocd_token}" ]; then
  echo "Opinionated GitHub token contract failed: ARGOCD_GITHUB_TOKEN is blank." >&2
  exit 1
fi

if [ -z "${seed_token}" ]; then
  echo "Opinionated GitHub token contract failed: GITEA_SEED_SOURCE_TOKEN is blank." >&2
  exit 1
fi

if [ "${argocd_token}" != "${sync_token}" ]; then
  echo "Opinionated GitHub token contract failed: ARGOCD_GITHUB_TOKEN does not match GITHUB_SYNC_TOKEN." >&2
  exit 1
fi

if [ "${seed_token}" != "${sync_token}" ]; then
  echo "Opinionated GitHub token contract failed: GITEA_SEED_SOURCE_TOKEN does not match GITHUB_SYNC_TOKEN." >&2
  exit 1
fi

case "$(printf '%s' "${mirror_enabled}" | tr '[:upper:]' '[:lower:]')" in
  1|true|yes) ;;
  *)
    echo "Opinionated GitHub token contract failed: GITEA_PUSH_MIRROR_ENABLED must be 1." >&2
    exit 1
    ;;
esac

if [ -z "${mirror_repo_url}" ]; then
  echo "Opinionated GitHub token contract failed: GITEA_PUSH_MIRROR_REPO_URL is blank." >&2
  exit 1
fi

if [ -z "${mirror_username}" ]; then
  echo "Opinionated GitHub token contract failed: GITEA_PUSH_MIRROR_USERNAME is blank." >&2
  exit 1
fi

if [ "${mirror_token}" != "${sync_token}" ]; then
  echo "Opinionated GitHub token contract failed: GITEA_PUSH_MIRROR_TOKEN does not match GITHUB_SYNC_TOKEN." >&2
  exit 1
fi

echo "Opinionated GitHub token contract passed: derived repo and push-mirror tokens match GITHUB_SYNC_TOKEN."
