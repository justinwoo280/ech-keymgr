#!/bin/sh
# postinstall.sh — runs after `dpkg -i` / `rpm -i` of an ech-keymgr package.
#
# Same philosophy as scripts/install.sh: create users/dirs/units but
# DO NOT enable or start the service. The operator must edit
# /etc/ech-keymgr/config.yaml first.

set -e

SVC_USER="ech-keymgr"
SVC_GROUP="ech-keymgr"
ETC_DIR="/etc/ech-keymgr"
KEY_PARENT_DIR="/etc/echkeydir"
STATE_DIR="/var/lib/ech-keymgr"

# Create the system user if it doesn't exist.
if ! id "$SVC_USER" >/dev/null 2>&1; then
    if command -v useradd >/dev/null 2>&1; then
        useradd --system --shell /usr/sbin/nologin --no-create-home \
            --home-dir "$STATE_DIR" "$SVC_USER" 2>/dev/null || true
    elif command -v adduser >/dev/null 2>&1; then
        adduser --system --shell /usr/sbin/nologin --no-create-home \
            --home "$STATE_DIR" --group "$SVC_USER" 2>/dev/null || true
    fi
fi

# Make sure the data directories exist with safe permissions.
install -d -m 0755 "$ETC_DIR"
install -d -m 0700 -o "$SVC_USER" -g "$SVC_GROUP" "$STATE_DIR" 2>/dev/null || \
    { mkdir -p "$STATE_DIR"; chown "$SVC_USER":"$SVC_GROUP" "$STATE_DIR"; chmod 0700 "$STATE_DIR"; }
install -d -m 0700 -o "$SVC_USER" -g "$SVC_GROUP" "$KEY_PARENT_DIR" 2>/dev/null || \
    { mkdir -p "$KEY_PARENT_DIR"; chown "$SVC_USER":"$SVC_GROUP" "$KEY_PARENT_DIR"; chmod 0700 "$KEY_PARENT_DIR"; }

# Seed the empty env file if it doesn't already exist.
if [ ! -f "$ETC_DIR/env" ]; then
    cat > "$ETC_DIR/env" <<'EOF'
# /etc/ech-keymgr/env — secrets for ech-keymgr.service.
# Read by systemd before launching the daemon.
# Example:
#   CF_API_TOKEN=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
#   PDNS_API_KEY=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
EOF
    chmod 0600 "$ETC_DIR/env"
    chown "root:$SVC_GROUP" "$ETC_DIR/env" 2>/dev/null || true
fi

# Promote the example config into a real config if there isn't one yet.
if [ ! -f "$ETC_DIR/config.yaml" ] && [ -f "$ETC_DIR/config.example.yaml" ]; then
    cp "$ETC_DIR/config.example.yaml" "$ETC_DIR/config.yaml"
    chmod 0640 "$ETC_DIR/config.yaml"
    chown "root:$SVC_GROUP" "$ETC_DIR/config.yaml" 2>/dev/null || true
fi

# Refresh systemd's view, but do NOT enable or start the unit.
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi

cat <<EOF

ech-keymgr installed.

Next steps (NOT automated, on purpose):
  1. Edit  /etc/ech-keymgr/config.yaml
  2. Put secrets in  /etc/ech-keymgr/env
  3. Smoke-test:  sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml status
  4. When ready:  sudo systemctl enable --now ech-keymgr

Documentation: /usr/share/doc/ech-keymgr/install.md

EOF

exit 0
