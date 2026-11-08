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

# The window and its subprocesses must see tools installed earlier in this run,
# including PyYAML in the isolated setup environment.
export PATH="${HOME}/.local/bin:/usr/local/go/bin:${PATH}"
required_go="$(awk '$1 == "go" { print $2; exit }' "${here}/go.mod")"
if [ -x "${HOME}/sdk/go${required_go}/bin/go" ] || [ -x "${HOME}/sdk/go${required_go}/bin/go.exe" ]; then
  export PATH="${HOME}/sdk/go${required_go}/bin:${PATH}"
fi
for python_bin in "${here}/../.adaetum/setup-python/bin" "${here}/../.adaetum/setup-python/Scripts"; do
  if [ -x "${python_bin}/python3" ] && "${python_bin}/python3" -c 'import yaml' >/dev/null 2>&1; then
    export PATH="${python_bin}:${PATH}"
  elif [ -x "${python_bin}/python.exe" ] && "${python_bin}/python.exe" -c 'import yaml' >/dev/null 2>&1; then
    export PATH="${python_bin}:${PATH}"
  fi
done

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

go_path="$(go env GOPATH)"
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*)
    # go.exe reports a Windows path, but Git Bash needs a POSIX path in PATH.
    if command -v cygpath >/dev/null 2>&1; then go_path="$(cygpath -u "${go_path}")"; fi ;;
esac
export PATH="${PATH}:${go_path}/bin"
if ! command -v wails >/dev/null 2>&1; then
  echo "Installing the Wails build tool (${wails_version})..."
  go install "github.com/wailsapp/wails/v2/cmd/wails@${wails_version}"
fi

case "$(uname -s)" in
  Darwin) binary="${app}/build/bin/adaetum-setup.app"; build_stamp="${binary}/Contents/MacOS/adaetum-setup" ;;
  MINGW*|MSYS*|CYGWIN*) binary="${app}/build/bin/adaetum-setup.exe"; build_stamp="${binary}" ;;
  *) binary="${app}/build/bin/adaetum-setup"; build_stamp="${binary}" ;;
esac

# Rebuild when anything the window is made from is newer than the build.
needs_build=0
clean_generated=0
if [ ! -e "${build_stamp}" ]; then
  needs_build=1
elif [ "${app}/build/appicon.png" -nt "${build_stamp}" ] || [ "${app}/build/windows/icon.ico" -nt "${build_stamp}" ]; then
  # Icon sources live under build/, which is otherwise pruned from this scan.
  # Recompile so Windows does not keep showing the previous executable icon.
  needs_build=1
  clean_generated=1
elif [ "${app}/build/darwin/Info.plist" -nt "${build_stamp}" ] || [ "${app}/build/darwin/Info.dev.plist" -nt "${build_stamp}" ]; then
  # A unique bundle identifier keeps macOS from reusing a cached Wails icon.
  needs_build=1
  clean_generated=1
elif [ -n "$(find "${here}" -path "${app}/build" -prune -o -path '*/node_modules' -prune -o -path "${app}/frontend/wailsjs" -prune -o -path "${app}/frontend/dist" -prune -o \( -name '*.go' -o -name '*.ts' -o -name '*.tsx' -o -name '*.css' -o -name '*.svg' -o -name 'go.mod' -o -name 'package.json' \) -newer "${build_stamp}" -print -quit)" ]; then
  needs_build=1
fi
if [ "${needs_build}" = 1 ]; then
  echo "Building the setup window..."
  if [ "${clean_generated}" = 1 ]; then
    # Wails v2 can leave a stale packaged icon in bin/ or frontend/dist.
    rm -rf "${binary}" "${app}/frontend/dist"
  fi
  if ! build_log="$(cd "${app}" && wails build 2>&1)"; then
    echo "${build_log}" >&2
    echo "The setup window failed to build." >&2
    exit 1
  fi
fi

# The checkout this window operates on is the one it was launched from.
export ADAETUM_ROOT="$(cd "${here}/.." && pwd)"
case "$(uname -s)" in
  Darwin) exec open -W -n "${binary}" --env PATH="${PATH}" --env ADAETUM_ROOT="${ADAETUM_ROOT}" --env ADAETUM_CLEAN="${ADAETUM_CLEAN:-0}" --env ADAETUM_SETUP_DRY_RUN="${ADAETUM_SETUP_DRY_RUN}" ;;
  *) exec "${binary}" ;;
esac
