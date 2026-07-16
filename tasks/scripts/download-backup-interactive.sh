#!/usr/bin/env bash
set -euo pipefail

# Interactively fetch and decrypt a recovery backup. This is intentionally a
# recovery utility: it reads local credentials but never changes cluster state.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
# shellcheck source=tasks/scripts/gum-ui.sh
. "${repo_root}/tasks/scripts/gum-ui.sh"

if adaetum_gum_enabled; then
  adaetum_gum_heading "Backup recovery"
  gum style --foreground 245 "Fork: ${repo_root}"
fi

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

  if adaetum_gum_enabled; then
    adaetum_gum_choose "${label}" "${items[@]}"
    return 0
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
  BOOTSTRAP_BACKUP_PASSPHRASE="$(normalize_value "${BOOTSTRAP_BACKUP_PASSPHRASE:-}")"
  BOOTSTRAP_BACKUP_PASSPHRASE_B64="$(normalize_value "${BOOTSTRAP_BACKUP_PASSPHRASE_B64:-}")"

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

resolve_passphrase() {
  local pass="${BOOTSTRAP_BACKUP_PASSPHRASE:-}"
  if [ -z "${pass}" ] && [ -n "${BOOTSTRAP_BACKUP_PASSPHRASE_B64:-}" ]; then
    pass="$(python3 -c 'import base64,sys; sys.stdout.write(base64.b64decode(sys.argv[1].strip().encode()).decode("utf-8"))' "${BOOTSTRAP_BACKUP_PASSPHRASE_B64}" 2>/dev/null || true)"
  fi
  if [ -z "${pass}" ]; then
    if adaetum_gum_enabled; then
      pass="$(adaetum_gum_input "Backup passphrase" "" 1)" || exit 1
    else
      read -r -s -p "Backup passphrase (input hidden): " pass
      echo ""
    fi
  fi
  if [ -z "${pass}" ]; then
    echo "Backup passphrase is required." >&2
    exit 1
  fi
  printf '%s' "${pass}"
}

passphrase_fingerprint() {
  python3 - <<'PY' "${1:-}"
import hashlib
import sys
value = sys.argv[1] if len(sys.argv) > 1 else ""
print(hashlib.sha256(value.encode("utf-8")).hexdigest(), end="")
PY
}

find_7z_bin() {
  if command -v 7z >/dev/null 2>&1; then
    command -v 7z
    return 0
  fi
  if command -v 7za >/dev/null 2>&1; then
    command -v 7za
    return 0
  fi
  if command -v 7zz >/dev/null 2>&1; then
    command -v 7zz
    return 0
  fi
  # Common native Windows install path (when running from Git Bash).
  if [ -x "/c/Program Files/7-Zip/7z.exe" ]; then
    printf '%s\n' "/c/Program Files/7-Zip/7z.exe"
    return 0
  fi
  return 1
}

ensure_7z_bin() {
  local seven=""
  seven="$(find_7z_bin || true)"
  if [ -n "${seven}" ]; then
    printf '%s\n' "${seven}"
    return 0
  fi

  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
      if command -v powershell.exe >/dev/null 2>&1; then
        local out=""
        out="$(powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "./tasks/scripts/install-7zip.ps1" 2>/dev/null | tr -d '\r' | tail -n 1 || true)"
        if [ -n "${out}" ]; then
          # Convert "C:\..." to "/c/..." for Git Bash execution.
          if command -v python3 >/dev/null 2>&1; then
            local converted=""
            converted="$(python3 -c 'import os,sys; p=sys.argv[1].strip(); p=p.replace("\\\\","/"); print("/"+p[0].lower()+p[2:] if len(p)>2 and p[1]==":" else p)' "${out}" 2>/dev/null || true)"
            if [ -n "${converted}" ] && [ -x "${converted}" ]; then
              printf '%s\n' "${converted}"
              return 0
            fi
          fi
          if [ -x "${out}" ]; then
            printf '%s\n' "${out}"
            return 0
          fi
        fi
      fi
      ;;
  esac

  seven="$(find_7z_bin || true)"
  if [ -n "${seven}" ]; then
    printf '%s\n' "${seven}"
    return 0
  fi
  return 1
}

echo "R2 Backup Wizard"
echo "Repo: ${repo_root}"

require_cmd aws
require_cmd python3
load_env

objects_json="$(aws s3api list-objects-v2 \
  --bucket "${R2_BUCKET}" \
  --prefix "backups/" \
  --endpoint-url "${R2_ENDPOINT}" \
  --output json)"

nodes_csv="$(printf '%s' "${objects_json}" | python3 -c '
import json, sys
obj = json.load(sys.stdin)
nodes = set()
for item in (obj.get("Contents") or []):
    key = (item.get("Key") or "").strip()
    parts = key.split("/")
    if len(parts) >= 3 and parts[0] == "backups":
        name = parts[-1]
        if name.endswith(".7z") or name.endswith(".enc"):
            nodes.add(parts[1])
for node in sorted(nodes):
    print(node)
')"

