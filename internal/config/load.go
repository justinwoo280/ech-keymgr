package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads, env-expands, parses and validates the YAML at path.
// On success it returns a fully-populated *Config in which every
// Domain.DNS.Credentials is the actual struct (not just the key).
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	// ParseAndValidate also calls ExpandEnv internally; avoid double expansion.
	return ParseAndValidate(raw)
}

// ParseAndValidate is Load minus disk I/O — useful for tests.
// Like Load, it env-expands the input before parsing.
func ParseAndValidate(data []byte) (*Config, error) {
	expanded := ExpandEnv(string(data))
	var c Config
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("config: yaml: %w", err)
	}
	if err := c.applyDefaultsAndResolve(); err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// envRefRE matches ${VAR}, $VAR, and ${VAR:-default} forms.
// We deliberately keep this conservative — only ASCII identifiers.
var envRefRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// ExpandEnv substitutes ${VAR}, ${VAR:-default}, and $VAR with the
// process environment values. Unknown variables become the empty
// string (or the default when ${VAR:-default} is used).
//
// We do this on the YAML text BEFORE parsing so the expansion is
// uniform regardless of where in the document the reference lives.
func ExpandEnv(s string) string {
	return envRefRE.ReplaceAllStringFunc(s, func(match string) string {
		groups := envRefRE.FindStringSubmatch(match)
		// groups: [whole, brace_name, brace_default, bare_name]
		if name := groups[1]; name != "" {
			if v, ok := os.LookupEnv(name); ok {
				return v
			}
			return groups[2] // default (possibly "")
		}
		if name := groups[3]; name != "" {
			return os.Getenv(name)
		}
		return match
	})
}

func (c *Config) applyDefaultsAndResolve() error {
	for i := range c.Domains {
		d := &c.Domains[i]
		if d.Rotation.Interval == 0 {
			d.Rotation.Interval = DefaultRotationInterval
		}
		if d.Rotation.GracePeriod == 0 {
			d.Rotation.GracePeriod = DefaultGracePeriod
		}
		if d.Rotation.KeepCount == 0 {
			d.Rotation.KeepCount = DefaultKeepCount
		}
		if d.DNS.TTL == 0 {
			d.DNS.TTL = DefaultDNSTTL
		}
		if d.Reload.Strategy == "" {
			d.Reload.Strategy = DefaultReloadStrategy
		}
		if d.Reload.Strategy == "signal" && d.Reload.Signal == "" {
			d.Reload.Signal = DefaultReloadSignal
		}
		// Resolve credentials_ref → *Credential.
		if d.DNS.CredentialsRef != "" {
			cred, ok := c.Credentials[d.DNS.CredentialsRef]
			if !ok {
				return fmt.Errorf("config: domain %q references unknown credentials_ref %q",
					d.RecordFQDN, d.DNS.CredentialsRef)
			}
			d.DNS.Credentials = cred
		}
	}
	if !c.Verification.Enabled && c.Verification.DelayAfterPush == 0 {
		// If verification is disabled, leave DelayAfterPush at zero.
	}
	if c.Verification.DelayAfterPush == 0 {
		c.Verification.DelayAfterPush = DefaultVerifyDelay
	}
	if c.Verification.OnMismatch == "" {
		c.Verification.OnMismatch = "warn"
	}
	return nil
}

func (c *Config) validate() error {
	if len(c.Domains) == 0 {
		return errors.New("config: no domains configured")
	}
	seen := map[string]bool{}
	for i := range c.Domains {
		d := &c.Domains[i]
		if err := validateDomain(d); err != nil {
			return fmt.Errorf("config: domain[%d]: %w", i, err)
		}
		if seen[d.RecordFQDN] {
			return fmt.Errorf("config: duplicate record_fqdn %q", d.RecordFQDN)
		}
		seen[d.RecordFQDN] = true
	}
	return nil
}

func validateDomain(d *Domain) error {
	if strings.TrimSpace(d.RecordFQDN) == "" {
		return errors.New("record_fqdn is required")
	}
	if strings.TrimSpace(d.PublicName) == "" {
		return errors.New("public_name is required")
	}
	if strings.TrimSpace(d.Keydir) == "" {
		return errors.New("keydir is required")
	}
	if strings.TrimSpace(d.DNS.Provider) == "" {
		return errors.New("dns.provider is required")
	}
	if strings.TrimSpace(d.DNS.Zone) == "" {
		return errors.New("dns.zone is required")
	}
	if d.DNS.CredentialsRef != "" {
		if d.DNS.Credentials == nil {
			return fmt.Errorf("credentials_ref %q did not resolve", d.DNS.CredentialsRef)
		}
		if d.DNS.Credentials.Provider != "" && d.DNS.Credentials.Provider != d.DNS.Provider {
			return fmt.Errorf("credentials.provider=%q does not match dns.provider=%q",
				d.DNS.Credentials.Provider, d.DNS.Provider)
		}
	}
	switch d.Reload.Strategy {
	case "signal":
		if d.Reload.PIDFile == "" {
			return errors.New("reload.pid_file is required for strategy=signal")
		}
	case "exec":
		if d.Reload.Command == "" {
			return errors.New("reload.command is required for strategy=exec")
		}
	case "systemd":
		if d.Reload.Unit == "" {
			return errors.New("reload.unit is required for strategy=systemd")
		}
	default:
		return fmt.Errorf("reload.strategy %q is invalid (want signal|exec|systemd)", d.Reload.Strategy)
	}
	return nil
}
