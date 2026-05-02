package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/justinwoo280/ech-keymgr/internal/config"
	"github.com/justinwoo280/ech-keymgr/internal/verify"
)

func newVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify [record_fqdn]",
		Short: "Soft DNS reconciliation against the local keystore",
		Long: `verify reads the published HTTPS RR for each managed domain via the
DNS provider, decodes its ech= ECHConfigList, and compares it to the
local .ech keystore. Drift is reported as warnings (severity WARN);
nothing here ever modifies state.

Exit status:
  0  no warnings — DNS and keystore are in agreement
  1  one or more warnings (drift detected)

If [record_fqdn] is given, only that one domain is verified; otherwise
every configured domain is checked in series.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fqdn := ""
			if len(args) == 1 {
				fqdn = args[0]
			}
			return doVerify(cmd.Context(), fqdn)
		},
	}
}

func doVerify(ctx context.Context, fqdn string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	d, err := findDomain(cfg, fqdn)
	anyWarn := false
	if errors.Is(err, errAllDomains) {
		for i := range cfg.Domains {
			if w, e := verifyOne(ctx, &cfg.Domains[i]); e != nil {
				return e
			} else if w {
				anyWarn = true
			}
		}
	} else if err != nil {
		return err
	} else {
		w, e := verifyOne(ctx, d)
		if e != nil {
			return e
		}
		anyWarn = w
	}
	if anyWarn {
		// Encourage scripts to detect drift via exit status.
		os.Exit(1)
	}
	return nil
}

// verifyOne runs a single verify pass and prints the report.
// Returns (anyWarning, error).
func verifyOne(ctx context.Context, d *config.Domain) (bool, error) {
	store, err := openStore(d)
	if err != nil {
		return false, err
	}
	prov, err := buildProvider(d)
	if err != nil {
		return false, err
	}
	rep, err := verify.Verify(ctx, verify.Request{
		RecordFQDN: d.RecordFQDN,
		DNSZone:    d.DNS.Zone,
		OwnerRel:   relName(d.RecordFQDN, d.DNS.Zone),
		Source:     verify.ProviderSource{P: prov},
		Store:      store,
	})
	if err != nil {
		return false, fmt.Errorf("%s: %w", d.RecordFQDN, err)
	}
	fmt.Fprint(os.Stdout, rep.String())
	return rep.Warns(), nil
}
