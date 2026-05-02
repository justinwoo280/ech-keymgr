# Installing ech-keymgr

This guide gets a Linux + systemd host from zero to a running
ech-keymgr daemon in about 10 minutes. It covers the `install.sh`
helper, the manual route, and a brief tour of the post-install
files.

> **Out of scope**: this guide does not cover building nginx with
> ECH support, obtaining X.509 certificates for `public_name`, or
> configuring the DNS provider account itself. See the linked
> resources at the bottom for those.

---

## 1. Prerequisites

| Component   | Minimum                                         |
| ----------- | ----------------------------------------------- |
| OS          | Linux with systemd (Debian/Ubuntu, RHEL, Arch)  |
| Architecture| amd64, arm64, or armv7                          |
| Privileges  | `root` (via `sudo`) for the install step only   |
| Network     | outbound HTTPS to GitHub + your DNS provider    |
| Optional    | `go` 1.25+ if installing with `--from-source`   |

You do **not** need OpenSSL, Adobe Flash 😅, or any other system
library. ech-keymgr is a static, CGO-free binary.

---

## 2. Quick install (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/justinwoo280/ech-keymgr/main/scripts/install.sh \
  | sudo bash
```

The default mode pulls the latest published release for your
architecture. If no release exists yet (early-adopter scenario),
it transparently falls back to building from `main`.

### Variants

```bash
# Pin a specific release.
sudo bash install.sh --release v0.1.0

# Track main and rebuild from source.
sudo bash install.sh --from-source

# Build a specific tag from source.
sudo bash install.sh --from-source --ref v0.1.0

# Use a binary you already built (e.g. on a build server).
sudo bash install.sh --binary /tmp/ech-keymgr

# Preview without changing anything.
sudo bash install.sh --dry-run
```

### What the installer creates

| Path                                | Purpose                                       |
| ----------------------------------- | --------------------------------------------- |
| `/usr/local/bin/ech-keymgr`         | the binary                                    |
| `/etc/ech-keymgr/config.yaml`       | example config (NOT auto-loaded)              |
| `/etc/ech-keymgr/env`               | empty file for `EnvironmentFile=` secrets     |
| `/etc/echkeydir/`                   | parent dir for per-domain `*.ech` files       |
| `/var/lib/ech-keymgr/`              | reserved for future state                     |
| `/etc/systemd/system/ech-keymgr.service` | hardened systemd unit                    |
| system user `ech-keymgr`            | runs the daemon (`/usr/sbin/nologin`)         |

### What the installer does **not** do

- ❌ Does not enable the systemd unit
- ❌ Does not start the daemon
- ❌ Does not put any real configuration in `config.yaml`
- ❌ Does not touch your DNS records
- ❌ Does not create or modify any nginx configuration

This is deliberate. You configure first, then enable when you're ready.

---

## 3. First-run configuration

```bash
sudo nano /etc/ech-keymgr/config.yaml
```

The example file has every official provider commented out. Uncomment
the one you use, fill in the relevant fields, and reference its
credentials block from your domain's `dns.credentials_ref`.

Put secrets in the env file rather than in YAML:

```bash
sudo nano /etc/ech-keymgr/env
# CF_API_TOKEN=...
# PDNS_API_KEY=...
# AKAMAI_CLIENT_SECRET=...
```

Then reference them from `config.yaml` as `${CF_API_TOKEN}`.

---

## 4. Smoke-test before enabling the daemon

```bash
ech-keymgr --version
ech-keymgr --help

# Should parse the config and print "no keys yet".
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml status

# Bootstrap the initial HTTPS RR for one domain.
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml init hidden.example.com

# One-shot rotation.
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml rotate hidden.example.com

