package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/justinwoo280/ech-keymgr/internal/config"
	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <record_fqdn>",
		Short: "Create the initial HTTPS RR for a domain (one-shot)",
		Long: `init creates the FIRST HTTPS DNS resource record for a managed domain.

Subsequent rotations use Update semantics — they refuse to create a new
record if none exists, on the principle that creating DNS records is a
rare and operator-supervised event. This subcommand is the explicit
"yes I want a fresh HTTPS RR with my ech= seed" entrypoint.

The created record has the minimum possible shape:
    1 . ech="<base64 of an empty placeholder>"

Run rotate immediately afterwards to populate it with a real HPKE key.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doInit(cmd.Context(), args[0])
		},
	}
}

func doInit(ctx context.Context, fqdn string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	d, err := findDomain(cfg, fqdn)
	if errors.Is(err, errAllDomains) {
		return errors.New("ech-keymgr init: please specify a record_fqdn")
	}
	if err != nil {
		return err
	}
	prov, err := buildProvider(d)
	if err != nil {
		return err
	}
	owner := relName(d.RecordFQDN, d.DNS.Zone)

	// Refuse if a record already exists — the operator should run
	// `rotate` instead.
	if existing, err := prov.GetHTTPSRDATA(ctx, d.DNS.Zone, owner); err == nil && len(existing) > 0 {
		return fmt.Errorf("ech-keymgr init: HTTPS RR already exists at %s; use `rotate` instead", d.RecordFQDN)
	} else if err != nil && !errors.Is(err, dns.ErrRecordNotFound) {
		return err
	}

	// Seed with a 4-byte placeholder ECHConfigList: a uint16 length
	// of 0 is invalid per RFC 9849, so we emit the smallest legal
	// ECHConfigList that decodes — a single zeroed unknown-version
	// entry. The next `rotate` will overwrite this with real keys.
	placeholderList := buildPlaceholderECH()
	rdata := []string{
		fmt.Sprintf(`1 . ech=%q`, base64.StdEncoding.EncodeToString(placeholderList)),
	}
	if err := prov.PutHTTPSRDATA(ctx, d.DNS.Zone, owner, d.DNS.TTL, rdata); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	fmt.Fprintf(os.Stdout, "ech-keymgr: %s: created HTTPS RR with placeholder ech= — now run `ech-keymgr rotate %s`\n",
		d.RecordFQDN, d.RecordFQDN)
	return nil
}

// buildPlaceholderECH returns the smallest legal ECHConfigList wire
// bytes: a uint16 outer length of 4, containing one entry with
// version 0x0000 and an empty body. Valid clients ignore unknown
// versions; the placeholder exists only so subsequent rotate calls
// can use Update semantics.
func buildPlaceholderECH() []byte {
	// outer length 4 || version 0x0000 || body length 0
	return []byte{0x00, 0x04, 0x00, 0x00, 0x00, 0x00}
}

// guard: avoid unused import warning while we keep config imported.
var _ = config.Domain{}
