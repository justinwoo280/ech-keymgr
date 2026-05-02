// Package dns defines the minimal interface every DNS-hosting backend
// must implement so ech-keymgr can publish ECH ECHConfigLists into a
// single HTTPS resource record (RFC 9460 / RFC 9848).
//
// # Thin-provider design
//
// Providers DO NOT parse or build SVCB SvcParams. They only read and
// write a list of opaque RDATA strings in the canonical RFC 9460
// presentation form, e.g.
//
//	`1 . alpn="h2,http/1.1" ipv4hint="1.2.3.4" ech="AEX+DQBB..."`
//
// All ECH-aware merging (preserving alpn / ipv4hint / etc. while
// only swapping the ech= SvcParam) happens once, in pkg/svcb,
// driven by ech-keymgr's core. This keeps community PRs tiny — a
// new provider is roughly two HTTP calls.
//
// See docs/provider-guide.md for the full implementation contract
// and PR checklist.
package dns

import (
	"context"
	"errors"
)

// ErrRecordNotFound is returned by GetHTTPSRDATA when no HTTPS RR
// exists at the queried owner name. Callers (the rotate / daemon
// path) surface this to the operator with a hint to run
// `ech-keymgr init`. New records are created only via that explicit
// subcommand path.
var ErrRecordNotFound = errors.New("dns: HTTPS resource record not found")

// Provider is the contract for a DNS hosting backend. The interface
// is intentionally minimal so that adding a new provider in a
// community PR is straightforward: implement two reads/writes
// against the vendor's API and self-register from init().
//
// Implementations MUST be safe for concurrent use by multiple
// goroutines.
type Provider interface {
	// Name returns a stable, lowercase identifier (e.g. "cloudflare").
	// Operators put this value in `dns.provider` in config.yaml.
	Name() string

	// GetHTTPSRDATA returns every HTTPS resource record at owner
	// `name` within `zone`, encoded in the RFC 9460 zone-file
	// presentation form, one entry per RR in the RRset.
	//
	// The string format MUST be:
	//
	//   "<SvcPriority> <TargetName> [<key>=<quoted-or-bare-value> ...]"
	//
	// Examples:
	//   `0 cdn.example.net.`                               (AliasMode)
	//   `1 . alpn="h2,h3" ech="AEX+DQBB..."`               (ServiceMode)
	//
	// Returns ErrRecordNotFound when no HTTPS RR exists at that
	// owner. Returns an empty slice (NOT an error) only if the
	// vendor API legitimately reports zero entries while still
	// confirming the owner exists; nearly all providers should treat
	// "no records" as ErrRecordNotFound.
	//
	// `name` is the owner relative to `zone` (use "@" for apex);
	// callers will not pass fully-qualified absolute names.
	GetHTTPSRDATA(ctx context.Context, zone, name string) ([]string, error)

	// PutHTTPSRDATA writes the given list of RFC 9460 presentation-
	// form RDATA strings as the COMPLETE RRset for owner `name`
	// within `zone`. Any pre-existing HTTPS records at this owner
	// are replaced.
	//
	// `ttl` is the TTL in seconds. Providers MAY clamp it to their
	// minimum; they MUST NOT silently drop the value.
	//
	// The slice ordering is informational only; DNS does not
	// guarantee preserve ordering for an RRset.
	PutHTTPSRDATA(ctx context.Context, zone, name string, ttl uint32, rdata []string) error

	// DeleteHTTPSRDATA removes every HTTPS record at owner `name`
	// within `zone`. Used by the offboarding / `uninstall` workflow
	// only; never by the rotation hot-path.
	//
	// Deleting a non-existent record MUST be a no-op (return nil),
	// not an error.
	DeleteHTTPSRDATA(ctx context.Context, zone, name string) error
}
