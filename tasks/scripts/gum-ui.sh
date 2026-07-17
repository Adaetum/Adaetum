#!/usr/bin/env bash

# Shared optional terminal presentation helpers. Gum never owns a setup
# decision or a secret: every caller keeps a plain-shell fallback so recovery,
# automation, and minimal operator workstations remain supported.

# Use the terminal's own ANSI palette so light, dark, and high-contrast themes
# remain legible. Body copy intentionally uses the terminal's default color.
ADAETUM_UI_PRIMARY=4
ADAETUM_UI_ACCENT=5
ADAETUM_UI_MUTED=""
ADAETUM_UI_BORDER=8
ADAETUM_UI_SUCCESS=2
ADAETUM_UI_WARNING=3
ADAETUM_UI_ERROR=1

adaetum_ui_width() {
  local columns=80 width=""
  if [ -r /dev/tty ] && command -v stty >/dev/null 2>&1; then
    columns="$(stty size </dev/tty 2>/dev/null | awk '{print $2}')"
  elif [ -n "${COLUMNS:-}" ]; then
    columns="${COLUMNS}"
  elif command -v tput >/dev/null 2>&1; then
    columns="$(tput cols 2>/dev/null || printf '80')"
  fi
  case "${columns}" in ''|*[!0-9]*) columns=80 ;; esac
  width=$((columns - 4))
  [ "${width}" -gt 88 ] && width=88
  [ "${width}" -lt 40 ] && width=40
  printf '%s' "${width}"
}

adaetum_gum_enabled() {
  # Inputs are commonly collected through command substitution, which captures
  # stdout while the terminal remains attached on stdin. Gum deliberately uses
  # that pattern, so only require an interactive input terminal here.
  [ "${ADAETUM_GUM_UI:-1}" != "0" ] && [ -t 0 ] && command -v gum >/dev/null 2>&1
}

adaetum_ui_silent_enabled() {
  case "${ADAETUM_INIT_SILENT:-0}" in
    1|true|yes) return 0 ;;
    *) return 1 ;;
  esac
}

adaetum_gum_input() {
  local label="$1"
  local default="${2:-}"
  local secret="${3:-0}"
  local -a args=(input)

  gum style --foreground "${ADAETUM_UI_ACCENT}" --bold "${label}" >&2

  if [ "${secret}" = "1" ]; then
    # Existing secrets must never be placed in a UI default or echoed back.
    args+=(--password --placeholder "Enter securely")
  elif [ -n "${default}" ]; then
    args+=(--value "${default}" --placeholder "Press Enter to accept the default")
  else
    args+=(--placeholder "Enter a value")
  fi
  gum "${args[@]}"
}

adaetum_gum_confirm() {
  local label="$1"
  local default="${2:-y}"
  if [ "${default}" = "y" ]; then
    gum confirm --default=true "${label}"
  else
    gum confirm --default=false "${label}"
  fi
}

adaetum_gum_choose() {
  local label="$1"
  shift
  gum style --foreground "${ADAETUM_UI_ACCENT}" --bold "${label}" >&2
  gum choose "$@"
}

adaetum_gum_heading() {
  gum style --border rounded --padding "0 1" --border-foreground "${ADAETUM_UI_PRIMARY}" --bold "$1"
}

# These are the one presentation contract for every interactive setup stage.
# Callers provide content and decisions; they must not recreate terminal UI.
# The plain-text versions retain every state label so color is never the only
# signal available to an operator or log reader.
adaetum_ui_hero() {
  local eyebrow="$1" title="$2" detail="$3"
  if adaetum_gum_enabled; then
    gum style --foreground "${ADAETUM_UI_ACCENT}" --bold --align center "${eyebrow}"
    gum style --border double --border-foreground "${ADAETUM_UI_PRIMARY}" --padding "1 3" --align center --bold "${title}"
    gum style --foreground "${ADAETUM_UI_MUTED}" --align center "${detail}"
    printf '\n'
    return
  fi
  printf '\n╔════════════════════════════════════════════════════════════╗\n'
  printf '║ %-58s ║\n' "${eyebrow}"
  printf '║ %-58s ║\n' "${title}"
  while IFS= read -r line; do
    printf '║ %-58s ║\n' "${line}"
  done < <(printf '%s\n' "${detail}" | fold -s -w 58)
  printf '╚════════════════════════════════════════════════════════════╝\n\n'
}

