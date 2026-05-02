//go:build unix

package reloader

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestSignal_SendsSIGUSR1ToOurselves drives a real signal-delivery
// round-trip: write our own PID to a pidfile, ask the reloader to
// signal SIGUSR1 to that pidfile, and assert we receive it. Validates
// the entire signal path including readPID, the os.Process lookup,
// and the kernel's signal queueing.
//
// SIGUSR1 does not exist on Windows, so this whole file is unix-only.
func TestSignal_SendsSIGUSR1ToOurselves(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{Strategy: StrategySignal, PIDFile: pidFile, Signal: "SIGUSR1"})
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	defer signal.Stop(ch)

	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		// got it
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive SIGUSR1 within 2s")
	}
}

// TestSignal_DefaultIsSIGHUP asserts that omitting the signal: name
// in YAML resolves to SIGHUP — the right answer for nginx and
// every other long-running unix daemon we care about.
func TestSignal_DefaultIsSIGHUP(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600)
	r, err := New(Config{Strategy: StrategySignal, PIDFile: pidFile, Signal: ""})
	if err != nil {
		t.Fatal(err)
	}
	sr := r.(*signalReloader)
	if sr.sig != syscall.SIGHUP {
		t.Errorf("default sig = %v, want SIGHUP", sr.sig)
	}
}

// TestParseSignal_AcceptsBareName_Unix proves that the user can
// write `signal: usr1` (no SIG prefix, lowercase) and get the
// expected POSIX value.
func TestParseSignal_AcceptsBareName_Unix(t *testing.T) {
	got, err := parseSignal("usr1")
	if err != nil {
		t.Fatalf("parse usr1: %v", err)
	}
	if got != syscall.SIGUSR1 {
		t.Errorf("parse usr1 = %v, want SIGUSR1", got)
	}
}
