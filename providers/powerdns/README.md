# PowerDNS Authoritative DNS provider

This provider talks to the
[PowerDNS Authoritative HTTP API](https://doc.powerdns.com/authoritative/http-api/zone.html).

It is also the **smallest reference** for community PRs: the entire
implementation is ~ 200 lines because PowerDNS already speaks RFC 9460
presentation form natively in its `content` field.

## Configuration

```yaml
domains:
  - record_fqdn: hidden.example.com
    public_name: example.com
    keydir: /etc/echkeydir/hidden.example.com
    rotation:
      interval: 3h
      grace_period: 6h
    dns:
      provider: powerdns
      zone: example.com
      credentials_ref: pdns_main
      ttl: 300

credentials:
  pdns_main:
    provider: powerdns
    api_url: https://pdns.example.com:8081
    api_key: ${PDNS_API_KEY}
    server_id: localhost          # optional; default "localhost"
```

## Server requirements

- PowerDNS Authoritative Server with the API enabled (`api=yes`,
  `api-key=<secret>`, `webserver=yes`).
- The zone you reference (`zone:`) must already exist on the server.
  ech-keymgr never creates new zones.
- The API key must have permission to PATCH the zone.

## What this provider does

- **GET**: fetches `GET /api/v1/servers/<server_id>/zones/<zone>.`,
  filters its `rrsets[]` by `name == <fqdn>.` and `type == "HTTPS"`,
  and returns each enabled record's `content` verbatim (already in
  RFC 9460 presentation form).
- **PUT**: issues a single
  `PATCH /api/v1/servers/<id>/zones/<zone>.` with one rrset using
  `changetype: "REPLACE"`. PowerDNS guarantees this is atomic — there
  is no window where the owner has no HTTPS RR.
- **DELETE**: issues `changetype: "DELETE"`. PowerDNS returns 204 even
  for absent rrsets, so this is idempotent.

## Build tags

```bash
go build ./cmd/ech-keymgr                  # all official providers (default)
go build -tags=powerdns ./cmd/ech-keymgr   # only PowerDNS
```

## Notes

- Owner names are sent with a trailing dot (FQDN), as PowerDNS expects.
- TTL defaults to 300 if you pass 0; PowerDNS will reject TTLs outside
  its server-configured bounds.
- The provider does NOT call `notify` or `axfr-retrieve`; it relies on
  PowerDNS's automatic SOA-EDIT-API to bump serials.

## Maintainers

Officially maintained by the ech-keymgr project.
