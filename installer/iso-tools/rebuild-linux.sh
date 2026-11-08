#!/usr/bin/env bash
set -euo pipefail

# Build the shipped GPL utilities from included source on the target Linux
# architecture. The released executables use static musl, not host libraries.
here="$(cd "$(dirname "$0")" && pwd)"
if [[ "$(uname -s)" != Linux ]]; then
  echo "This recipe builds the tools on Linux with a musl C toolchain." >&2
  exit 1
fi
case "$(uname -m)" in
  aarch64|arm64) target=linux-arm64 ;;
  x86_64|amd64) target=linux-amd64 ;;
  *) echo "Unsupported Linux architecture." >&2; exit 1 ;;
esac
scratch="$(mktemp -d "${TMPDIR:-/tmp}/adaetum-iso-tools.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
mkdir -p "$scratch/xorriso"
tar -xzf "$here/source/xorriso-1.5.8.pl02.tar.gz" -C "$scratch/xorriso" --strip-components=1
(
  cd "$scratch/xorriso"
  ./configure --disable-shared --disable-libreadline --disable-libedit --disable-libacl --disable-xattr --disable-zlib
  make -j4 LDFLAGS=-all-static
  install -m 755 xorriso/xorriso "$here/$target/xorriso"
)
(
  cd "$here/source/isomd5sum"
  cc -static -O2 -std=gnu11 -Icompat -o "$here/$target/iso-checksum" adaetum.c md5.c libimplantisomd5.c libcheckisomd5.c utilities.c
)
strip "$here/$target/xorriso" "$here/$target/iso-checksum"
