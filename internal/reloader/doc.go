// Package reloader triggers a hot-reload of the user's web server so
// it picks up newly-added or removed .ech files in its
// ssl_echkeydir directory (or equivalent).
//
// We support three strategies, picked by the user in config.yaml:
//
//   1. signal — the default. Send a configurable signal (SIGHUP for
//      nginx; SIGUSR2 for some HAProxy setups) to the PID found in a
//      pid_file. No external commands required, fully cross-platform
//      where signals exist.
//
//   2. exec   — run an arbitrary command (e.g. `nginx -s reload` or
//      a vendor-specific script). Useful when the server author
//      doesn't expose a PID file or when the operator wraps the
//      reload in additional housekeeping.
//
//   3. systemd — `systemctl reload <unit>`. Wraps exec with a
//      well-known invocation; equivalent to exec+well-typed args
//      but more discoverable in YAML.
//
// All three strategies share a common Reloader interface so the
// rotator state machine doesn't care which one is in use.
//
// Out of scope:
//
//   - Verifying that the server actually re-read the new keys: that
//     is observable end-to-end via internal/verify (DNS) and via
//     the operator's own monitoring of $ssl_ech in nginx logs. We
//     do not poll log files here.
//   - Restarting (vs. reloading) the server. This package is for
//     graceful, in-flight-handshake-preserving reloads only.
package reloader
