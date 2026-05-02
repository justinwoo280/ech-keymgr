# ech-keymgr — Architecture & Design

This document captures every design decision aligned during the planning
phase of the project. It is normative for contributors: please update it
whenever a behaviour changes.

## 1. Mission statement (what this tool is, and isn't)

`ech-keymgr` automates the lifecycle of **ECH HPKE key material**
(RFC 9849, March 2026) and the corresponding `ECHConfigList` published
in a single DNS HTTPS resource record (RFC 9848 + RFC 9460), in a way
that is safe for in-flight TLS handshakes.

It explicitly **does not** manage TLS certificates, web-server
configuration, A/AAAA records, DNSSEC, or anything related to
`public_name` validity. Those remain the operator's responsibility.

## 2. Standards we track

| RFC | Title | Date |
| --- | --- | --- |
| RFC 9849 | TLS Encrypted Client Hello | March 2026 |
| RFC 9848 | Bootstrapping TLS Encrypted ClientHello with DNS Service Bindings | March 2026 |
| RFC 9460 | Service Binding and Parameter Specification via the DNS (SVCB and HTTPS RRs) | November 2023 |
| RFC 9180 | Hybrid Public Key Encryption (HPKE) | February 2022 |
| draft-farrell-tls-pemesni | PEM file format for ECH (DEfO) | individual draft (de-facto standard for ECH-enabled servers) |

We do **not** target older drafts (e.g. `draft-ietf-tls-esni-XX`).

## 3. Responsibility boundaries

### 3.1 Hard responsibilities — failures are errors

