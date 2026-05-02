package config

import (
	"time"
)

// Config is the parsed and validated form of the YAML file.
//
// After Load returns, every Domain has Domain.DNS.Credentials
// resolved (the bare credentials_ref key has been replaced by a
// pointer into the Credentials map), so consumers don't need to
// re-look anything up.
type Config struct {
	Domains      []Domain               `yaml:"domains"`
	Verification Verification           `yaml:"verification"`
	Credentials  map[string]*Credential `yaml:"credentials"`
}

// Domain is one managed (record_fqdn, public_name) pair.
type Domain struct {
	RecordFQDN   string    `yaml:"record_fqdn"`
	PublicName   string    `yaml:"public_name"`
	Keydir       string    `yaml:"keydir"`
	Rotation     Rotation  `yaml:"rotation"`
	CipherSuites []string  `yaml:"cipher_suites"`
	Reload       ReloadCfg `yaml:"reload"`
	DNS          DNSCfg    `yaml:"dns"`
}

// Rotation governs the schedule and grace policy.
type Rotation struct {
	Interval    time.Duration `yaml:"interval"`
	GracePeriod time.Duration `yaml:"grace_period"`
	KeepCount   int           `yaml:"keep_count"`
}

// ReloadCfg is the YAML mirror of internal/reloader.Config.
type ReloadCfg struct {
	Strategy string   `yaml:"strategy"`
	PIDFile  string   `yaml:"pid_file"`
	Signal   string   `yaml:"signal"`
	Command  string   `yaml:"command"`
	Args     []string `yaml:"args"`
	Unit     string   `yaml:"unit"`
}

// DNSCfg ties a Domain to a credentials_ref + zone + TTL.
//
// `Credentials` is populated by Load() after cross-referencing
// `CredentialsRef` against the top-level Credentials map.
type DNSCfg struct {
	Provider       string      `yaml:"provider"`
	Zone           string      `yaml:"zone"`
	CredentialsRef string      `yaml:"credentials_ref"`
	TTL            uint32      `yaml:"ttl"`
	Credentials    *Credential `yaml:"-"` // resolved by Load
}

// Verification controls soft DNS reconciliation.
type Verification struct {
	Enabled        bool          `yaml:"enabled"`
	DelayAfterPush time.Duration `yaml:"delay_after_push"`
	Resolvers      []string      `yaml:"resolvers"`
	OnMismatch     string        `yaml:"on_mismatch"` // "warn" | "error"
}

// Credential is a free-form bag of provider-specific fields.
//
// The Provider field is mandatory and must match Domain.DNS.Provider
// for any domain referencing this credential. The remaining keys
// are passed through to the provider's Factory unmodified (so
// adding a new credential field for a new provider needs no change
// in this package).
type Credential struct {
	Provider string `yaml:"provider"`
	// Extra holds every other field present in the YAML for this
	// credential. It is the map the provider's Factory consumes.
	Extra map[string]any `yaml:",inline"`
}

// Defaults applied if a YAML field is zero.
const (
	DefaultRotationInterval = 3 * time.Hour
	DefaultGracePeriod      = 6 * time.Hour
	DefaultKeepCount        = 3
	DefaultDNSTTL           = uint32(300)
	DefaultVerifyDelay      = 30 * time.Second
	DefaultReloadStrategy   = "signal"
	DefaultReloadSignal     = "SIGHUP"
)
