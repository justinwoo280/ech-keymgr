# Akamai Edge DNS provider

Talks to the
[Akamai Edge DNS Zone Management API v2](https://techdocs.akamai.com/edge-dns/reference/edge-dns-api)
using the official
[EdgeGrid Go authentication library](https://github.com/akamai/AkamaiOPEN-edgegrid-golang)
for request signing.

We use only the EdgeGrid `pkg/edgegrid` package — not the giant
v13 SDK — so the dependency footprint stays small. Akamai's
EdgeGrid auth scheme is non-trivial (HMAC-SHA256 over a
canonicalised request with body hash, timestamp, nonce, and a
headers-to-sign list); reusing the official signer keeps us
correct on the part that matters.

## Configuration

Two equivalent forms are accepted.

### 1. From an `.edgerc` file (recommended)

```yaml
credentials:
  akamai_main:
    provider:    akamai
    edgerc_path: /home/admin/.edgerc
    section:     default        # optional; default "default"
```

The `.edgerc` file follows the standard Akamai convention:

```ini
[default]
host          = akab-XXXXXXXXXXXXXXXX.luna.akamaiapis.net
client_token  = akab-XXXXXXXXXXXXXXXXXXXXXXXXXX
client_secret = XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
access_token  = akab-XXXXXXXXXXXXXXXXXXXXXXXXXX
```

### 2. Inline credentials

```yaml
credentials:
  akamai_main:
    provider:      akamai
    host:          akab-XXXX.luna.akamaiapis.net
    client_token:  akab-XXXX
    client_secret: XXXXXXXX
    access_token:  akab-XXXX
    account_key:   1-AB2C            # optional, multi-account
```

Combining both forms (e.g. `edgerc_path` plus `host`) is rejected.

## API client permissions

In Akamai Control Center, create an API client with at least:

| Permission                   | Access     |
| ---------------------------- | ---------- |
| DNS Zone Management          | READ-WRITE |

The zone you reference (`zone:` in the domain block) must already
exist in your Akamai contract — ech-keymgr never creates new zones.

## What this provider does

- **GET**: `GET /config-dns/v2/zones/{zone}/names/{fqdn}/types/HTTPS`,
  returns the rrset's `rdata` array verbatim — already in RFC 9460
  presentation form (`1 . alpn="h2" ech="..."`), no translation needed.
- **PUT**: try `PUT` first; on 404 (record not yet present) fall back
  to `POST`. This gives the caller create-or-replace semantics in
  one call. Old rrset is replaced atomically by Edge DNS.
- **DELETE**: `DELETE /config-dns/v2/zones/.../types/HTTPS`. 404 →
  treated as success (idempotent).

## Build tags

```bash
go build ./cmd/ech-keymgr                # all official providers (default)
go build -tags=akamai ./cmd/ech-keymgr   # only Akamai
```

## Notes

- All requests are signed with the EdgeGrid scheme; we never use
  Basic auth or simple bearer tokens.
- The `host` config field is the Akamai-allocated API hostname (the
  one ending in `.akamaiapis.net`), NOT the public DNS host of your
  managed zone.
- The provider doesn't trigger explicit `submit` operations or
  changelist transactions — Edge DNS RRset endpoints commit
  immediately. For workflows that need atomic batch changes across
  many rrsets, use the upstream Akamai SDK directly.

## Maintainers

Officially maintained by the ech-keymgr project.
