#!/usr/bin/env bash
#
# install.sh — install ech-keymgr on Linux (systemd hosts).
#
# Default mode: download the latest published release binary from
# GitHub Releases. If no releases exist yet (e.g. for early adopters
# tracking main), automatically falls back to building from source.
#
# Explicit modes:
#   --release <tag>           pin to a specific release (e.g. v0.1.0)
#   --release latest          pull latest release (default)
#   --from-source             clone and build from a git ref
#   --ref <gitref>            ref to build (with --from-source; default: main)
#   --binary <path>           install an already-built binary
#
# Other useful flags:
#   --prefix <dir>            install dir (default: /usr/local)
#   --no-systemd              skip systemd unit installation
#   --no-user                 skip creating the ech-keymgr system user
#   --user <name>             override the system user name (default: ech-keymgr)
#   --dry-run                 print what would happen, change nothing
#   -h | --help               show this help
#
# install.sh deliberately does NOT enable or start the systemd unit.
# After installation, edit /etc/ech-keymgr/config.yaml and then:
#
#     sudo systemctl daemon-reload
#     sudo systemctl enable --now ech-keymgr
#
# https://github.com/justinwoo280/ech-keymgr

set -euo pipefail

# ---- defaults ---------------------------------------------------------
REPO_OWNER="justinwoo280"
REPO_NAME="ech-keymgr"
REPO_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}"
RELEASES_API="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases"

PREFIX="/usr/local"
ETC_DIR="/etc/ech-keymgr"
KEY_PARENT_DIR="/etc/echkeydir"
STATE_DIR="/var/lib/ech-keymgr"
SVC_USER="ech-keymgr"
SVC_GROUP="ech-keymgr"
SYSTEMD_UNIT_DIR="/etc/systemd/system"

MODE="release"      # release | from-source | binary
RELEASE_TAG="latest"
GIT_REF="main"
LOCAL_BIN=""
INSTALL_SYSTEMD="yes"
CREATE_USER="yes"
DRY_RUN="no"

# ---- pretty output ----------------------------------------------------
COL_RESET=$'\033[0m'
COL_BOLD=$'\033[1m'
COL_RED=$'\033[31m'
COL_GREEN=$'\033[32m'
COL_YELLOW=$'\033[33m'
COL_BLUE=$'\033[34m'
COL_DIM=$'\033[2m'

# Disable colour if stdout is not a TTY.
if [ ! -t 1 ]; then
  COL_RESET="" COL_BOLD="" COL_RED="" COL_GREEN="" COL_YELLOW="" COL_BLUE="" COL_DIM=""
fi

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

# ---- usage ------------------------------------------------------------
usage() {
  sed -n '2,/^$/p' "$0" | sed -E 's/^# ?//'
  exit 0
}

# ---- arg parsing ------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --release)       MODE="release";       RELEASE_TAG="${2:-latest}"; shift 2 ;;
    --from-source)   MODE="from-source";   shift ;;
    --ref)           GIT_REF="${2:?--ref needs a git ref}"; shift 2 ;;
    --binary)        MODE="binary"; LOCAL_BIN="${2:?--binary needs a path}"; shift 2 ;;
    --prefix)        PREFIX="${2:?--prefix needs a dir}"; shift 2 ;;
    --user)          SVC_USER="${2:?--user needs a name}"; SVC_GROUP="$SVC_USER"; shift 2 ;;
    --no-systemd)    INSTALL_SYSTEMD="no"; shift ;;
    --no-user)       CREATE_USER="no"; shift ;;
    --dry-run)       DRY_RUN="yes"; shift ;;
    -h|--help)       usage ;;
    *) fail "unknown argument: $1 (try --help)" ;;
  esac
done

# ---- preflight --------------------------------------------------------
[ "$(id -u)" -eq 0 ] || fail "install.sh must run as root (use sudo)"

UNAME_OS="$(uname -s)"
UNAME_ARCH="$(uname -m)"
case "$UNAME_OS" in
  Linux) ;;
  *) fail "install.sh currently supports Linux only (got $UNAME_OS). On other OSes, build manually with 'go build -tags=all -o ech-keymgr ./cmd/ech-keymgr'." ;;
esac

case "$UNAME_ARCH" in
  x86_64|amd64)   GOARCH="amd64"  ;;
  aarch64|arm64)  GOARCH="arm64"  ;;
  armv7l|armv7)   GOARCH="arm"    ;;
  *) fail "unsupported architecture: $UNAME_ARCH" ;;
esac

if ! command -v systemctl >/dev/null 2>&1; then
  warn "systemctl not found; forcing --no-systemd"
  INSTALL_SYSTEMD="no"
fi

need() { command -v "$1" >/dev/null 2>&1 || fail "missing required tool: $1"; }
need install
need mkdir
need chmod
need chown
case "$MODE" in
  release)     need curl; need tar ;;
  from-source) need git; need go ;;
  binary)      [ -x "$LOCAL_BIN" ] || fail "--binary path is not an executable file: $LOCAL_BIN" ;;
esac

