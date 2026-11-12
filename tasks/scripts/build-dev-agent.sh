#!/usr/bin/env bash
# Build the host agent from this checkout for development installer media.
#
# Usage: build-dev-agent.sh <amd64|arm64> <output-file>
#
# This is the same build the release workflow does (web page first, because it is
# embedded in the binary), minus signing and publishing. The version is "dev-<commit>"
# so a dev agent is never mistaken for a release. It is only used by `task init:dev`.
set -euo pipefail

arch="${1:?architecture (amd64 or arm64) is required}"
output="${2:?output file is required}"
case "${arch}" in amd64|arm64) ;; *) echo "unsupported architecture: ${arch}" >&2; exit 1 ;; esac

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
version="dev-$(git -C "${root}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
if ! git -C "${root}" diff --quiet HEAD -- adaetum/agent 2>/dev/null; then
  version="${version}+local"
fi

echo "[dev-agent] building the web page"
(cd "${root}/adaetum/agent/web" && npm ci --no-audit --no-fund && npm run build)

echo "[dev-agent] building adaetum-agent ${version} for linux/${arch}"
mkdir -p "$(dirname "${output}")"
(cd "${root}/adaetum/agent" && CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" go build -trimpath \
  -ldflags="-s -w -X main.agentVersion=${version}" -o "${output}" ./cmd/adaetum-agent)
chmod 0755 "${output}"
