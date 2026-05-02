// Package akamai implements the ech-keymgr DNS Provider interface
// against the Akamai Edge DNS Zone Management API v2.
//
// We use the official Akamai EdgeGrid Go package
// (github.com/akamai/AkamaiOPEN-edgegrid-golang/v13/pkg/edgegrid) for
// HTTP request signing — that one half of the integration is the
// hardest to get right (HMAC-SHA256 over a canonicalised request
// with body hash, timestamp, nonce, and headers-to-sign list). For
// the rest we use net/http directly, matching the thin-provider
// philosophy used by every other provider in this project.
//
// Edge DNS exposes a per-RRset endpoint:
//
//	GET    /config-dns/v2/zones/{zone}/names/{name}/types/{type}
//	PUT    /config-dns/v2/zones/{zone}/names/{name}/types/{type}
//	POST   /config-dns/v2/zones/{zone}/names/{name}/types/{type}
//	DELETE /config-dns/v2/zones/{zone}/names/{name}/types/{type}
//
// where the JSON body is:
//
//	{"name": "...", "type": "HTTPS", "ttl": 300, "rdata": [
//	    "1 . alpn=\"h2\" ech=\"AEX...\"",
//	]}
//
// The `rdata` array contains the RFC 9460 zone-file presentation
// form verbatim — exactly what our pkg/svcb produces — so no
// translation is needed.
//
// Configuration shape (under credentials.<ref> in config.yaml):
//
// One of these two forms is required:
//
// 1. Inline credentials:
//
//	provider:      akamai
//	host:          akab-...akamaiapis.net
//	client_token:  akab-...
//	client_secret: ...
//	access_token:  akab-...
//	account_key:   1-AB2C            # optional, multi-account
//
// 2. From an .edgerc file:
//
//	provider:    akamai
//	edgerc_path: /home/admin/.edgerc
//	section:     default             # optional; default "default"
//
// API documentation:
//
//	https://techdocs.akamai.com/edge-dns/reference/edge-dns-api
//
// Required API client permissions:
//
//	DNS Zone Management — READ-WRITE
package akamai
