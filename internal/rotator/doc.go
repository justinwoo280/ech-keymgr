// Package rotator drives the no-downtime ECH key rotation state
// machine described in docs/architecture.md §5.
//
// One Rotator instance manages exactly one (record_fqdn, public_name,
// keystore, dns provider, reloader) tuple. Multiple domains are
// served by separate Rotator instances composed by the daemon.
//
// # State machine
//
//	state R0:  DNS = [K_n]            files = [K_n, K_{n-1}, ...]   # steady
//	   │
//	   │ tick (Rotate)
//	   ▼
//	R1: generate K_{n+1}                              # in-memory only
//	R2: keystore.Add → writes <ts>-<id>.ech atomically;
//	    demotes K_n to Previous                       # files += K_{n+1}
//	R3: reloader.Reload                               # server loads K_{n+1}
//	R4: dns.PutHTTPSRDATA(list=[K_{n+1},K_n,...])     # DNS list grows
//	R5: sleep max(TTL, settle_delay)                  # caches age out
//	R6: dns.PutHTTPSRDATA(list=[K_{n+1}])             # DNS list shrinks
//	R7: keystore.SetState(K_n, Grace, +grace_period)  # mark K_n grace
//	R8: keystore.PruneExpired(now)                    # delete >grace
//	R9: reloader.Reload                               # back to steady
//
// Rotation is INVOKED, not scheduled, by this package: a single
// Rotate(ctx) call performs steps R1..R9 in order. Scheduling lives
// in cmd/ech-keymgr (the daemon mode); this keeps Rotator easy to
// drive from cron / Kubernetes CronJob / unit tests / one-shot CLI.
//
// # Concurrency
//
// Rotate must not be called concurrently for the same Rotator; an
// internal mutex enforces this and returns ErrBusy if a second call
// arrives during the long wait at R5.
//
// # Failure semantics
//
//   - R1 fails → state unchanged.
//   - R2 fails → no .ech file persisted, state unchanged.
//   - R3 fails → file exists, server still on old keys; we propagate
//     and abort. The next Rotate retries from R1 (it will see two
//     keys both marked Current/Previous; it tolerates that).
//   - R4 fails → file exists, server has new key, DNS unchanged. We
//     propagate. The next Rotate's R4 will overwrite.
//   - R6 fails → DNS still has the larger list; functionally fine,
//     just slightly stale. Logged.
//   - R8 fails → grace pruning didn't happen yet; will retry next time.
//
// All errors are wrapped with stage names so logs say "rotator: R4
// PutHTTPSRDATA: <inner error>".
package rotator
