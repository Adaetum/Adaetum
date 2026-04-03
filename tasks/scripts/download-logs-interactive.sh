#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

normalize_value() {
  local value="${1:-}"
  printf '%s' "${value}" | tr -d '\r\n' | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//; s/^\"(.*)\"$/\1/; s/^\x27(.*)\x27$/\1/'
}

require_cmd() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "Missing required command: ${cmd}" >&2
    exit 1
  fi
}

choose_from_list() {
  local label="$1"
  shift
  local items=("$@")
  local idx=""
  if [ "${#items[@]}" -eq 0 ]; then
    echo "No options available for ${label}." >&2
    return 1
  fi

  echo "" >&2
  echo "${label}:" >&2
  local i=1
  for item in "${items[@]}"; do
    printf '  %d) %s\n' "${i}" "${item}" >&2
    i=$((i + 1))
  done
  echo "" >&2

  while true; do
    read -r -p "Select ${label} [1-${#items[@]}]: " idx
    if [[ "${idx}" =~ ^[0-9]+$ ]] && [ "${idx}" -ge 1 ] && [ "${idx}" -le "${#items[@]}" ]; then
      printf '%s' "${items[$((idx - 1))]}"
      return 0
    fi
    echo "Invalid selection: ${idx}" >&2
  done
}

load_env() {
  local ks_env="${KS_ENV:-.env}"
  if [ ! -f "${ks_env}" ]; then
    echo "Missing env file: ${ks_env}" >&2
    echo "Set KS_ENV or create .env first." >&2
    exit 1
  fi

  set -a
  # shellcheck disable=SC1090
  . "${ks_env}"
  set +a

  R2_ENDPOINT="$(normalize_value "${R2_ENDPOINT:-}")"
  R2_BUCKET="$(normalize_value "${R2_BUCKET:-}")"
  R2_ACCESS_KEY_ID="$(normalize_value "${R2_ACCESS_KEY_ID:-}")"
  R2_SECRET_ACCESS_KEY="$(normalize_value "${R2_SECRET_ACCESS_KEY:-}")"

  if [ -z "${R2_ENDPOINT}" ] || [ -z "${R2_BUCKET}" ] || [ -z "${R2_ACCESS_KEY_ID}" ] || [ -z "${R2_SECRET_ACCESS_KEY}" ]; then
    echo "Missing required R2 settings in ${ks_env}." >&2
    echo "Required: R2_ENDPOINT, R2_BUCKET, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY" >&2
    exit 1
  fi

  export AWS_EC2_METADATA_DISABLED=true
  export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-auto}"
  export AWS_ACCESS_KEY_ID="${R2_ACCESS_KEY_ID}"
  export AWS_SECRET_ACCESS_KEY="${R2_SECRET_ACCESS_KEY}"
}

echo "R2 Logs Wizard"
echo "Repo: ${repo_root}"

require_cmd aws
require_cmd python3
load_env

objects_json="$(aws s3api list-objects-v2 \
  --bucket "${R2_BUCKET}" \
  --prefix "logs/" \
  --endpoint-url "${R2_ENDPOINT}" \
  --output json)"

nodes_csv="$(printf '%s' "${objects_json}" | python3 -c '
import json, sys
obj = json.load(sys.stdin)
nodes = set()
for item in (obj.get("Contents") or []):
    key = (item.get("Key") or "").strip()
    parts = key.split("/")
    if len(parts) >= 3 and parts[0] == "logs":
        nodes.add(parts[1])
for node in sorted(nodes):
    print(node)
')"

if [ -z "${nodes_csv}" ]; then
  echo "No logs found under s3://${R2_BUCKET}/logs/." >&2
  exit 1
fi

nodes=()
while IFS= read -r line; do
  [ -n "${line}" ] || continue
  nodes+=("${line}")
done <<EOF
${nodes_csv}
EOF
selected_node="$(choose_from_list "Node" "${nodes[@]}")"
echo "Selected node: ${selected_node}"

files_tsv="$(printf '%s' "${objects_json}" | python3 -c '
import json, sys
obj = json.load(sys.stdin)
node = sys.argv[1]
rows = []
for item in (obj.get("Contents") or []):
    key = (item.get("Key") or "").strip()
    parts = key.split("/")
    if len(parts) < 3 or parts[0] != "logs":
        continue
    host = parts[1]
    if host != node:
        continue
    name = "/".join(parts[2:]).strip()
    if not name:
        continue
    last_mod = (item.get("LastModified") or "").strip()
    size = int(item.get("Size") or 0)
    rows.append((last_mod, name, size, key))
rows.sort(reverse=True)
for last_mod, name, size, key in rows:
    print(f"{name}\t{last_mod}\t{size}\t{key}")
' "${selected_node}")"

if [ -z "${files_tsv}" ]; then
  echo "No logs found for node ${selected_node}." >&2
  exit 1
fi

download_dir="dist/ks-logs/${selected_node}"
mkdir -p "${download_dir}"

summary="$(printf '%s' "${files_tsv}" | python3 -c '
import sys
count = 0
size_total = 0
for line in sys.stdin:
    line = line.rstrip("\n")
    if not line:
        continue
    parts = line.split("\t")
    if len(parts) < 4:
        continue
    count += 1
    try:
        size_total += int(parts[2])
    except Exception:
        pass
print(f"{count}\t{size_total}")
')"
download_count="$(printf '%s' "${summary}" | cut -f1)"
download_bytes="$(printf '%s' "${summary}" | cut -f2)"

if [ "${download_count}" -eq 0 ]; then
  echo "No downloadable logs found for node ${selected_node}." >&2
  exit 1
fi

echo "Downloading ${download_count} log file(s) for ${selected_node}..."
aws s3 cp "s3://${R2_BUCKET}/logs/${selected_node}/" "${download_dir}/" \
  --recursive \
  --endpoint-url "${R2_ENDPOINT}" >/dev/null

echo "Logs wizard complete."
echo "Saved ${download_count} file(s), ${download_bytes} bytes total."
echo "Destination: ${download_dir}"
