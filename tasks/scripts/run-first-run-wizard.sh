#!/usr/bin/env bash
set -euo pipefail

# Compatibility entrypoint. The actual first-run UI lives in the same setup
# process as provider capture and bootstrap, so dry runs and real runs share
# one interaction flow.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
export ADAETUM_FIRST_RUN=1
exec bash ./tasks/scripts/run-opinionated-setup.sh
