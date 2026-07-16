#!/usr/bin/env bash
set -euo pipefail

# Inspect the checkout that first-run setup will target. Git exposes a remote
# URL but not GitHub's fork metadata; the interactive wizard uses this to guide
# an upstream checkout through creating a fork before provider mutations begin.

adaetum_origin_url() {
  if ! command -v git >/dev/null 2>&1; then
    echo "git is required to verify that task init targets your fork." >&2
    return 1
  fi

  local origin_url=""
  origin_url="$(git remote get-url origin 2>/dev/null || true)"
  if [ -z "${origin_url}" ]; then
    echo "This checkout has no origin remote." >&2
    return 1
  fi

  printf '%s' "${origin_url}"
}

adaetum_normalize_github_url() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E 's#^git@github\.com:#github.com/#; s#^ssh://git@github\.com/#github.com/#; s#^https?://github\.com/#github.com/#; s#\.git/?$##'
}

adaetum_origin_is_upstream() {
  local normalized_origin=""
  normalized_origin="$(adaetum_normalize_github_url "$1")"
  [ "${normalized_origin}" = "github.com/adaetum/adaetum" ]
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  origin_url="$(adaetum_origin_url)"
  if adaetum_origin_is_upstream "${origin_url}"; then
    printf 'origin is Adaetum upstream: %s\n' "${origin_url}"
    printf 'task init will guide you through creating and selecting a fork.\n'
  else
    printf 'Checkout origin: %s\n' "${origin_url}"
  fi
fi
