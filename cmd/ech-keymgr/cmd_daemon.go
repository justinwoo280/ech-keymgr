package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/justinwoo280/ech-keymgr/internal/config"
)

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run continuously, rotating each domain on its configured interval",
		Long: `daemon stays in the foreground and rotates every managed domain on
its configured rotation.interval. SIGINT and SIGTERM trigger a graceful
shutdown that waits for any in-flight rotation to finish.

This is the recommended deployment mode under systemd. For one-shot
cron use cases, prefer the ` + "`rotate`" + ` subcommand.

Each domain runs on its own goroutine so a slow DNS API for one
domain doesn't delay rotations of unrelated domains.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return doDaemon(cmd.Context())
		},
	}
}

func doDaemon(parent context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Domains) == 0 {
		return fmt.Errorf("daemon: no domains configured")
	}

	// Wire SIGINT/SIGTERM to ctx cancellation so a Ctrl-C during
	// the long sleeps inside Rotate exits cleanly.
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	for i := range cfg.Domains {
		d := &cfg.Domains[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			runDomainLoop(ctx, d)
		}()
	}
	fmt.Fprintf(os.Stdout, "ech-keymgr daemon: managing %d domain(s); send SIGINT/SIGTERM to stop\n", len(cfg.Domains))
	wg.Wait()
	fmt.Fprintln(os.Stdout, "ech-keymgr daemon: shutdown complete")
	return nil
}

// runDomainLoop is one domain's main loop. It performs a rotation
// immediately on start (so a fresh deploy doesn't have to wait
// `interval` for its first key) then rotates on every tick.
func runDomainLoop(ctx context.Context, d *config.Domain) {
	interval := d.Rotation.Interval
	if interval <= 0 {
		interval = 3 * time.Hour
	}

	rotate := func() {
		if err := rotateOne(ctx, d); err != nil {
			fmt.Fprintf(os.Stderr, "ech-keymgr daemon: %s: rotation failed: %v\n", d.RecordFQDN, err)
			return
		}
		fmt.Fprintf(os.Stdout, "ech-keymgr daemon: %s: rotation succeeded\n", d.RecordFQDN)
	}

	rotate() // immediate first rotation

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rotate()
		}
	}
}
