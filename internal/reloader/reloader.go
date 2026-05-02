package reloader

import (
	"context"
	"errors"
	"fmt"
)

// Reloader triggers one hot-reload of the target web server.
//
// Implementations MUST NOT block longer than the supplied context;
// the rotator typically passes a 30-second deadline so a stuck
// reload doesn't wedge the rotation cycle.
type Reloader interface {
	// Name returns the strategy identifier for diagnostics
	// (e.g. "signal", "exec", "systemd").
	Name() string

	// Reload signals the server to re-read its configuration.
	// Returns nil on apparent success; a non-nil error indicates
	// the reload could not be requested. Note: a nil return does
	// not prove the server actually finished reloading — see
	// package doc.
	Reload(ctx context.Context) error
}

// Config is the YAML-driven shape used to build a Reloader. Only
// the fields relevant to the chosen Strategy are read.
type Config struct {
	Strategy Strategy `yaml:"strategy" json:"strategy"`

	// signal strategy fields
	PIDFile string `yaml:"pid_file" json:"pid_file,omitempty"`
	Signal  string `yaml:"signal"   json:"signal,omitempty"` // e.g. "SIGHUP"

	// exec strategy fields
	Command string   `yaml:"command" json:"command,omitempty"`
	Args    []string `yaml:"args"    json:"args,omitempty"`

	// systemd strategy fields
	Unit string `yaml:"unit" json:"unit,omitempty"`
}

// Strategy enumerates how to talk to the server.
type Strategy string

const (
	StrategySignal  Strategy = "signal"
	StrategyExec    Strategy = "exec"
	StrategySystemd Strategy = "systemd"
)

// IsValid reports whether s is a recognised strategy.
func (s Strategy) IsValid() bool {
	switch s {
	case StrategySignal, StrategyExec, StrategySystemd:
		return true
	}
	return false
}

// New constructs a Reloader from cfg. Returns an error for an
// unknown strategy or for a strategy whose required fields are
// missing.
func New(cfg Config) (Reloader, error) {
	if !cfg.Strategy.IsValid() {
		return nil, fmt.Errorf("reloader: unknown strategy %q (want signal|exec|systemd)", cfg.Strategy)
	}
	switch cfg.Strategy {
	case StrategySignal:
		return newSignalReloader(cfg)
	case StrategyExec:
		return newExecReloader(cfg)
	case StrategySystemd:
		return newSystemdReloader(cfg)
	}
	return nil, errors.New("reloader: unreachable")
}

// Noop is a Reloader that does nothing successfully. It exists so
// that integration tests and dry-run modes have a Reloader to plug
// in without faking signals or spawning processes.
type Noop struct{}

// Name implements Reloader.
func (Noop) Name() string { return "noop" }

// Reload implements Reloader.
func (Noop) Reload(_ context.Context) error { return nil }
