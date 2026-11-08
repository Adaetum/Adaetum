#!/usr/bin/env bash
set -euo pipefail

# Only task init invokes this installer. Discovery and dry-run commands must
# remain read-only. Tool installs happen before the setup window is built.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="${HOME}/.local/bin:/usr/local/go/bin:${PATH}"
required_go="$(awk '$1 == "go" { print $2; exit }' "${root}/installer/go.mod")"
if [ -z "${required_go}" ]; then echo "Could not determine the required Go version." >&2; exit 1; fi
if [ -x "${HOME}/sdk/go${required_go}/bin/go" ]; then export PATH="${HOME}/sdk/go${required_go}/bin:${PATH}"; fi

install_package() {
  local package="$1" debian_package="${2:-$1}" fedora_package="${3:-$1}"
  echo "Installing ${package} for Adaetum setup..."
  if command -v brew >/dev/null 2>&1; then
    brew install "${package}"
  elif command -v apt-get >/dev/null 2>&1; then
    if [ "$(id -u)" -eq 0 ]; then apt-get update && apt-get install -y "${debian_package}"; else sudo apt-get update && sudo apt-get install -y "${debian_package}"; fi
  elif command -v dnf >/dev/null 2>&1; then
    if [ "$(id -u)" -eq 0 ]; then dnf install -y "${fedora_package}"; else sudo dnf install -y "${fedora_package}"; fi
  else
    echo "Cannot install ${package}: no supported package manager was found." >&2
    return 1
  fi
}

command -v git >/dev/null 2>&1 || install_package git
command -v curl >/dev/null 2>&1 || install_package curl
command -v python3 >/dev/null 2>&1 || install_package python python3 python3
command -v go >/dev/null 2>&1 || install_package go golang-go golang
if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
  install_package node nodejs nodejs
  if ! command -v npm >/dev/null 2>&1 && command -v apt-get >/dev/null 2>&1; then install_package npm npm npm; fi
fi
for tool in git curl python3 go node npm; do
  if ! command -v "${tool}" >/dev/null 2>&1; then echo "${tool} is still unavailable after installation; check the package manager and PATH." >&2; exit 1; fi
done

# Go before 1.21 cannot select the module's required toolchain itself. Install
# the exact version named by go.mod beside the user's other Go SDKs instead of
# replacing their existing system installation.
if [ -z "$(go env GOTOOLCHAIN 2>/dev/null || true)" ]; then
  tool_dir="${root}/.adaetum/setup-bin"
  mkdir -p "${tool_dir}"
  echo "Installing Go ${required_go} for Adaetum setup..."
  GOBIN="${tool_dir}" go install "golang.org/dl/go${required_go}@latest"
  "${tool_dir}/go${required_go}" download
fi

# Keep Python packages in an Adaetum-owned venv, never the system interpreter.
# The launcher and setup tasks add it to PATH only for the setup process.
venv="${root}/.adaetum/setup-python"
if ! python3 -c 'import yaml' >/dev/null 2>&1; then
  if [ ! -x "${venv}/bin/python3" ]; then
    mkdir -p "${root}/.adaetum"
    if ! python3 -m venv "${venv}"; then
      if command -v apt-get >/dev/null 2>&1; then install_package python3-venv python3-venv python3; python3 -m venv "${venv}"; else exit 1; fi
    fi
  fi
  if ! "${venv}/bin/python3" -c 'import yaml' >/dev/null 2>&1; then
    echo "Installing PyYAML in Adaetum's setup environment..."
    "${venv}/bin/python3" -m pip install PyYAML
  fi
fi
