package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/justinwoo280/ech-keymgr/internal/config"
)

func newRotateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate [record_fqdn]",
		Short: "Run a single rotation cycle (R1..R9) for one or all domains",
		Long: `rotate runs exactly one full rotation cycle:

  1. Generate a fresh HPKE key pair.
  2. Write a new .ech file atomically into the keystore.
  3. Reload the web server.
  4. Publish DNS = ECHConfigList(new + previous keys).
  5. Wait settle_delay so caches age out.
  6. Publish DNS = ECHConfigList(new only).
  7. Demote the previous key to grace state.
  8. Prune any grace entries whose deadline has passed.
  9. Reload the web server.

If [record_fqdn] is given, only that one domain is rotated; otherwise
every domain in the config is rotated in series.

Suitable for cron / Kubernetes CronJob.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fqdn := ""
			if len(args) == 1 {
				fqdn = args[0]
			}
			return doRotate(cmd.Context(), fqdn)
		},
	}
}

// doRotate is the real worker.
func doRotate(ctx context.Context, fqdn string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	d, err := findDomain(cfg, fqdn)
	if errors.Is(err, errAllDomains) {
		var firstErr error
		for i := range cfg.Domains {
			one := &cfg.Domains[i]
			if rerr := rotateOne(ctx, one); rerr != nil {
				fmt.Fprintf(os.Stderr, "ech-keymgr: %s: %v\n", one.RecordFQDN, rerr)
				if firstErr == nil {
					firstErr = rerr
				}
			} else {
				fmt.Fprintf(os.Stdout, "ech-keymgr: %s: rotation succeeded\n", one.RecordFQDN)
			}
		}
		return firstErr
	}
	if err != nil {
		return err
	}
	if err := rotateOne(ctx, d); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "ech-keymgr: %s: rotation succeeded\n", d.RecordFQDN)
	return nil
}

// rotateOne wires up the per-domain rotator and runs one cycle.
func rotateOne(ctx context.Context, d *config.Domain) error {
	r, _, _, _, err := buildRotator(d)
	if err != nil {
		return err
	}
	return r.Rotate(ctx)
}