# Pretty banner.
log "ech-keymgr installer"
dim "    mode:        $MODE"
dim "    prefix:      $PREFIX"
dim "    config dir:  $ETC_DIR"
dim "    state dir:   $STATE_DIR"
dim "    keys dir:    $KEY_PARENT_DIR"
dim "    user/group:  $SVC_USER:$SVC_GROUP"
dim "    systemd:     $INSTALL_SYSTEMD"
dim "    arch:        $UNAME_ARCH ($GOARCH)"
[ "$DRY_RUN" = "yes" ] && warn "DRY RUN — no changes will be made"
echo

# ---- 1. Acquire the binary -------------------------------------------
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

acquire_release() {
  local tag="$1"
  log "Looking up release: $tag"

  local meta
  if [ "$tag" = "latest" ]; then
    meta="${RELEASES_API}/latest"
  else
    meta="${RELEASES_API}/tags/${tag}"
  fi

  # Try to find the asset for our arch.
  local resp
  resp="$(curl -fsSL --max-time 30 "$meta" 2>/dev/null || true)"
  if [ -z "$resp" ] || echo "$resp" | grep -q '"message": *"Not Found"'; then
    if [ "$tag" = "latest" ]; then
      warn "no published releases found at $REPO_URL/releases — falling back to building from source"
      MODE="from-source"
      acquire_from_source
      return
    fi
    fail "release $tag not found at $meta"
  fi

  # Pick a tarball asset that matches "linux_<arch>". We accept both
  # "ech-keymgr_<ver>_linux_amd64.tar.gz" (GoReleaser default) and
  # plain ".tar.gz" naming. First try a structured pick via grep.
  local asset_url
  asset_url="$(echo "$resp" \
    | grep -oE '"browser_download_url":[[:space:]]*"[^"]+"' \
    | sed -E 's/.*"([^"]+)"$/\1/' \
    | grep -E "linux[_-]${GOARCH}.*\\.tar\\.gz$" \
    | head -n1 || true)"

  [ -n "$asset_url" ] || fail "could not find a linux_${GOARCH} .tar.gz asset for release $tag"

  log "Downloading: $asset_url"
  run curl -fSL --progress-bar -o "$TMP_DIR/ech-keymgr.tar.gz" "$asset_url"
  run tar -xzf "$TMP_DIR/ech-keymgr.tar.gz" -C "$TMP_DIR"

  # GoReleaser may put the binary at top level or in a subdirectory.
  if [ "$DRY_RUN" = "yes" ]; then
    LOCAL_BIN="$TMP_DIR/ech-keymgr (dry-run placeholder)"
  else
    LOCAL_BIN="$(find "$TMP_DIR" -maxdepth 3 -type f -name 'ech-keymgr' -executable | head -n1)"
    [ -n "$LOCAL_BIN" ] || fail "release tarball did not contain an 'ech-keymgr' executable"
  fi

  ok "binary acquired: $(basename "$LOCAL_BIN")"
}

# resolve_remote_ref echoes the git ref to use for raw.githubusercontent.com
# fetches based on the chosen install mode. "latest" maps to "main" because
# raw.githubusercontent.com cannot resolve the literal string "latest".
resolve_remote_ref() {
  local ref="$GIT_REF"
  [ "$MODE" = "release" ] && ref="$RELEASE_TAG"
  [ "$ref" = "latest" ] && ref="main"
  printf '%s' "$ref"
}

acquire_from_source() {
  log "Cloning $REPO_URL @ $GIT_REF"
  run git clone --depth 1 --branch "$GIT_REF" "$REPO_URL" "$TMP_DIR/src" \
    || fail "git clone failed (does ref '$GIT_REF' exist?)"

  log "Building (CGO_ENABLED=0, -tags=all)"
  if [ "$DRY_RUN" = "yes" ]; then
    LOCAL_BIN="$TMP_DIR/src/ech-keymgr (dry-run placeholder)"
  else
    (
      cd "$TMP_DIR/src"
      CGO_ENABLED=0 go build -tags=all -trimpath \
        -ldflags="-s -w -X main.Version=${GIT_REF}" \
        -o ./ech-keymgr ./cmd/ech-keymgr
    )
    LOCAL_BIN="$TMP_DIR/src/ech-keymgr"
  fi
  ok "built: $LOCAL_BIN"
}

case "$MODE" in
  release)     acquire_release "$RELEASE_TAG" ;;
  from-source) acquire_from_source ;;
  binary)      ok "using pre-built binary: $LOCAL_BIN" ;;
esac

# ---- 2. Install the binary -------------------------------------------
BIN_PATH="$PREFIX/bin/ech-keymgr"
log "Installing binary → $BIN_PATH"
run install -d -m 0755 "$PREFIX/bin"
run install -m 0755 "$LOCAL_BIN" "$BIN_PATH"
ok "installed: $BIN_PATH"

