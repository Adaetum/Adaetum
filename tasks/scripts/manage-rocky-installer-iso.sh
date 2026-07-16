#!/usr/bin/env bash
set -euo pipefail

# Own the one supported installer-media input. This script only accepts Rocky
# 10 Minimal ISOs because the bootstrap and validation paths are built around
# that media; it verifies downloaded bytes before placing them in repo root.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release="10.2"

format_size() {
  local path="$1"
  if stat -f '%z' "${path}" >/dev/null 2>&1; then
    stat -f '%z' "${path}" | awk '{printf "%.2f GiB", $1 / 1073741824}'
  else
    stat -c '%s' "${path}" | awk '{printf "%.2f GiB", $1 / 1073741824}'
  fi
}

list() {
  local iso=""
  local base=""
  local version=""
  local arch=""
  local found=0
  while IFS= read -r iso; do
    [ -n "${iso}" ] || continue
    base="$(basename "${iso}")"
    if [[ ! "${base}" =~ ^Rocky-10(\.[0-9]+)?-(x86_64|aarch64)-minimal\.iso$ ]]; then
      continue
    fi
    version="${BASH_REMATCH[1]:-.0}"
    version="10${version}"
    arch="${BASH_REMATCH[2]}"
    printf '%s\tRocky %s\t%s\t%s\n' "${iso}" "${version}" "${arch}" "$(format_size "${iso}")"
    found=1
  done < <(
    {
      find "${repo_root}" -maxdepth 1 -type f -name '*.iso' 2>/dev/null
      [ -d "${HOME}/Downloads" ] && find "${HOME}/Downloads" -maxdepth 1 -type f -name '*.iso' 2>/dev/null
      [ -d "${HOME}/Desktop" ] && find "${HOME}/Desktop" -maxdepth 1 -type f -name '*.iso' 2>/dev/null
    } | awk '!seen[$0]++' | sort
  )
  [ "${found}" = "1" ]
}

adopt() {
  local source="$1"
  local base="$(basename "${source}")"
  local destination="${repo_root}/${base}"
  if [[ ! "${base}" =~ ^Rocky-10(\.[0-9]+)?-(x86_64|aarch64)-minimal\.iso$ ]]; then
    echo "Unsupported Rocky installer ISO: ${source}" >&2
    exit 1
  fi
  if [ ! -f "${source}" ]; then
    echo "Installer ISO does not exist: ${source}" >&2
    exit 1
  fi
  if [ "$(cd "$(dirname "${source}")" && pwd -P)/${base}" = "${destination}" ]; then
    echo "Installer ISO is already in the repository root: ${destination}"
    return 0
  fi
  if [ -f "${destination}" ]; then
    echo "A same-named installer ISO already exists in the repository root: ${destination}" >&2
    exit 1
  fi
  cp -p "${source}" "${destination}"
  echo "Copied installer ISO to: ${destination}"
}

download() {
  local arch="$1"
  local base="Rocky-${release}-${arch}-minimal.iso"
  local url="https://download.rockylinux.org/pub/rocky/10/isos/${arch}/${base}"
  local checksum_url="${url}.CHECKSUM"
  local output="${repo_root}/${base}"
  local partial="${output}.partial"
  local checksum_file="${partial}.CHECKSUM"
  local expected=""
  local actual=""

  case "${arch}" in
    x86_64|aarch64) ;;
    *) echo "Unsupported Rocky installer architecture: ${arch}" >&2; exit 1 ;;
  esac
  command -v curl >/dev/null 2>&1 || {
    echo "curl is required to download installer media." >&2
    exit 1
  }
  if [ -f "${output}" ]; then
    echo "Installer ISO already exists: ${output}"
    return 0
  fi

  trap 'rm -f "${partial}" "${checksum_file}"' EXIT
  echo "Downloading Rocky Linux ${release} Minimal (${arch})..."
  curl --fail --location --progress-bar --output "${partial}" "${url}"
  curl --fail --location --silent --show-error --output "${checksum_file}" "${checksum_url}"
  expected="$(awk '{print $1}' "${checksum_file}" | tr -d '\r\n')"
  if [ -z "${expected}" ]; then
    echo "Official checksum file did not contain a SHA-256 digest." >&2
    exit 1
  fi
  if command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "${partial}" | awk '{print $1}')"
  else
    actual="$(sha256sum "${partial}" | awk '{print $1}')"
  fi
  if [ "${actual}" != "${expected}" ]; then
    echo "Downloaded ISO checksum did not match Rocky's published SHA-256." >&2
    exit 1
  fi
  mv "${partial}" "${output}"
  rm -f "${checksum_file}"
  trap - EXIT
  echo "Verified installer ISO: ${output}"
}

case "${1:-list}" in
  list) list ;;
  adopt) adopt "${2:?usage: manage-rocky-installer-iso.sh adopt <path>}" ;;
  download) download "${2:?usage: manage-rocky-installer-iso.sh download <x86_64|aarch64>}" ;;
  *) echo "usage: manage-rocky-installer-iso.sh [list|adopt <path>|download <x86_64|aarch64>]" >&2; exit 2 ;;
esac
