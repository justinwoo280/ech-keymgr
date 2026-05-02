#!/bin/sh
# preremove.sh — runs before `dpkg -r` / `rpm -e` of an ech-keymgr package.
#
# Stops/disables the systemd unit if active. Does NOT delete config,
# keys, or the system user — preserving data on package removal is
# standard policy. To wipe everything, use scripts/uninstall.sh --purge
# AFTER the package has been removed, or `apt purge` / `rpm -e --erase`
# variants.

set -e

if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet ech-keymgr.service 2>/dev/null; then
        systemctl stop ech-keymgr.service >/dev/null 2>&1 || true
    fi
    if systemctl is-enabled --quiet ech-keymgr.service 2>/dev/null; then
        systemctl disable ech-keymgr.service >/dev/null 2>&1 || true
    fi
fi

exit 0