if [ -z "${nodes_csv}" ]; then
  echo "No emergency-kit backups (.7z or .enc) found under s3://${R2_BUCKET}/backups/." >&2
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

files_csv="$(printf '%s' "${objects_json}" | python3 -c '
import json, sys
obj = json.load(sys.stdin)
node = sys.argv[1]
prefer = (sys.argv[2] or "openssl").strip().lower()
rows = []
for item in (obj.get("Contents") or []):
    key = (item.get("Key") or "").strip()
    parts = key.split("/")
    if len(parts) < 3 or parts[0] != "backups":
        continue
    host = parts[1]
    name = parts[-1]
    if host != node:
        continue
    if not (name.endswith(".7z") or name.endswith(".enc")):
        continue
    last_mod = item.get("LastModified") or ""
    size = int(item.get("Size") or 0)
    if prefer == "7z":
        rank = 0 if name.endswith(".7z") else 1
    else:
        rank = 0 if name.endswith(".enc") else 1
    rows.append((last_mod, rank, key, name, size))
rows.sort(key=lambda row: (row[1], row[0]), reverse=False)
for _, _, key, name, size in rows:
    print(f"{name}\t{size}\t{key}")
' "${selected_node}" "${BACKUP_PREFER:-${BOOTSTRAP_BACKUP_FORMAT:-openssl}}")"

if [ -z "${files_csv}" ]; then
  echo "No emergency-kit backups (.7z or .enc) found for node ${selected_node}." >&2
  exit 1
fi

file_rows=()
while IFS= read -r line; do
  [ -n "${line}" ] || continue
  file_rows+=("${line}")
done <<EOF
${files_csv}
EOF
selected_row="${file_rows[0]}"
selected_name="$(printf '%s' "${selected_row}" | cut -f1)"
selected_size="$(printf '%s' "${selected_row}" | cut -f2)"
selected_key="$(printf '%s' "${selected_row}" | cut -f3)"
echo "Using first backup found: ${selected_name} (${selected_size} bytes)"

download_dir="dist/ks-backups"
extract_dir="dist/ks-backups/extracted/${selected_node}"
mkdir -p "${download_dir}"

download_path="${download_dir}/${selected_node}-${selected_name}"
rm -f "${download_path}"
echo "Downloading s3://${R2_BUCKET}/${selected_key} -> ${download_path}"
aws s3 cp "s3://${R2_BUCKET}/${selected_key}" "${download_path}" \
  --endpoint-url "${R2_ENDPOINT}"

fingerprint_key="${selected_key}.passphrase.sha256"
remote_fingerprint="$(
  aws s3 cp "s3://${R2_BUCKET}/${fingerprint_key}" - \
    --endpoint-url "${R2_ENDPOINT}" 2>/dev/null | tr -d '\r\n' || true
)"

passphrase="$(resolve_passphrase)"
local_fingerprint="$(passphrase_fingerprint "${passphrase}")"
if [ -n "${remote_fingerprint}" ] && [ "${local_fingerprint}" != "${remote_fingerprint}" ]; then
  echo "Backup passphrase fingerprint mismatch for ${selected_name}." >&2
  echo "Local .env/cache passphrase does not match the archive uploaded for ${selected_node}." >&2
  echo "Local fingerprint:  ${local_fingerprint}" >&2
  echo "Archive fingerprint: ${remote_fingerprint}" >&2
  echo "Refusing extraction before 7z failure. Restore the original BOOTSTRAP_BACKUP_PASSPHRASE for this node or intentionally rotate with a new install." >&2
  exit 1
fi
rm -rf "${extract_dir}"
mkdir -p "${extract_dir}"
echo "Extracting ${download_path} -> ${extract_dir}"
if printf '%s' "${download_path}" | grep -Eq '\.7z$'; then
  seven_bin="$(ensure_7z_bin || true)"
  if [ -z "${seven_bin}" ]; then
    echo "Missing 7z/7za and automatic install did not succeed." >&2
    echo "Install 7-Zip manually, then rerun task fetch-backup." >&2
    exit 1
  fi
  "${seven_bin}" x -y -p"${passphrase}" -o"${extract_dir}" "${download_path}" >/dev/null
elif printf '%s' "${download_path}" | grep -Eq '\.enc$'; then
  tarball="${extract_dir}/emergency-kit.tar.gz"
  if ! openssl enc -d -aes-256-gcm -salt -pbkdf2 -iter 200000 \
      -in "${download_path}" -out "${tarball}" -pass "pass:${passphrase}" >/dev/null 2>&1; then
    openssl enc -d -aes-256-cbc -salt -pbkdf2 -iter 200000 \
      -in "${download_path}" -out "${tarball}" -pass "pass:${passphrase}" >/dev/null
  fi
  tar -xzf "${tarball}" -C "${extract_dir}"
  rm -f "${tarball}"
else
  echo "Unsupported downloaded backup type: ${download_path}" >&2
  exit 1
fi

echo "Backup wizard complete."
echo "Downloaded: ${download_path}"
echo "Extracted: ${extract_dir}"
