// Package powerdns implements the ech-keymgr DNS Provider interface
// against the PowerDNS Authoritative Server HTTP API.
//
// PowerDNS is a strong test of the Provider abstraction because its
// API model is the polar opposite of cloud DNS providers like
// Cloudflare:
//
//   - PATCH /zones/<id> mutates an entire RRset in one atomic
//     transaction, not record-by-record.
//   - record `content` is the RFC 9460 presentation form verbatim,
//     not a structured object — perfectly matched to our thin
//     Provider interface (no JSON-to-presentation marshaling).
//   - Authentication is X-API-Key, not Bearer.
//   - Owner names are FQDNs with a trailing dot.
//
// Configuration shape (under credentials.<ref> in config.yaml):
//
//	provider:    powerdns
//	api_url:     https://pdns.example.com:8081
//	api_key:     ${PDNS_API_KEY}
//	server_id:   localhost          # optional; default "localhost"
//
// PowerDNS API documentation:
//   https://doc.powerdns.com/authoritative/http-api/zone.html
package powerdns