| # | Responsibility |
| --- | --- |
| 1 | Generate HPKE key pairs (X25519 KEM, HKDF-SHA256, AES-128-GCM and ChaCha20-Poly1305 AEADs) |
| 2 | Encode `ECHConfig` per RFC 9849 §4 |
| 3 | Maintain an `ECHConfigList` containing N keys during the grace overlap |
| 4 | Atomically write `.ech` PEM files (PKCS#8 private key + ECHCONFIG block, per draft-farrell-tls-pemesni) to a key directory |
| 5 | Trigger server hot-reload (SIGHUP / `nginx -s reload` / `systemctl`) |
| 6 | Patch the `ech=` SvcParam on **exactly one** HTTPS RR via a DNS provider API |
| 7 | Schedule rotation (default 3h interval, 6h grace) |

### 3.2 Soft responsibilities — failures are warnings only

| # | Responsibility |
| --- | --- |
| 1 | Verify the published HTTPS RR matches what we just pushed (TYPE65 query) |
| 2 | Verify the local keystore is consistent with the DNS-published `ECHConfigList` |

These produce structured WARN logs and Prometheus counters but never
roll back, fail, or block a rotation.

### 3.3 Out of scope — the operator handles these

- `public_name` validity (DNS, certificate, matching server block)
- X.509 certificate lifecycle (use ACME tools)
- A/AAAA records, other SvcParams (`alpn`, `port`, `ipv4hint`, …)
- Split-mode CFS / backend IP routing
- DNSSEC signing
- End-to-end TLS-with-ECH handshake testing

## 4. ECH deployment topologies, and why we touch one RR

| Topology | DNS records involved | Does this tool manage them? |
| --- | --- | --- |
| **Shared mode** (NGINX 1.29.4 default) | 1 HTTPS RR carrying `ech=` | ✅ Yes, **this is our target** |
| Split mode | HTTPS RR with `ech=`, plus A/AAAA pointed at the client-facing server | ❌ Out of scope |
| AliasMode HTTPS RR (priority 0) → CDN | 1 alias record + CDN-side ServiceMode record | ❌ Out of scope |
| Multi-port / per-ALPN ServiceMode RRs (`_4443._https.foo`) | Multiple RRs in same RRset | ⚠️ Possible v2 |

The `public_name` field that the client uses for the outer SNI is **part
of the ECHConfig binary structure**, not a separate DNS record. We embed
it into every ECHConfig we generate, exactly as the operator declared
it in `config.yaml`.

## 5. The "no-downtime" rotation state machine

```
state R0:  DNS = [K_n]            files = [K_n, K_{n-1}, ...]   # steady
   │
   │ tick (every rotation_interval)
   ▼
state R1:  generate K_{n+1}                                       # in-memory
state R2:  write key_{n+1}.ech (atomic tmp+rename)                # files += K_{n+1}
state R3:  reload server                                          # all keys loaded
state R4:  push DNS = base64([K_{n+1}, K_n])                      # DNS list grows
state R5:  wait max(TTL, settle_delay)                            # caches age out
state R6:  push DNS = base64([K_{n+1}])                           # DNS list shrinks
state R7:  wait grace_period                                      # stragglers finish
state R8:  delete oldest .ech file beyond keep_count              # files prune
state R9:  reload server                                          # back to steady
   │
   ▼
state R0' (with K_{n+1} as the new K_n)
```

Invariants:

- **A1.** During R3..R8, the server's keydir contains every key that
  appears in any DNS response any client could have cached.
- **A2.** During R4..R6, the DNS list is a non-empty subset of the keydir.
- **A3.** Files in the keydir are written atomically (`tmp` + rename),
  so no concurrent server reload can read a partial PEM.

If R5 or R6 fail, the previous state is preserved; the next tick will
retry. Failure during a partial DNS update never lets `ech=` go missing
(we always submit a full, valid `ECHConfigList`).

## 6. File layout

```
/etc/echkeydir/<record_fqdn>/
  20260502T070000-a3f1.ech       # newest, "current" (first in DNS list)
  20260502T040000-9c2e.ech       # one rotation ago
  20260502T010000-1b8d.ech       # two rotations ago, in DNS grace
  .meta.json                     # per-key metadata (config_id, created, in_dns)
```

`.ech` filename format: `<UTC RFC3339 basic>-<config_id_hex>.ech`.
`config_id` collisions are avoided by random rejection sampling
(RFC 9849 §4.1 recommended approach).

`.meta.json` schema (versioned):

```json
{
  "version": 1,
  "record_fqdn": "hidden.example.com",
  "public_name": "example.com",
  "keys": [
    {
      "filename": "20260502T070000-a3f1.ech",
      "config_id": 163,
      "created_at": "2026-05-02T07:00:00Z",
      "in_dns_since": "2026-05-02T07:00:31Z",
      "scheduled_drop_at": "2026-05-02T13:00:00Z"
    }
  ]
}
```

## 7. Configuration model

```yaml
domains:
  - record_fqdn: hidden.example.com
    public_name: example.com               # opaque to us, embedded into ECHConfig
    keydir: /etc/echkeydir/hidden.example.com
    rotation:
      interval: 3h
      grace_period: 6h
      keep_count: 3                        # max .ech files retained
    cipher_suites:
      - aes128gcm-sha256
      - chacha20poly1305-sha256
    reload:
      strategy: signal                     # signal | exec | systemd
      pid_file: /run/nginx.pid
      signal: SIGHUP
    dns:
      provider: cloudflare
      zone: example.com
      credentials_ref: cf_main
      ttl: 300

verification:
  enabled: true
  delay_after_push: 30s
  resolvers: [1.1.1.1:53, 8.8.8.8:53]
  on_mismatch: warn

credentials:
  cf_main:
    provider: cloudflare
    api_token: ${CF_API_TOKEN}
```

## 8. DNS Provider model (acme.sh style)

The DNS provider plug-in is the smallest possible interface — three
methods. New providers are contributed as PRs and live under
`providers/community/<name>/`. Officially maintained providers live
under `providers/<name>/` and are covered by the project's CI.

```go
type Provider interface {
    Name() string
    UpsertHTTPSRecord(ctx, UpsertRequest) error
    GetHTTPSRecord(ctx, zone, name string) (*HTTPSRecord, error)
    DeleteHTTPSRecord(ctx, zone, name string) error
}
```

Important: `UpsertHTTPSRecord` must **preserve every existing SvcParam**
on the record other than `ech=`. We never own `alpn`, `ipv4hint`, etc.
The operator owns those.

If the HTTPS RR does not exist:

- `init` subcommand: explicitly create a minimal RR `<priority> . ech="..."`.
- `rotate`/`daemon`: fail-fast and tell the operator to run `init`.

Officially supported, MVP order:
**Cloudflare → Route 53 → Akamai Edge DNS → PowerDNS**.

## 9. Cryptographic choices

- **KEM:**    `DHKEM(X25519, HKDF-SHA256)` (HPKE id `0x0020`)
- **KDF:**    `HKDF-SHA256` (HPKE id `0x0001`)
- **AEADs:**  `AES-128-GCM` (`0x0001`) and `ChaCha20Poly1305` (`0x0003`),
              both advertised by default.

All implemented via `github.com/cloudflare/circl/hpke` — pure Go, no
CGO, no system OpenSSL/BoringSSL dependency.

ECHConfig binary encoding uses `golang.org/x/crypto/cryptobyte`
(TLS Presentation Language).

## 10. Module layout

```
ech-keymgr/
  cmd/ech-keymgr/        # CLI entrypoint (cobra)
  internal/
    config/              # YAML loading & validation
    echconfig/           # RFC 9849 §4 encode/decode
    hpke/                # CIRCL-based key generation
    pemfile/             # draft-farrell-tls-pemesni read/write
    keystore/            # .ech directory + .meta.json + atomic writes
    rotator/             # state machine described in §5
    reloader/            # signal / exec / systemd
    verify/              # soft DNS reconciliation (WARN only)
    log/                 # structured logging (slog wrapper)
  pkg/
    dns/                 # Provider interface + registry (public; community plugs in here)
    echconfig/           # re-export of internal/echconfig for external consumers
  providers/
    cloudflare/          # official
    route53/             # official
    akamai/              # official
    powerdns/            # official
    community/           # community PRs land here, each with its own README & maintainers
  docs/
  examples/
```
