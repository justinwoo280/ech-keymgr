package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/justinwoo280/ech-keymgr/internal/config"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print current keys and DNS-publication state for every managed domain",
		Long: `status reads each domain's local keystore and prints, for each
.ech key currently on disk:

  config_id  state      created_at            in_dns_since          drop_at
  ────────   ────────   ───────────────────   ───────────────────   ───────────────────
  0x42       current    2026-05-02T07:00:00Z  2026-05-02T07:00:31Z
  0x9c       grace      2026-05-02T04:00:00Z  -                     2026-05-02T13:00:00Z

It does NOT contact DNS providers; for that, use verify.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return doStatus()
		},
	}
}

func doStatus() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	for i := range cfg.Domains {
		printDomainStatus(&cfg.Domains[i])
		if i+1 < len(cfg.Domains) {
			fmt.Fprintln(os.Stdout)
		}
	}
	return nil
}

func printDomainStatus(d *config.Domain) {
	fmt.Fprintf(os.Stdout, "── %s (public_name=%s) ─ keydir=%s\n",
		d.RecordFQDN, d.PublicName, d.Keydir)

	store, err := openStore(d)
	if err != nil {
		fmt.Fprintf(os.Stdout, "  (could not open keystore: %v)\n", err)
		return
	}
	entries := store.List()
	if len(entries) == 0 {
		fmt.Fprintln(os.Stdout, "  (no keys yet — run `ech-keymgr rotate` to generate the first one)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  config_id\tstate\tcreated_at\tin_dns_since\tdrop_at")
	for _, e := range entries {
		fmt.Fprintf(w, "  0x%02x\t%s\t%s\t%s\t%s\n",
			e.ConfigID,
			e.State,
			fmtTime(e.CreatedAt),
			fmtTime(e.InDNSSince),
			fmtTime(e.ScheduledDropAt),
		)
	}
	_ = w.Flush()
}

// fmtTime renders t in RFC 3339 UTC, or "-" if zero.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
