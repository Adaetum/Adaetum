#!/usr/bin/env bash
# Compile the account sign-in service (adaetum/auth, Better Auth on Bun) for development
# installer media, so a host can sign people in with the accounts copied from the cluster.
#
# Usage: build-dev-auth.sh <amd64|arm64> <output-file>
#
# Only `task init:dev` uses this. The release pipeline does not publish the service yet, so
# production media carries no auth service and a host there accepts only its access token.
# Needs Bun (https://bun.sh); the target runtime is fetched by Bun itself.
set -euo pipefail

arch="${1:?architecture (amd64 or arm64) is required}"
output="${2:?output file is required}"
case "${arch}" in
  amd64) target="bun-linux-x64" ;;
  arm64) target="bun-linux-arm64" ;;
  *) echo "unsupported architecture: ${arch}" >&2; exit 1 ;;
esac
command -v bun >/dev/null 2>&1 || { echo "bun is required to build the auth service for development media (https://bun.sh)" >&2; exit 1; }

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
case "${output}" in /*) ;; *) output="$(pwd)/${output}" ;; esac

echo "[dev-auth] building the account sign-in service for ${target}"
mkdir -p "$(dirname "${output}")"
(cd "${root}/adaetum/auth" && bun install --frozen-lockfile --production >/dev/null \
  && bun build --compile --target="${target}" src/server.ts --outfile "${output}" >/dev/null)
chmod 0755 "${output}"
