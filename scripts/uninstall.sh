#!/usr/bin/env bash
#
# uninstall.sh — remove ech-keymgr from a host.
#
# By default this is INTERACTIVE: it asks before touching anything
# that contains your data (config, keys, state). Use --purge to skip
# all prompts and remove everything.
#
# Flags:
#   --purge       remove EVERYTHING without prompting (config, keys, state, user)
#   --keep-data   keep config + keys + state, even non-interactively
#   --yes         non-interactive: keep data (same as --keep-data) and proceed
#   --user <name> override system user name (default: ech-keymgr)
#   --prefix <d>  install prefix (default: /usr/local)
#   --dry-run     print what would happen
#   -h | --help   show this help
#
# https://github.com/justinwoo280/ech-keymgr

set -euo pipefail

PREFIX="/usr/local"
ETC_DIR="/etc/ech-keymgr"
KEY_PARENT_DIR="/etc/echkeydir"
STATE_DIR="/var/lib/ech-keymgr"
SVC_USER="ech-keymgr"
SYSTEMD_UNIT_DIR="/etc/systemd/system"

MODE="interactive"     # interactive | purge | keep-data
DRY_RUN="no"

COL_RESET=$'\033[0m'
COL_BOLD=$'\033[1m'
COL_RED=$'\033[31m'
COL_GREEN=$'\033[32m'
COL_YELLOW=$'\033[33m'
COL_BLUE=$'\033[34m'
COL_DIM=$'\033[2m'
[ -t 1 ] || { COL_RESET="" COL_BOLD="" COL_RED="" COL_GREEN="" COL_YELLOW="" COL_BLUE="" COL_DIM=""; }

log()   { printf "%s==>%s %s\n"     "${COL_BLUE}${COL_BOLD}" "${COL_RESET}" "$*"; }
ok()    { printf "%s ✓%s %s\n"      "${COL_GREEN}"           "${COL_RESET}" "$*"; }
warn()  { printf "%s ⚠%s %s\n"      "${COL_YELLOW}"          "${COL_RESET}" "$*" >&2; }
fail()  { printf "%s ✗%s %s\n"      "${COL_RED}${COL_BOLD}"  "${COL_RESET}" "$*" >&2; exit 1; }
dim()   { printf "%s%s%s\n"         "${COL_DIM}"             "${COL_RESET}" "$*"; }
run() {
  if [ "$DRY_RUN" = "yes" ]; then
    printf "%s   [dry-run]%s %s\n" "${COL_DIM}" "${COL_RESET}" "$*"
  else
    "$@"
  fi
}

usage() { sed -n '2,/^$/p' "$0" | sed -E 's/^# ?//'; exit 0; }

# Ask a yes/no question. Default is given as second arg ("y" or "n").
confirm() {
  local prompt="$1" default="${2:-n}" reply
  local hint="[y/N]"
  [ "$default" = "y" ] && hint="[Y/n]"

  if [ "$MODE" != "interactive" ]; then
    [ "$MODE" = "purge" ] && return 0 || return 1
  fi
  printf "%s? %s " "$prompt" "$hint" >&2
  read -r reply || reply=""
  reply="${reply:-$default}"
  case "$reply" in
    y|Y|yes|YES) return 0 ;;
    *)           return 1 ;;
  esac
}

while [ $# -gt 0 ]; do
  case "$1" in
    --purge)     MODE="purge"; shift ;;
    --keep-data) MODE="keep-data"; shift ;;
    --yes)       MODE="keep-data"; shift ;;
    --user)      SVC_USER="${2:?--user needs a name}"; shift 2 ;;
    --prefix)    PREFIX="${2:?--prefix needs a dir}"; shift 2 ;;
    --dry-run)   DRY_RUN="yes"; shift ;;
    -h|--help)   usage ;;
    *) fail "unknown argument: $1 (try --help)" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || fail "uninstall.sh must run as root (use sudo)"

BIN_PATH="$PREFIX/bin/ech-keymgr"
UNIT_PATH="$SYSTEMD_UNIT_DIR/ech-keymgr.service"

log "ech-keymgr uninstaller"
dim "    mode:        $MODE"
dim "    prefix:      $PREFIX"
[ "$DRY_RUN" = "yes" ] && warn "DRY RUN — no changes will be made"
echo

