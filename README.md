# ech-keymgr

> **Automated HPKE key & ECHConfig rotation for TLS Encrypted Client Hello (RFC 9849).**
> Pure Go. Zero CGO. nginx-friendly. DNS-provider pluggable (acme.sh style).

[![test](https://github.com/justinwoo280/ech-keymgr/actions/workflows/test.yml/badge.svg)](https://github.com/justinwoo280/ech-keymgr/actions/workflows/test.yml)
[![lint](https://github.com/justinwoo280/ech-keymgr/actions/workflows/lint.yml/badge.svg)](https://github.com/justinwoo280/ech-keymgr/actions/workflows/lint.yml)
[![build](https://github.com/justinwoo280/ech-keymgr/actions/workflows/build.yml/badge.svg)](https://github.com/justinwoo280/ech-keymgr/actions/workflows/build.yml)
[![codeql](https://github.com/justinwoo280/ech-keymgr/actions/workflows/codeql.yml/badge.svg)](https://github.com/justinwoo280/ech-keymgr/actions/workflows/codeql.yml)
[![govulncheck](https://github.com/justinwoo280/ech-keymgr/actions/workflows/govulncheck.yml/badge.svg)](https://github.com/justinwoo280/ech-keymgr/actions/workflows/govulncheck.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/justinwoo280/ech-keymgr)](https://goreportcard.com/report/github.com/justinwoo280/ech-keymgr)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/justinwoo280/ech-keymgr.svg)](https://pkg.go.dev/github.com/justinwoo280/ech-keymgr)

`ech-keymgr` is the **ACME-for-ECH equivalent**: just as you keep your TLS
certificates managed by certbot / lego / acme.sh, you let `ech-keymgr`
manage the ECH HPKE key material in parallel — generation, rotation,
DNS publication, and hot-reload — all without dropping in-flight
TLS 1.3 handshakes.

---

## What it does

| ✅ In scope                                                              | ❌ Out of scope                                              |
| ----------------------------------------------------------------------- | ----------------------------------------------------------- |
| Generate HPKE key pairs (X25519 + HKDF-SHA256)                          | X.509 certificates for the `public_name` (use ACME)         |
| Maintain a rolling `ECHConfigList` (multi-key for zero-downtime rotation) | nginx config generation                                    |
| Write `*.ech` files in [`draft-farrell-tls-pemesni`][pemfmt] format       | Validating that `public_name` resolves                      |
| Publish/patch the `ech=` parameter in **one** HTTPS DNS RR              | Generic SVCB management for non-ECH params                  |
| Trigger nginx hot-reload (signal / exec / systemd)                      | Split-mode CFS routing                                      |
| Soft DNS reconciliation (`verify` subcommand)                           | DNSSEC signing                                              |

[pemfmt]: https://datatracker.ietf.org/doc/draft-farrell-tls-pemesni/

---

## Status

**Beta — feature-complete and well tested. Used in early production.**

- ✅ All 18 planned tasks complete
- ✅ 153 unit tests across 13 packages, full Linux/macOS/Windows × Go 1.22 – 1.25 matrix
- ✅ Cloudflare, AWS Route 53, Akamai Edge DNS, PowerDNS officially supported
- ✅ Hardened systemd unit + interactive install/uninstall
- ⏳ First public release (`v0.1.0`) coming soon

Tracks **[RFC 9849][rfc9849]** (TLS ECH) and **[RFC 9848][rfc9848]** (ECH in
SVCB/HTTPS RRs), both published March 2026, plus **[RFC 9460][rfc9460]**
(SVCB/HTTPS RR types).

[rfc9849]: https://www.rfc-editor.org/rfc/rfc9849.html
[rfc9848]: https://www.rfc-editor.org/rfc/rfc9848.html
[rfc9460]: https://www.rfc-editor.org/rfc/rfc9460.html

---

## Quick install

```bash
curl -fsSL https://raw.githubusercontent.com/justinwoo280/ech-keymgr/main/scripts/install.sh \
  | sudo bash
```

The default mode pulls the latest published release for your CPU
architecture; if no release exists yet, it transparently falls back
to building from source. After install you'll find:

| Path                                | Purpose                                       |
| ----------------------------------- | --------------------------------------------- |
| `/usr/local/bin/ech-keymgr`         | the binary                                    |
| `/etc/ech-keymgr/config.yaml`       | example config (NOT auto-loaded)              |
| `/etc/ech-keymgr/env`               | empty env file for `EnvironmentFile=` secrets |
| `/etc/echkeydir/`                   | parent dir for per-domain `*.ech` files       |
| `/etc/systemd/system/ech-keymgr.service` | hardened systemd unit (NOT enabled)      |

The installer **deliberately does not enable or start the service**.
Edit your config, smoke-test, then enable when you're ready. See
[`docs/install.md`](docs/install.md) for the full walkthrough.

### Other install paths

```bash
sudo bash install.sh --release v0.1.0      # pin a release
sudo bash install.sh --from-source         # track main, rebuild from source
sudo bash install.sh --binary /tmp/bin     # already built it yourself
sudo bash install.sh --dry-run             # preview only
```

### Build manually

```bash
git clone https://github.com/justinwoo280/ech-keymgr
cd ech-keymgr
CGO_ENABLED=0 go build -tags=all -trimpath -o ech-keymgr ./cmd/ech-keymgr
```

---

## Quick start (5 minutes)

```bash
# 1. Edit config (uncomment your DNS provider, fill in zone + cred ref)
sudo nano /etc/ech-keymgr/config.yaml

# 2. Put secrets in the env file
sudo nano /etc/ech-keymgr/env
#   CF_API_TOKEN=...

# 3. Verify it parses
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml status

# 4. Bootstrap the initial HTTPS RR for your domain
sudo -u ech-keymgr ech-keymgr -c /etc/ech-keymgr/config.yaml init hidden.example.com

# 5. Run the daemon
sudo systemctl enable --now ech-keymgr
journalctl -u ech-keymgr -f
```

Point nginx at the keydir:

```nginx
http {
  ssl_echkeydir /etc/echkeydir/hidden.example.com;
  # ...
}
```

That's it. New keys mint every 3 hours by default; old keys remain
accepted for 6 hours of grace; DNS gets atomically updated; nginx
reloads itself; in-flight handshakes never drop.

---

## CLI cheat sheet

| Command                                 | What it does                                   |
| --------------------------------------- | ---------------------------------------------- |
| `ech-keymgr daemon`                     | run continuously (use systemd, not nohup)      |
| `ech-keymgr rotate [fqdn]`              | one rotation cycle (cron-friendly)             |
| `ech-keymgr verify [fqdn]`              | reconcile DNS vs local state (warn-only)       |
| `ech-keymgr status`                     | tabular view of all managed domains            |
| `ech-keymgr init <fqdn>`                | create the initial HTTPS RR (one-shot)         |
| `ech-keymgr keygen <fqdn>`              | mint a key + write `.ech` (no DNS, no reload)  |
| `ech-keymgr completion bash\|zsh\|fish` | shell autocompletion                           |

Pass `-c /path/to/config.yaml` to override the default config location.

---

## DNS providers

`ech-keymgr` ships with **four officially maintained** DNS providers:

| Provider              | Build tag    | Auth                  | Notes                                                    |
| --------------------- | ------------ | --------------------- | -------------------------------------------------------- |
| **Cloudflare**        | `cloudflare` | API Token (Bearer)    | Reference implementation; structured `data` field        |
| **AWS Route 53**      | `route53`    | sigv4 (SDK chain)     | Atomic UPSERT; works with IRSA / instance profile        |
| **Akamai Edge DNS**   | `akamai`     | EdgeGrid HMAC-SHA256  | Reads `~/.edgerc` or inline credentials                  |
| **PowerDNS** (Auth)   | `powerdns`   | `X-API-Key`           | Single PATCH RRset; ideal for self-hosted                |

All four are compiled into the default release. Slim builds:

```bash
go build -tags=cloudflare ./cmd/ech-keymgr           # only Cloudflare
go build -tags=route53,akamai ./cmd/ech-keymgr       # multi-vendor
```

### Adding more providers (community welcome!)

The `Provider` interface is **deliberately tiny** (3 methods, RFC 9460
strings only). New providers live under `providers/community/<name>/`
and follow the [acme.sh][acmesh] community-PR model — see
[`docs/provider-guide.md`](docs/provider-guide.md) and the
[provider PR template](.github/PULL_REQUEST_TEMPLATE/provider.md).

[acmesh]: https://github.com/acmesh-official/acme.sh

---

## Architecture in 30 seconds

```text
                       ┌─────────────────┐
                       │  ech-keymgr     │
                       │   (daemon)      │
                       └────────┬────────┘
                                │
            ┌───────────────────┼───────────────────┐
            ▼                   ▼                   ▼
   ┌─────────────────┐ ┌──────────────────┐ ┌──────────────┐
   │ keystore        │ │ DNS provider     │ │ reloader     │
   │ /etc/echkeydir/ │ │ (CF / R53 / ...) │ │ SIGHUP nginx │
   └────────┬────────┘ └─────────┬────────┘ └──────┬───────┘
            │                    │                 │
            ▼                    ▼                 ▼
     *.ech PEM files      HTTPS RR ech=        master pid
       (nginx loads)        (DNS pub)
```

Each rotation runs the **R0–R9 state machine** documented in
[`docs/architecture.md`](docs/architecture.md): generate → write
keystore → reload nginx (now serving new+old) → push expanded
ECHConfigList to DNS → settle → converge DNS → grace period →
prune old keys → reload again. Each step is independently
recoverable on the next cycle.

---

## Documentation

| Doc                                                | What's in it                                           |
| -------------------------------------------------- | ------------------------------------------------------ |
| [`docs/install.md`](docs/install.md)               | 10-minute install walkthrough                          |
| [`docs/architecture.md`](docs/architecture.md)     | Design, scope boundaries, R0–R9 state machine          |
| [`docs/provider-guide.md`](docs/provider-guide.md) | How to write a new DNS provider                        |
| [`SECURITY.md`](SECURITY.md)                       | Threat model, vulnerability reporting, hardening tips  |
| [`CONTRIBUTING.md`](CONTRIBUTING.md)               | Dev setup, project boundaries, PR conventions          |
| [`examples/config.example.yaml`](examples/config.example.yaml) | Every config knob, fully commented         |
| [`examples/systemd/ech-keymgr.service`](examples/systemd/ech-keymgr.service) | Hardened systemd unit              |

---

## Why pure Go (zero CGO)?

- One static binary per architecture — `scp` and run; no `apt install libssl-dev`
- Trivial cross-compilation: `GOOS=linux GOARCH=arm64 go build` just works
- Uniform behavior across distros and kernel versions
- ~14 MB binary contains all four DNS providers, the full ECH key lifecycle, and cobra CLI
- Survives the upcoming OpenSSL ABI shake-out (4.x is still bedding in)
- HPKE comes from [`github.com/cloudflare/circl`][circl], an audited pure-Go cryptography library

[circl]: https://github.com/cloudflare/circl

---

## Compatibility

| Component                          | Status                                            |
| ---------------------------------- | ------------------------------------------------- |
| TLS server: **NGINX 1.29.4+**      | ✅ tested target (uses `ssl_echkeydir`)           |
| TLS server: Apache + ECH branch    | ✅ same `.ech` PEM format works                   |
| TLS server: lighttpd + ECH branch  | ✅ same `.ech` PEM format works                   |
| OpenSSL                            | ✅ 4.0+ (we don't link it; nginx does)            |
| Go toolchain                       | ✅ 1.22 → 1.25 (CI matrix)                        |
| OS                                 | ✅ Linux, macOS, Windows, FreeBSD                 |
| Architectures                      | ✅ amd64, arm64, armv7                            |

---

## License

Apache License 2.0 — see [LICENSE](LICENSE).

You can use, modify, and redistribute (including commercially and
in proprietary derivatives) provided you keep the copyright notice,
include the LICENSE in distributions, and respect the patent and
trademark clauses. The Apache 2.0 disclaimer of warranty and
limitation of liability apply — this project provides no guarantees,
and contributors are not liable for damages arising from its use.
