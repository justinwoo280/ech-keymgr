// Package main is the ech-keymgr command-line entrypoint.
//
// Subcommands (see `ech-keymgr --help`):
//
//	init    Create the initial HTTPS RR for a domain (one-shot).
//	rotate  Run a single rotation cycle (cron-friendly).
//	daemon  Run continuously; rotate on the configured interval.
//	verify  Read DNS, compare to local keystore, print drift.
//	status  Print current keys + DNS parity for every domain.
//	keygen  Generate one HPKE key pair + .ech file (no DNS, no reload).
package main

import (
	"fmt"
	"os"
)

// Version is set at link time using -ldflags="-X main.Version=...".
var Version = "0.0.0-dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// cobra already prints the error itself; ensure non-zero
		// exit even when a subcommand returned a non-cobra error.
		_ = err
		fmt.Fprintln(os.Stderr) // trailing blank line for terminal cleanliness
		os.Exit(1)
	}
}
