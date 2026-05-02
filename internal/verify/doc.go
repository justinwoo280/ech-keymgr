// Package verify performs SOFT reconciliation between what
// ech-keymgr believes about a managed domain (its keystore) and
// what the world actually sees (the published HTTPS RR + its
// `ech=` ECHConfigList).
//
// The whole point of this package is to be informational. Every
// finding it produces is downgraded to a structured warning;
// nothing here ever returns an error that the rotator or daemon
// must abort on. See docs/architecture.md §3.2.
//
// What we DO check:
//
//   - The HTTPS RR exists at the configured owner.
//   - The `ech=` SvcParam is present and base64-decodable.
//   - The decoded ECHConfigList parses per RFC 9849.
//   - Every config_id in the published list is also present (with a
//     usable .ech file) in the keystore.
//   - Every keystore entry in StateCurrent or StatePrevious appears
//     in the published list. (Grace entries are NOT expected in DNS.)
//
// What we explicitly DO NOT check (per the architecture boundary):
//
//   - The public_name field of any ECHConfig: that's the operator's
//     concern.
//   - That public_name resolves, has a certificate, or is served by
//     a matching server block.
//   - End-to-end TLS-with-ECH handshakes.
//   - DNSSEC validity.
//
// We can read DNS via two backends:
//
//   - The configured DNS provider (the same one the rotator
//     publishes through). This bypasses recursive caches and
//     reflects the authoritative state.
//   - A direct DNS query against a list of resolvers (e.g.
//     1.1.1.1:53, 8.8.8.8:53). This reflects what real clients
//     would actually see, including caching effects.
//
// The two backends are exposed as the same Source interface so the
// CLI can offer either or both.
package verify
