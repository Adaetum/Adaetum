#!/usr/bin/env bash
set -euo pipefail

# Rebuild the separately distributed GPL utilities from the included sources.
# Only macOS system libraries are linked; the operator does not need Homebrew.
export MACOSX_DEPLOYMENT_TARGET=11.0
here="$(cd "$(dirname "$0")" && pwd)"
if [[ "$(uname -s)" != Darwin ]]; then
  echo "This recipe builds the tools on macOS." >&2
  exit 1
fi
case "$(uname -m)" in
  arm64) target=darwin-arm64 ;;
  x86_64) target=darwin-amd64 ;;
  *) echo "Unsupported macOS architecture." >&2; exit 1 ;;
esac
scratch="$(mktemp -d "${TMPDIR:-/tmp}/adaetum-iso-tools.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
mkdir -p "$scratch/xorriso" "$scratch/isomd5sum"
tar -xzf "$here/source/xorriso-1.5.8.pl02.tar.gz" -C "$scratch/xorriso" --strip-components=1
(
  cd "$scratch/xorriso"
  ./configure --disable-shared --disable-libreadline --disable-libedit --disable-libacl --disable-xattr --disable-zlib
  make -j4
  install -m 755 xorriso/xorriso "$here/$target/xorriso"
)
cp -R "$here/source/isomd5sum/." "$scratch/isomd5sum/"
(
  cd "$scratch/isomd5sum"
  clang -O2 -std=gnu11 -Icompat -c md5.c libimplantisomd5.c libcheckisomd5.c utilities.c
  clang -O2 -std=gnu11 -Icompat -o iso-checksum adaetum.c md5.o libimplantisomd5.o libcheckisomd5.o utilities.o
  install -m 755 iso-checksum "$here/$target/iso-checksum"
)
