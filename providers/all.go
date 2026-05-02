// Package providers is a side-effect-only umbrella package that
// imports every officially maintained DNS provider so that their
// init() functions register them with pkg/dns.
//
// Importing this package compiles in every official provider that
// matches the active build tags. The default `all` tag pulls in the
// full set; users can build slim binaries with explicit per-provider
// tags, e.g. `go build -tags=cloudflare`.
//
// Community providers live under providers/community/ and are pulled
// in by a parallel umbrella file there; community providers are not
// imported by default.
package providers

import (
	// Officially maintained providers. Each file is gated by its own
	// build tag (e.g. `cloudflare || all`); the default build tag set
	// includes `all`.
	_ "github.com/justinwoo280/ech-keymgr/providers/akamai"
	_ "github.com/justinwoo280/ech-keymgr/providers/cloudflare"
	_ "github.com/justinwoo280/ech-keymgr/providers/powerdns"
	_ "github.com/justinwoo280/ech-keymgr/providers/route53"

	// Community providers (build tag: `community || all`, plus the
	// per-provider tag). Importing the umbrella keeps registrations
	// together with official providers.
	_ "github.com/justinwoo280/ech-keymgr/providers/community"
)
