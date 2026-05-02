// Package keystore manages the on-disk directory of .ech key files
// for one ECH-managed domain (the directory pointed to by NGINX's
// ssl_echkeydir directive, or its equivalent in other servers).
//
// Responsibilities:
//
//   - Atomic create / replace of .ech files (tmp + rename, fsync,
//     chmod 0600). A reloading server can never observe a half-written
//     PEM file mid-rotation.
//
//   - Stable file-naming convention encoding rotation timestamp +
//     config_id, so a quick `ls` shows lifecycle order.
//
//   - A small, durable .meta.json index sitting alongside the .ech
//     files so the rotator can answer "which keys are currently in
//     DNS, and which are still inside their grace window?" without
//     re-parsing every PEM on every tick.
//
//   - List + Lookup + Delete operations (used by the rotator state
//     machine in §5 of docs/architecture.md).
//
// Thread-safety: a Store value is safe for concurrent use; mutating
// methods take an internal lock.
//
// Out of scope:
//
//   - Reload semantics: that's internal/reloader's job.
//   - Rotation policy: that's internal/rotator's job.
//   - Generating keys: that's internal/hpke's job.
//
// Filename format:
//
//	<UTC RFC3339 basic>-<config_id_hex2>.ech
//	  e.g. 20260502T070000Z-a3.ech
//
// Lifecycle states (as recorded in .meta.json):
//
//	current  → the latest key; first in DNS, served as preferred
//	previous → in DNS list during the convergence overlap
//	grace    → no longer in DNS, but kept loadable by the server
//	           until grace_period elapses (so stragglers using
//	           stale DNS can still complete an ECH handshake)
package keystore