# ---- 1. Stop / disable the systemd unit ------------------------------
if command -v systemctl >/dev/null 2>&1 && [ -f "$UNIT_PATH" ]; then
  if systemctl is-active --quiet ech-keymgr.service 2>/dev/null; then
    log "Stopping ech-keymgr.service"
    run systemctl stop ech-keymgr.service || warn "stop failed (continuing)"
  fi
  if systemctl is-enabled --quiet ech-keymgr.service 2>/dev/null; then
    log "Disabling ech-keymgr.service"
    run systemctl disable ech-keymgr.service || warn "disable failed (continuing)"
  fi
  log "Removing systemd unit: $UNIT_PATH"
  run rm -f "$UNIT_PATH"
  run systemctl daemon-reload
  ok "systemd unit removed"
else
  dim "no systemd unit installed"
fi

# ---- 2. Remove the binary --------------------------------------------
if [ -f "$BIN_PATH" ]; then
  log "Removing binary: $BIN_PATH"
  run rm -f "$BIN_PATH"
  ok "binary removed"
else
  dim "no binary at $BIN_PATH"
fi

# ---- 3. Decide what to do with data ----------------------------------
echo
case "$MODE" in
  purge)
    warn "PURGE mode — config, keys, and state will be DELETED"
    REMOVE_CONFIG="yes"; REMOVE_KEYS="yes"; REMOVE_STATE="yes"; REMOVE_USER="yes"
    ;;
  keep-data)
    log "Keeping config, keys, state, and the system user"
    REMOVE_CONFIG="no";  REMOVE_KEYS="no";  REMOVE_STATE="no";  REMOVE_USER="no"
    ;;
  interactive)
    REMOVE_CONFIG="no"; REMOVE_KEYS="no"; REMOVE_STATE="no"; REMOVE_USER="no"
    log "Choose what to remove (defaults are SAFE — keep data):"
    confirm "Remove config dir   $ETC_DIR" n         && REMOVE_CONFIG="yes"
    confirm "Remove keys dir     $KEY_PARENT_DIR (nginx may still need these!)" n && REMOVE_KEYS="yes"
    confirm "Remove state dir    $STATE_DIR (.meta.json + private keys)" n && REMOVE_STATE="yes"
    confirm "Remove system user  $SVC_USER" n        && REMOVE_USER="yes"
    ;;
esac

# ---- 4. Apply choices -----------------------------------------------
if [ "$REMOVE_CONFIG" = "yes" ] && [ -d "$ETC_DIR" ]; then
  log "Removing $ETC_DIR"
  run rm -rf "$ETC_DIR"
  ok "config removed"
fi

if [ "$REMOVE_KEYS" = "yes" ] && [ -d "$KEY_PARENT_DIR" ]; then
  log "Removing $KEY_PARENT_DIR"
  run rm -rf "$KEY_PARENT_DIR"
  ok "keys removed"
fi

if [ "$REMOVE_STATE" = "yes" ] && [ -d "$STATE_DIR" ]; then
  log "Removing $STATE_DIR"
  run rm -rf "$STATE_DIR"
  ok "state removed"
fi

if [ "$REMOVE_USER" = "yes" ] && id "$SVC_USER" >/dev/null 2>&1; then
  log "Removing system user: $SVC_USER"
  if command -v userdel >/dev/null 2>&1; then
    run userdel "$SVC_USER" || warn "userdel failed (user may own files outside our dirs)"
  elif command -v deluser >/dev/null 2>&1; then
    run deluser "$SVC_USER" || warn "deluser failed"
  else
    warn "neither userdel nor deluser found; user not removed"
  fi
  ok "user removed"
fi

# ---- 5. Summary ------------------------------------------------------
echo
log "ech-keymgr uninstall complete"

if [ "$REMOVE_CONFIG" != "yes" ] && [ -d "$ETC_DIR" ]; then
  dim "  preserved: $ETC_DIR"
fi
if [ "$REMOVE_KEYS" != "yes" ] && [ -d "$KEY_PARENT_DIR" ]; then
  dim "  preserved: $KEY_PARENT_DIR  (nginx ssl_echkeydir)"
fi
if [ "$REMOVE_STATE" != "yes" ] && [ -d "$STATE_DIR" ]; then
  dim "  preserved: $STATE_DIR  (.meta.json + private keys)"
fi
if [ "$REMOVE_USER" != "yes" ] && id "$SVC_USER" >/dev/null 2>&1; then
  dim "  preserved: system user '$SVC_USER'"
fi
echo
