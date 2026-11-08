#!/usr/bin/env bash
set -euo pipefail

# Windows xorriso uses the Git for Windows MSYS runtime already needed by setup.
# Fetch the pinned MSYS2 binaries and build the checksum utility from our source.
here="$(cd "$(dirname "$0")" && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/adaetum-win-iso.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
fetch() {
  local name="$1" checksum="$2"
  curl -fLsS "https://mirror.msys2.org/msys/x86_64/${name}.pkg.tar.zst" -o "${scratch}/${name}.tar.zst"
  printf '%s  %s\n' "$checksum" "${scratch}/${name}.tar.zst" | sha256sum -c -
  tar --zstd -xf "${scratch}/${name}.tar.zst" -C "$scratch"
}
fetch xorriso-1.5.8.2-1-x86_64 d97aed8ea0be8a247af5487aa3fc0790fcf488da9fc5c4964e7d73e5542b1036
fetch libbz2-1.0.8-4-x86_64 89fb0e1478b22b80effda55ab4ae7549388d01245d3841a096d8a2d63236c2a1
mkdir -p "$here/windows-amd64"
install -m 755 "$scratch/usr/bin/xorriso.exe" "$scratch/usr/bin/msys-bz2-1.dll" "$here/windows-amd64/"
(
  cd "$here/source/isomd5sum"
  x86_64-w64-mingw32-gcc -O2 -std=gnu11 -Icompat -o "$here/windows-amd64/iso-checksum.exe" adaetum.c md5.c libimplantisomd5.c libcheckisomd5.c utilities.c
)
