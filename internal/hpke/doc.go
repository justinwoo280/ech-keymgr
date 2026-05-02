// Package hpke is a thin wrapper over github.com/cloudflare/circl/hpke
// that produces ECH HPKE key pairs in the format ech-keymgr needs.
//
// We only generate keys for the KEM(s) ECH currently uses widely:
//
//   - DHKEM(X25519, HKDF-SHA256)  (HPKE codepoint 0x0020)
//
// Other KEMs (P-256, P-384, X-Wing, etc.) are intentionally not
// surfaced here because real-world ECH deployments overwhelmingly
// use X25519 and adding more would expand the test matrix without
// operational benefit. They can be added behind a build tag if there
// is demand.
//
// The wrapper exists so the rest of the project never imports CIRCL
// directly: we may later swap implementations (e.g. add a CGO-free
// FIPS-mode backend) without churning callers.
package hpke
