package reloader

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// systemdReloader is a thin convenience wrapper over execReloader
// that hard-codes `systemctl reload <unit>`. Operators can also get
// the same effect with strategy: exec, command: systemctl,
// args: [reload, nginx]; this strategy just makes the YAML clearer.
type systemdReloader struct {
	unit string
}

func newSystemdReloader(cfg Config) (Reloader, error) {
	if cfg.Unit == "" {
		return nil, errors.New("reloader: systemd strategy requires unit")
	}
	return &systemdReloader{unit: cfg.Unit}, nil
}

// Name implements Reloader.
func (s *systemdReloader) Name() string { return "systemd" }

// Reload implements Reloader.
func (s *systemdReloader) Reload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := exec.CommandContext(ctx, "systemctl", "reload", s.unit)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reloader: systemctl reload %s: %w (output: %s)",
			s.unit, err, truncate(out, 512))
	}
	return nil
}