# ---- 3. Create system user -------------------------------------------
if [ "$CREATE_USER" = "yes" ]; then
  if id "$SVC_USER" >/dev/null 2>&1; then
    dim "user $SVC_USER already exists"
  else
    log "Creating system user: $SVC_USER"
    if command -v useradd >/dev/null 2>&1; then
      run useradd --system --shell /usr/sbin/nologin --no-create-home \
        --home-dir "$STATE_DIR" "$SVC_USER"
    elif command -v adduser >/dev/null 2>&1; then
      run adduser --system --shell /usr/sbin/nologin --no-create-home \
        --home "$STATE_DIR" --group "$SVC_USER"
    else
      warn "neither useradd nor adduser found; skipping user creation"
    fi
    ok "user $SVC_USER created"
  fi
fi

# ---- 4. Directories --------------------------------------------------
log "Creating directories"
run install -d -m 0755 "$ETC_DIR"
run install -d -m 0700 -o "$SVC_USER" -g "$SVC_GROUP" "$STATE_DIR"
run install -d -m 0700 -o "$SVC_USER" -g "$SVC_GROUP" "$KEY_PARENT_DIR"
ok "directories ready"

# ---- 5. Config example -----------------------------------------------
CFG_PATH="$ETC_DIR/config.yaml"
ENV_PATH="$ETC_DIR/env"

if [ -f "$CFG_PATH" ]; then
  dim "$CFG_PATH already exists; leaving untouched"
else
  log "Installing example config → $CFG_PATH"
  if [ "$MODE" = "from-source" ] && [ -f "$TMP_DIR/src/examples/config.example.yaml" ]; then
    run install -m 0640 -o root -g "$SVC_GROUP" \
      "$TMP_DIR/src/examples/config.example.yaml" "$CFG_PATH"
  else
    # Fetch the example from the same release / ref.
    cfg_ref="$(resolve_remote_ref)"
    run curl -fsSL --max-time 15 \
      -o "$CFG_PATH" \
      "https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/${cfg_ref}/examples/config.example.yaml" \
      || fail "could not fetch config.example.yaml; please copy it manually"
    run chmod 0640 "$CFG_PATH"
    run chown root:"$SVC_GROUP" "$CFG_PATH"
  fi
  ok "example config installed (NOT auto-loaded; edit it before enabling the service)"
fi

if [ ! -f "$ENV_PATH" ]; then
  log "Installing empty env file → $ENV_PATH"
  if [ "$DRY_RUN" != "yes" ]; then
    cat > "$ENV_PATH" <<'EOF'
# /etc/ech-keymgr/env — secrets for ech-keymgr.service.
# This file is read by systemd before launching the daemon.
# Reference variables from /etc/ech-keymgr/config.yaml using
# ${VAR} or ${VAR:-default}.
#
# Example:
#   CF_API_TOKEN=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
#   PDNS_API_KEY=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
#   AKAMAI_CLIENT_SECRET=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
EOF
    chmod 0600 "$ENV_PATH"
    chown root:"$SVC_GROUP" "$ENV_PATH"
  fi
  ok "env file ready"
fi

# ---- 6. systemd unit -------------------------------------------------
if [ "$INSTALL_SYSTEMD" = "yes" ]; then
  UNIT_PATH="$SYSTEMD_UNIT_DIR/ech-keymgr.service"
  log "Installing systemd unit → $UNIT_PATH"

  if [ "$MODE" = "from-source" ] && [ -f "$TMP_DIR/src/examples/systemd/ech-keymgr.service" ]; then
    run install -m 0644 \
      "$TMP_DIR/src/examples/systemd/ech-keymgr.service" "$UNIT_PATH"
  else
    unit_ref="$(resolve_remote_ref)"
    run curl -fsSL --max-time 15 \
      -o "$UNIT_PATH" \
      "https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/${unit_ref}/examples/systemd/ech-keymgr.service" \
      || warn "could not fetch the systemd unit; install it manually"
    [ -f "$UNIT_PATH" ] && run chmod 0644 "$UNIT_PATH"
  fi

  log "Reloading systemd"
  run systemctl daemon-reload
  ok "systemd unit installed (NOT enabled, NOT started)"
fi

# ---- 7. Done — print next steps --------------------------------------
echo
log "ech-keymgr installation complete"
cat <<EOF

${COL_BOLD}Next steps (NOT automated, on purpose):${COL_RESET}

  1. Edit the configuration:
       sudo nano $CFG_PATH

  2. Put DNS provider secrets in:
       sudo nano $ENV_PATH

  3. Verify the binary works:
       ech-keymgr --version
       sudo -u $SVC_USER ech-keymgr -c $CFG_PATH status

  4. Bootstrap the initial HTTPS RR (one-shot, per domain):
       sudo -u $SVC_USER ech-keymgr -c $CFG_PATH init <fqdn>

  5. Enable the daemon when you're ready:
       sudo systemctl enable --now ech-keymgr
       journalctl -u ech-keymgr -f

  Useful one-shots without the daemon:
       sudo -u $SVC_USER ech-keymgr -c $CFG_PATH rotate <fqdn>
       sudo -u $SVC_USER ech-keymgr -c $CFG_PATH verify <fqdn>
       sudo -u $SVC_USER ech-keymgr -c $CFG_PATH keygen <fqdn>

  Documentation: $REPO_URL/blob/main/docs/install.md

EOF
