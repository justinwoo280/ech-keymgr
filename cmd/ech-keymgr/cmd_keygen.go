package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/justinwoo280/ech-keymgr/internal/echconfig"
	"github.com/justinwoo280/ech-keymgr/internal/hpke"
	"github.com/justinwoo280/ech-keymgr/internal/pemfile"
)

func newKeygenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keygen <record_fqdn>",
		Short: "Generate one HPKE key pair + .ech file (no DNS, no reload)",
		Long: `keygen creates a fresh HPKE key pair and writes a .ech PEM file
into the keystore directory for the named domain. It does NOT touch
DNS and does NOT reload the web server — exactly the right tool for:

  - Bootstrapping a fresh deployment offline (combine with init).
  - Generating a key by hand for incident-response inspection.
  - Pre-staging a key before a maintenance window.

The new key is added in StateCurrent; any existing Current is
demoted to Previous, just like a normal rotation.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doKeygen(cmd.Context(), args[0])
		},
	}
}

func doKeygen(_ context.Context, fqdn string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	d, err := findDomain(cfg, fqdn)
	if errors.Is(err, errAllDomains) {
		return errors.New("ech-keymgr keygen: please specify a record_fqdn")
	}
	if err != nil {
		return err
	}
	store, err := openStore(d)
	if err != nil {
		return err
	}

	kp, err := hpke.GenerateKeyPair(echconfig.KEMX25519HKDFSHA256)
	if err != nil {
		return err
	}

	// Pick a config_id not already used by the keystore.
	used := map[uint8]bool{}
	for _, e := range store.List() {
		used[e.ConfigID] = true
	}
	var cfgID uint8
	for v := 0; v <= 255; v++ {
		if !used[uint8(v)] {
			cfgID = uint8(v)
			break
		}
	}
	if used[cfgID] {
		return errors.New("ech-keymgr keygen: all 256 config_ids in use")
	}

	c := echconfig.Config{
		ConfigID:          cfgID,
		KEMID:             echconfig.KEMX25519HKDFSHA256,
		PublicKey:         kp.PublicKey,
		CipherSuites:      defaultCipherSuites(),
		MaximumNameLength: 64,
		PublicName:        []byte(d.PublicName),
	}
	listBytes, err := echconfig.MarshalList(&echconfig.List{Configs: []echconfig.Config{c}})
	if err != nil {
		return err
	}
	pemBytes, err := pemfile.Marshal(&pemfile.File{KeyPair: kp, ConfigListBytes: listBytes})
	if err != nil {
		return err
	}
	entry, err := store.Add(pemBytes, cfgID)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "ech-keymgr: %s: wrote %s (config_id=0x%02x)\n",
		d.RecordFQDN, entry.Filename, entry.ConfigID)
	return nil
}

func defaultCipherSuites() []echconfig.CipherSuite {
	return []echconfig.CipherSuite{
		{KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADAES128GCM},
		{KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADChaCha20Poly1305},
	}
}
