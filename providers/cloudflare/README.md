# Cloudflare DNS provider

This is the **reference implementation** for an ech-keymgr DNS provider.
Use it as a template when contributing support for a new DNS hosting
service. See [`docs/provider-guide.md`](../../docs/provider-guide.md)
for the contract every provider must honour.

## Configuration

In your `config.yaml`:

```yaml
domains:
  - record_fqdn: hidden.example.com
    public_name: example.com
    keydir: /etc/echkeydir/hidden.example.com
    rotation:
      interval: 3h
      grace_period: 6h
    dns:
      provider: cloudflare
      zone: example.com
      credentials_ref: cf_main
      ttl: 300

credentials:
  cf_main:
    provider: cloudflare
    api_token: ${CF_API_TOKEN}
    # api_base: https://api.cloudflare.com/client/v4   # optional
```

## API token permissions

Create a scoped token at https://dash.cloudflare.com/profile/api-tokens
with these permissions:

| Permission           | Resource                           |
| -------------------- | ---------------------------------- |
| `Zone:Zone:Read`     | All zones (or specific ones)       |
| `Zone:DNS:Edit`      | Specific zone(s) you'll manage     |

The `Zone:Read` permission is required so we can resolve a zone name
(`example.com`) into a Cloudflare zone_id at runtime. If you scope the
token narrowly, list every zone you intend to manage in the resource
filter.

## What this provider does

- **GET**: lists `HTTPS` records at the given owner via
  `GET /zones/{id}/dns_records?type=HTTPS&name={fqdn}`,
  reassembles each into RFC 9460 presentation form
  (`<priority> <target> <SvcParams>`).
- **PUT**: creates the new HTTPS record(s) first, then deletes the
  previously-existing ones. This ordering means that if a network
  hiccup interrupts the operation, the old `ech=` value still resolves
  — clients never see an empty `ech=` response.
- **DELETE**: removes every HTTPS RR at the owner. Idempotent.

## Build tags

This provider is compiled in by default (under the `all` build tag):

```bash
go build ./cmd/ech-keymgr                    # all official providers
go build -tags=cloudflare ./cmd/ech-keymgr   # only Cloudflare
```

## Limitations / notes

- `api_token` only — Global Key (`X-Auth-Email + X-Auth-Key`) is
  intentionally unsupported. Tokens are safer and Cloudflare
  recommends them.
- TTL must satisfy Cloudflare's bounds (60–86400 for non-Enterprise;
  Enterprise can go down to 30). The provider passes the TTL through
  unchanged; the Cloudflare API will reject values outside its range.
- The provider caches `zone_name → zone_id` in process memory; restart
  the daemon to pick up renamed zones.

## Maintainers

Officially maintained by the ech-keymgr project.