adaetum_ui_roadmap() {
  local content="$*" width=""
  if adaetum_gum_enabled; then
    width="$(adaetum_ui_width)"
    if [ "${width}" -lt 70 ]; then
      content="${content//  ›  /$'\n'}"
    fi
    gum style --border rounded --border-foreground "${ADAETUM_UI_BORDER}" --padding "0 1" --foreground "${ADAETUM_UI_MUTED}" "${content}"
    return
  fi
  printf 'Plan: %s\n' "$*"
}

adaetum_ui_progress() {
  local current="$1" total="$2" label="$3" completed="" upcoming="" i="" indicator=""
  for ((i = 1; i < current; i++)); do completed="${completed}■"; done
  for ((i = current + 1; i <= total; i++)); do upcoming="${upcoming}□"; done
  indicator="${completed}◆${upcoming}"
  if adaetum_gum_enabled; then
    gum style --foreground "${ADAETUM_UI_ACCENT}" --bold "${indicator}  SECTION ${current}/${total}"
    adaetum_gum_heading "${label}"
    return
  fi
  printf '[%s] SECTION %s/%s — %s\n' "${indicator}" "${current}" "${total}" "${label}"
}

adaetum_ui_phase() {
  local current="$1" total="$2" label="$3" detail="$4"
  printf '\n'
  adaetum_ui_progress "${current}" "${total}" "${label}"
  if [ -n "${detail}" ]; then
    adaetum_ui_message "${ADAETUM_UI_MUTED}" "${detail}"
  fi
}

adaetum_ui_panel() {
  local title="$1"
  if adaetum_gum_enabled; then
    printf '\n'
    gum style --foreground "${ADAETUM_UI_PRIMARY}" --bold "◆  ${title}"
    return
  fi
  printf '\n-- %s --\n' "${title}"
}

adaetum_ui_milestone() {
  local index="$1" label="$2"
  if adaetum_gum_enabled; then
    printf '\n'
    gum style --border normal --border-foreground "${ADAETUM_UI_ACCENT}" --padding "0 1" --bold "${index}  ${label}"
    return
  fi
  printf '\n  [%s] %s\n' "${index}" "${label}"
}

adaetum_ui_task() {
  local index="$1" label="$2"
  if adaetum_gum_enabled; then
    gum style --foreground "${ADAETUM_UI_MUTED}" "  ◇  ${index}  ${label}"
    return
  fi
  printf '  ◇  %s  %s\n' "${index}" "${label}"
}

adaetum_ui_subtask() {
  local index="$1" label="$2"
  if adaetum_gum_enabled; then
    gum style --foreground "${ADAETUM_UI_MUTED}" "     ·  ${index}  ${label}"
    return
  fi
  printf '     ·  %s  %s\n' "${index}" "${label}"
}

adaetum_ui_status() {
  local state="$1" message="$2" color="${ADAETUM_UI_MUTED}" marker="[INFO]"
  case "${state}" in
    success) color="${ADAETUM_UI_SUCCESS}"; marker="[DONE]" ;;
    warning) color="${ADAETUM_UI_WARNING}"; marker="[WARN]" ;;
    error) color="${ADAETUM_UI_ERROR}"; marker="[ERROR]" ;;
    info) color="${ADAETUM_UI_PRIMARY}"; marker="[INFO]" ;;
  esac
  if adaetum_gum_enabled; then
    gum style --width "$(adaetum_ui_width)" --foreground "${color}" "${marker} ${message}"
    return
  fi
  printf '%s %s\n' "${marker}" "${message}"
}

adaetum_ui_key_value() {
  local label="$1" value="$2"
  if adaetum_gum_enabled; then
    gum style "  ${label}:  ${value}"
    return
  fi
  printf '  %-22s %s\n' "${label}:" "${value}"
}