# Verify DNS matches local state.
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml verify hidden.example.com
```

The order matters: `init` creates the HTTPS RR with an initial
`ech=` value, `rotate` then runs the full state machine on top of it.

---

## 5. Enable the daemon

When the smoke test passes:

```bash
sudo systemctl enable --now ech-keymgr
sudo systemctl status ech-keymgr
journalctl -u ech-keymgr -f
```

You should see lines like:

```
ech-keymgr daemon: managing 1 domain(s); send SIGINT/SIGTERM to stop
ech-keymgr daemon: hidden.example.com: rotation succeeded
```

The daemon performs an immediate first rotation on startup, then
ticks on each domain's `rotation.interval` (default 3h).

---

## 6. nginx integration (informational)

ech-keymgr writes `*.ech` files into your domain's `keydir`. Point
nginx at the parent directory:

```nginx
http {
  ssl_echkeydir /etc/echkeydir/hidden.example.com;
  # ...
  server {
    listen 443 ssl;
    server_name hidden.example.com;
    ssl_certificate     /etc/letsencrypt/live/hidden.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/hidden.example.com/privkey.pem;
    # ECH outer name. Must have a working server block + certificate.
    # ...
  }
  server {
    listen 443 ssl;
    server_name example.com;          # public_name from config
    ssl_certificate     /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;
  }
}
```

After ech-keymgr writes a new `.ech` file, it reloads nginx via
your configured `reload.strategy`. The default config example uses
`exec: /usr/bin/systemctl reload nginx`, which works out of the box
with the included sudoers-friendly setup. If you prefer signal-based
reload, see the `signal` strategy in `config.example.yaml`.

---

## 7. Operating

```bash
# Show what's stored locally and whether DNS agrees.
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml status

# Force an off-cycle rotation.
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml rotate hidden.example.com

# Run reconciliation only (no key changes).
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml verify hidden.example.com

# Just generate a key on disk, do not push to DNS.
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml keygen hidden.example.com
```

`verify` exits non-zero on drift, so it's safe to wire into Nagios /
Prometheus blackbox / cron emails.

---

## 8. Upgrades

```bash
# Same script, same flags. install.sh detects an existing config
# and won't overwrite it.
curl -fsSL https://raw.githubusercontent.com/justinwoo280/ech-keymgr/main/scripts/install.sh \
  | sudo bash

sudo systemctl restart ech-keymgr
```

To switch from "track main" to a stable release:

```bash
sudo bash install.sh --release v0.2.0
sudo systemctl restart ech-keymgr
```

---

## 9. Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/justinwoo280/ech-keymgr/main/scripts/uninstall.sh \
  | sudo bash
```

By default this is **interactive** — it asks before touching anything
that contains your data. Variants:

```bash
sudo bash uninstall.sh --keep-data   # remove binary + service, keep keys/config
sudo bash uninstall.sh --purge       # remove EVERYTHING, no prompts
```

> ⚠️ Be careful with `--purge` if nginx is still serving the domains —
> deleting `/etc/echkeydir/` while nginx is loaded will silently leave
> ECH disabled on the next reload.

---

## 10. Manual install (without install.sh)

If you don't want to run the helper:

```bash
# 1. Build (or download a release tarball and extract).
git clone --depth 1 https://github.com/justinwoo280/ech-keymgr
cd ech-keymgr
CGO_ENABLED=0 go build -tags=all -trimpath \
  -ldflags="-s -w" -o ech-keymgr ./cmd/ech-keymgr

# 2. Place the binary.
sudo install -m 0755 ech-keymgr /usr/local/bin/

# 3. Make a system user.
sudo useradd --system --shell /usr/sbin/nologin --no-create-home ech-keymgr

# 4. Directories.
sudo install -d -m 0755 /etc/ech-keymgr
sudo install -d -m 0700 -o ech-keymgr -g ech-keymgr /etc/echkeydir
sudo install -d -m 0700 -o ech-keymgr -g ech-keymgr /var/lib/ech-keymgr

# 5. Config.
sudo install -m 0640 -o root -g ech-keymgr \
  examples/config.example.yaml /etc/ech-keymgr/config.yaml
sudo touch /etc/ech-keymgr/env
sudo chmod 0600 /etc/ech-keymgr/env

# 6. systemd unit.
sudo install -m 0644 examples/systemd/ech-keymgr.service \
  /etc/systemd/system/
sudo systemctl daemon-reload
```

Now jump back to step 3 above.

---

## See also

- [`docs/architecture.md`](architecture.md) — design, state machine, scope boundaries
- [`docs/provider-guide.md`](provider-guide.md) — write a new DNS provider
- `examples/systemd/ech-keymgr.service` — the hardened unit, fully commented
- `examples/config.example.yaml` — every supported config knob, commented
- [defo.ie ECH guides](https://defo.ie/) — building nginx with ECH support
- [RFC 9849](https://www.rfc-editor.org/rfc/rfc9849.html) — the ECH standard
- [RFC 9460](https://www.rfc-editor.org/rfc/rfc9460.html) — SVCB / HTTPS records
