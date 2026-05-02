// Package cloudflare implements the ech-keymgr DNS Provider interface
// against the Cloudflare DNS API v4.
//
// This provider is the REFERENCE implementation for community
// contributors: it is intentionally compact (~ 250 lines + tests),
// uses only the Go standard library plus pkg/dns and pkg/svcb, and
// demonstrates every contract a new provider must honor:
//
//   - Self-registration in init()
//   - Build-tag gating ("cloudflare" || "all")
//   - GET → translate to RFC 9460 presentation form
//   - PUT → replace the entire RRset (no SvcParam merging here;
//     ech-keymgr core already did that via pkg/svcb)
//   - Returns dns.ErrRecordNotFound when the HTTPS RRset is empty
//   - Delete is idempotent (missing record → nil error)
//
// Cloudflare API documentation:
//
//	https://developers.cloudflare.com/api/operations/dns-records-for-a-zone-list-dns-records
//
// Configuration shape (under credentials.<ref> in config.yaml):
//
//	provider:  cloudflare
//	api_token: ${CF_API_TOKEN}    # required; Bearer token
//	api_base:  https://api.cloudflare.com/client/v4   # optional; default
//
// API token permissions required:
//   - Zone:DNS:Edit  (scoped to the zones you manage)
//   - Zone:Zone:Read (to resolve zone name → zone_id)
package cloudflare
