package main

import (
	"fmt"
	"strings"

	"github.com/justinwoo280/ech-keymgr/internal/config"
	"github.com/justinwoo280/ech-keymgr/internal/echconfig"
	"github.com/justinwoo280/ech-keymgr/internal/keystore"
	"github.com/justinwoo280/ech-keymgr/internal/reloader"
	"github.com/justinwoo280/ech-keymgr/internal/rotator"
	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

// loadConfig reads the global --config path and returns the parsed
// Config. We don't cache the result; subcommands typically run once
// per process, and config files are tiny.
func loadConfig() (*config.Config, error) {
	c, err := config.Load(gflags.configPath)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// findDomain returns the *Domain whose RecordFQDN equals fqdn (or
// nil + error if no such domain is configured).
//
// Empty fqdn returns nil with a special error the caller can use to
// trigger "all domains" iteration.
func findDomain(c *config.Config, fqdn string) (*config.Domain, error) {
	if fqdn == "" {
		return nil, errAllDomains
	}
	for i := range c.Domains {
		if strings.EqualFold(c.Domains[i].RecordFQDN, fqdn) {
			return &c.Domains[i], nil
		}
	}
	known := make([]string, 0, len(c.Domains))
	for _, d := range c.Domains {
		known = append(known, d.RecordFQDN)
	}
	return nil, fmt.Errorf("ech-keymgr: domain %q not found in config (known: %s)",
		fqdn, strings.Join(known, ", "))
}

// errAllDomains is returned by findDomain when fqdn == "". It is a
// sentinel, not a real error; subcommands check it and iterate.
var errAllDomains = fmt.Errorf("ech-keymgr: no domain specified — iterate over all")

// buildProvider instantiates the DNS provider for a domain by
// looking up the registered factory and feeding it the credential
// extras.
func buildProvider(d *config.Domain) (dns.Provider, error) {
	if d.DNS.Credentials == nil {
		// Some providers (e.g. mock) accept zero config.
		return dns.Build(d.DNS.Provider, nil)
	}
	return dns.Build(d.DNS.Provider, d.DNS.Credentials.Extra)
}

// buildReloader instantiates the configured reload strategy.
func buildReloader(d *config.Domain) (reloader.Reloader, error) {
	return reloader.New(reloader.Config{
		Strategy: reloader.Strategy(d.Reload.Strategy),
		PIDFile:  d.Reload.PIDFile,
		Signal:   d.Reload.Signal,
		Command:  d.Reload.Command,
		Args:     d.Reload.Args,
		Unit:     d.Reload.Unit,
	})
}

// openStore opens (or initialises) the keystore directory for d.
func openStore(d *config.Domain) (*keystore.Store, error) {
	return keystore.OpenOrInit(d.Keydir, d.RecordFQDN, d.PublicName)
}

// buildRotator wires Config.Domain → rotator.Rotator. The returned
// Rotator is ready to call .Rotate(ctx).
func buildRotator(d *config.Domain) (*rotator.Rotator, *keystore.Store, dns.Provider, reloader.Reloader, error) {
	store, err := openStore(d)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("keystore open: %w", err)
	}
	prov, err := buildProvider(d)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dns provider: %w", err)
	}
	rl, err := buildReloader(d)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("reloader: %w", err)
	}
	r, err := rotator.New(rotator.Config{
		RecordFQDN:  d.RecordFQDN,
		PublicName:  d.PublicName,
		DNSZone:     d.DNS.Zone,
		SettleDelay: 0, // use the default
		GracePeriod: d.Rotation.GracePeriod,
		DNSTTL:      d.DNS.TTL,
		// Cipher suites: translate textual list (cipher_suites in YAML)
		// into the binary HPKE codepoints. Empty falls back to defaults.
		CipherSuites: parseCipherSuites(d.CipherSuites),
	}, rotator.Deps{
		Store:    store,
		DNS:      prov,
		Reloader: rl,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return r, store, prov, rl, nil
}

// parseCipherSuites converts the textual aliases used in config.yaml
// into echconfig.CipherSuite codepoint pairs. Unknown aliases are
// silently dropped — the rotator falls back to its default set when
// the result is empty.
func parseCipherSuites(names []string) []echconfig.CipherSuite {
	var out []echconfig.CipherSuite
	for _, n := range names {
		switch strings.ToLower(strings.TrimSpace(n)) {
		case "aes128gcm-sha256":
			out = append(out, echconfig.CipherSuite{
				KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADAES128GCM,
			})
		case "aes256gcm-sha256":
			out = append(out, echconfig.CipherSuite{
				KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADAES256GCM,
			})
		case "chacha20poly1305-sha256":
			out = append(out, echconfig.CipherSuite{
				KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADChaCha20Poly1305,
			})
		}
	}
	return out
}

// relName converts an absolute FQDN into the owner relative to its
// zone, returning "@" for the apex. Same logic as rotator.relName
// but exposed for the verify / status subcommands.
func relName(fqdn, zone string) string {
	fqdn = strings.TrimSuffix(strings.ToLower(fqdn), ".")
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	if fqdn == zone {
		return "@"
	}
	if strings.HasSuffix(fqdn, "."+zone) {
		return fqdn[:len(fqdn)-len(zone)-1]
	}
	return fqdn
}
