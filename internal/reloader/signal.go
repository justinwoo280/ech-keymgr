package reloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// signalReloader sends a UNIX signal to the PID found in a pid_file.
// This is the canonical way nginx (SIGHUP) and many similar daemons
// reload their configuration without dropping in-flight connections.
type signalReloader struct {
	pidFile string
	sig     syscall.Signal
}

func newSignalReloader(cfg Config) (Reloader, error) {
	if cfg.PIDFile == "" {
		return nil, errors.New("reloader: signal strategy requires pid_file")
	}
	sig, err := parseSignal(cfg.Signal)
	if err != nil {
		return nil, err
	}
	return &signalReloader{pidFile: cfg.PIDFile, sig: sig}, nil
}

// Name implements Reloader.
func (s *signalReloader) Name() string { return "signal" }

// Reload reads the pid_file, sends s.sig to the PID, and returns.
//
// We don't honour ctx.Deadline() here because os.Process.Signal is
// non-blocking; if it's slow that's an OS problem we can't paper
// over. We do still respect ctx.Err() at entry so a cancelled
// context skips the work.
func (s *signalReloader) Reload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pid, err := readPID(s.pidFile)
	if err != nil {
		return err
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		// On Unix os.FindProcess never errors, but be defensive
		// for portability.
		return fmt.Errorf("reloader: find pid %d: %w", pid, err)
	}
	if err := proc.Signal(s.sig); err != nil {
		return fmt.Errorf("reloader: signal pid %d (%s): %w", pid, s.sig, err)
	}
	return nil
}

// readPID reads and parses an integer PID from a one-line pid file.
// Trailing whitespace is tolerated.
func readPID(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reloader: read pid_file %q: %w", path, err)
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, fmt.Errorf("reloader: pid_file %q is empty", path)
	}
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("reloader: pid_file %q: parse %q: %w", path, s, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("reloader: pid_file %q: non-positive pid %d", path, pid)
	}
	return pid, nil
}

// parseSignal converts a textual signal name (case-insensitive,
// optional "SIG" prefix) into a syscall.Signal. Empty input
// defaults to SIGHUP, the right answer for nginx.
func parseSignal(name string) (syscall.Signal, error) {
	name = strings.TrimSpace(strings.ToUpper(name))
	if name == "" {
		return syscall.SIGHUP, nil
	}
	if !strings.HasPrefix(name, "SIG") {
		name = "SIG" + name
	}
	switch name {
	case "SIGHUP":
		return syscall.SIGHUP, nil
	case "SIGUSR1":
		return syscall.SIGUSR1, nil
	case "SIGUSR2":
		return syscall.SIGUSR2, nil
	case "SIGTERM":
		// Allow but warn-via-name only; ech-keymgr never wants
		// to terminate the server during rotation, but the
		// option lets users do unusual things on purpose.
		return syscall.SIGTERM, nil
	case "SIGQUIT":
		return syscall.SIGQUIT, nil
	}
	return 0, fmt.Errorf("reloader: unsupported signal %q (want SIGHUP|SIGUSR1|SIGUSR2|SIGTERM|SIGQUIT)", name)
}
