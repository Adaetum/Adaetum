#!/usr/bin/env bash
set -euo pipefail

# Build (if needed) and open the Adaetum setup window from this checkout. The window
# is a Wails app compiled from installer/app, so nothing is downloaded or installed
# as a release: the first run builds it, later runs rebuild only when it is out of
# date. Needs Go and Node; installs the Wails CLI with `go install` if it is missing.

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
app="${here}/app"
wails_version="v2.17.1"
dry_run=0

while (($#)); do
  case "$1" in
    --dry-run) dry_run=1 ;;
    *) echo "Unknown installer window option: $1" >&2; exit 2 ;;
  esac
  shift
done

if [ "${dry_run}" = 1 ]; then
  export ADAETUM_SETUP_DRY_RUN=1
else
  export ADAETUM_SETUP_DRY_RUN=0
fi

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "$1 is required to build the setup window. $2" >&2
    echo "To run setup without a window instead: task initialize -- --apply" >&2
    exit 1
  }
}

need go "Install it from https://go.dev/dl/."
need npm "Install Node.js from https://nodejs.org/."

export PATH="${PATH}:$(go env GOPATH)/bin"
if ! command -v wails >/dev/null 2>&1; then
  echo "Installing the Wails build tool (${wails_version})..."
  go install "github.com/wailsapp/wails/v2/cmd/wails@${wails_version}"
fi

case "$(uname -s)" in
  Darwin) binary="${app}/build/bin/adaetum-setup.app" ;;
  MINGW*|MSYS*|CYGWIN*) binary="${app}/build/bin/adaetum-setup.exe" ;;
  *) binary="${app}/build/bin/adaetum-setup" ;;
esac

# Rebuild when anything the window is made from is newer than the build.
needs_build=0
if [ ! -e "${binary}" ]; then
  needs_build=1
elif [ -n "$(find "${here}" -path "${app}/build" -prune -o -path '*/node_modules' -prune -o -path "${app}/frontend/wailsjs" -prune -o -path "${app}/frontend/dist" -prune -o \( -name '*.go' -o -name '*.ts' -o -name '*.tsx' -o -name '*.css' -o -name 'go.mod' -o -name 'package.json' \) -newer "${binary}" -print -quit)" ]; then
  needs_build=1
fi
if [ "${needs_build}" = 1 ]; then
  echo "Building the setup window..."
  if ! build_log="$(cd "${app}" && wails build 2>&1)"; then
    echo "${build_log}" >&2
    echo "The setup window failed to build." >&2
    exit 1
  fi
fi

# The checkout this window operates on is the one it was launched from.
export ADAETUM_ROOT="$(cd "${here}/.." && pwd)"
case "$(uname -s)" in
  Darwin) exec open -W -n "${binary}" --env ADAETUM_ROOT="${ADAETUM_ROOT}" --env ADAETUM_CLEAN="${ADAETUM_CLEAN:-0}" --env ADAETUM_SETUP_DRY_RUN="${ADAETUM_SETUP_DRY_RUN}" ;;
  *) exec "${binary}" ;;
esac
