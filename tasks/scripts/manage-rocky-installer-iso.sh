#!/usr/bin/env bash
set -euo pipefail

# Own the supported Rocky installer-media inputs. Minimal is the default; DVD
# is available for operators who need a complete offline package repository.
# Rocky's Boot ISO (online installer) is intentionally unsupported because it
# lacks the local package repository consumed by Adaetum's kickstart.

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

sha256_file() {
  local path="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${path}" | awk '{print $1}'
  else
    sha256sum "${path}" | awk '{print $1}'
  fi
}

list() {
  local iso=""
  local base=""
  local version=""
  local arch=""
  local image_type=""
  local found=0
  while IFS= read -r iso; do
    [ -n "${iso}" ] || continue
    base="$(basename "${iso}")"
    if [[ ! "${base}" =~ ^Rocky-10(\.[0-9]+)?-(x86_64|aarch64)-(minimal|dvd1|dvd)\.iso$ ]]; then
      continue
    fi
    version="${BASH_REMATCH[1]:-.0}"
    version="10${version}"
    arch="${BASH_REMATCH[2]}"
    image_type="${BASH_REMATCH[3]}"
    [ "${image_type}" = dvd1 ] && image_type=dvd
    printf '%s\tRocky %s\t%s\t%s\t%s\n' "${iso}" "${version}" "${arch}" "${image_type}" "$(format_size "${iso}")"
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
  if [[ ! "${base}" =~ ^Rocky-10(\.[0-9]+)?-(x86_64|aarch64)-(minimal|dvd1|dvd)\.iso$ ]]; then
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
  local image_type="${2:-minimal}"
  local requested_release="${3:-${release}}"
  local file_type="${image_type}"
  [ "${image_type}" = dvd ] && file_type=dvd1
  local base="Rocky-${requested_release}-${arch}-${file_type}.iso"
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
  case "${image_type}" in
    minimal|dvd) ;;
    *) echo "Unsupported Rocky installer image type: ${image_type}" >&2; exit 1 ;;
  esac
  command -v curl >/dev/null 2>&1 || {
    echo "curl is required to download installer media." >&2
    exit 1
  }
  local display_type="Minimal"
  [ "${image_type}" = dvd ] && display_type="DVD"
  trap 'rm -f "${checksum_file}"' EXIT
  curl --fail --location --silent --show-error --output "${checksum_file}" "${checksum_url}"
  # Rocky publishes BSD-style checksum files:
  # SHA256 (Rocky-10.2-x86_64-minimal.iso) = <digest>
  expected="$(awk '$1 == "SHA256" && $3 == "=" { print tolower($4); exit }' "${checksum_file}" | tr -d '\r\n')"
  if [[ ! "${expected}" =~ ^[0-9a-f]{64}$ ]]; then
    echo "Official checksum file did not contain a SHA-256 digest." >&2
    exit 1
  fi

  if [ -f "${output}" ]; then
    actual="$(sha256_file "${output}")"
    if [ "${actual}" != "${expected}" ]; then
      echo "Existing installer ISO does not match Rocky's published SHA-256: ${output}" >&2
      exit 1
    fi
    rm -f "${checksum_file}"
    trap - EXIT
    echo "Reusing verified installer ISO: ${output}"
    return 0
  fi

  if [ -f "${partial}" ]; then
    actual="$(sha256_file "${partial}")"
    if [ "${actual}" = "${expected}" ]; then
      mv "${partial}" "${output}"
      rm -f "${checksum_file}"
      trap - EXIT
      echo "Reusing completed download and verified installer ISO: ${output}"
      return 0
    fi
    echo "Resuming the existing Rocky Linux ${requested_release} ${display_type} download (${arch})..."
  else
    echo "Downloading Rocky Linux ${requested_release} ${display_type} (${arch})..."
  fi

  curl --fail --location --continue-at - --progress-bar --output "${partial}" "${url}"
  actual="$(sha256_file "${partial}")"
  if [ "${actual}" != "${expected}" ]; then
    echo "Downloaded ISO checksum did not match Rocky's published SHA-256. The partial file was retained so a rerun can resume it: ${partial}" >&2
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
  download) download "${2:?usage: manage-rocky-installer-iso.sh download <x86_64|aarch64> [minimal|dvd] [release]}" "${3:-minimal}" "${4:-${release}}" ;;
  *) echo "usage: manage-rocky-installer-iso.sh [list|adopt <path>|download <x86_64|aarch64> [minimal|dvd] [release]]" >&2; exit 2 ;;
esac
