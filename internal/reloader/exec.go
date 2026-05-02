package reloader

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// execReloader runs an arbitrary command. The command's exit status
// is propagated as the Reload return value: a non-zero exit yields
// a non-nil error containing stderr (truncated).
type execReloader struct {
	cmd  string
	args []string
}

func newExecReloader(cfg Config) (Reloader, error) {
	if cfg.Command == "" {
		return nil, errors.New("reloader: exec strategy requires command")
	}
	return &execReloader{cmd: cfg.Command, args: append([]string(nil), cfg.Args...)}, nil
}

// Name implements Reloader.
func (e *execReloader) Name() string { return "exec" }

// Reload implements Reloader. ctx.Deadline() bounds the runtime; the
// child is killed when the context is canceled.
func (e *execReloader) Reload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := exec.CommandContext(ctx, e.cmd, e.args...)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reloader: %s %v: %w (output: %s)",
			e.cmd, e.args, err, truncate(out, 512))
	}
	return nil
}

// truncate returns at most n bytes of b, suffixed with "…" if cut.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