adaetum_ui_completion() {
  local title="$1" detail="$2"
  printf '\n'
  if adaetum_gum_enabled; then
    gum style --border double --border-foreground "${ADAETUM_UI_SUCCESS}" --padding "1 3" --bold --align center "${title}"
    gum style --foreground "${ADAETUM_UI_MUTED}" --align center "${detail}"
    return
  fi
  printf '╔════════════════════════════════════════════════════════════╗\n'
  printf '║ %-58s ║\n' "${title}"
  while IFS= read -r line; do
    printf '║ %-58s ║\n' "${line}"
  done < <(printf '%s\n' "${detail}" | fold -s -w 58)
  printf '╚════════════════════════════════════════════════════════════╝\n'
}

adaetum_ui_heading() {
  if adaetum_gum_enabled; then adaetum_gum_heading "$1"; else printf '\n== %s ==\n' "$1"; fi
}

adaetum_ui_message() {
  if adaetum_gum_enabled; then gum style --width "$(adaetum_ui_width)" --foreground "$1" "$2"; else printf '%s\n' "$2"; fi
}

adaetum_ui_confirm() {
  local label="$1" default="${2:-y}" answer=""
  if adaetum_ui_silent_enabled; then
    adaetum_ui_status info "Silent replay: ${label} $([ "${default}" = y ] && printf Yes || printf No)"
    [ "${default}" = y ]
    return
  fi
  if adaetum_gum_enabled; then adaetum_gum_confirm "${label}" "${default}"; return $?; fi
  read -r -p "${label} [$([ "${default}" = y ] && printf 'Y/n' || printf 'y/N')]: " answer
  if [ "${default}" = y ]; then [[ "${answer}" =~ ^(|y|Y|yes|YES)$ ]]; else [[ "${answer}" =~ ^(y|Y|yes|YES)$ ]]; fi
}

adaetum_ui_input() {
  local label="$1" default="$2" secret="${3:-0}" value=""
  if adaetum_ui_silent_enabled; then
    if [ -z "${default}" ]; then
      printf '[ERROR] Silent replay has no saved value for %s. Run task init interactively once.\n' "${label}" >&2
      return 1
    fi
    printf '[INFO] Silent replay: using the saved/default value for %s.\n' "${label}" >&2
    printf '%s' "${default}"
    return 0
  fi
  if adaetum_gum_enabled; then adaetum_gum_input "${label}" "${default}" "${secret}"; return; fi
  if [ "${secret}" = 1 ]; then
    read -r -s -p "${label}: " value
    printf '\n' >&2
  else
    read -r -p "${label}${default:+ [${default}]}: " value
  fi
  printf '%s' "${value:-${default}}"
}

adaetum_ui_choose() {
  local label="$1" option="" index=1 selection=""
  shift
  if adaetum_ui_silent_enabled; then
    [ "$#" -gt 0 ] || return 1
    printf '[INFO] Silent replay: selected the saved/default choice for %s.\n' "${label}" >&2
    printf '%s' "$1"
    return 0
  fi
  if adaetum_gum_enabled; then adaetum_gum_choose "${label}" "$@"; return; fi
  printf '%s\n' "${label}" >&2
  for option in "$@"; do printf '  %s) %s\n' "${index}" "${option}" >&2; index=$((index + 1)); done
  while true; do
    read -r -p "Choose an option [1-${#}]: " selection
    case "${selection}" in
      ''|*[!0-9]*) ;;
      *) [ "${selection}" -ge 1 ] && [ "${selection}" -le "$#" ] && { printf '%s' "${@:${selection}:1}"; return; } ;;
    esac
  done
}

adaetum_open_url() {
  local url="$1"

  if command -v open >/dev/null 2>&1; then
    open "${url}" >/dev/null 2>&1 && return 0
  fi
  if command -v xdg-open >/dev/null 2>&1; then
    xdg-open "${url}" >/dev/null 2>&1 && return 0
  fi
  if command -v powershell.exe >/dev/null 2>&1; then
    powershell.exe -NoProfile -Command "Start-Process '${url}'" >/dev/null 2>&1 && return 0
  fi
  return 1
}
