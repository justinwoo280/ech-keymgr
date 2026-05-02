package rotator

import (
	"context"
	"errors"
	"time"

	"github.com/justinwoo280/ech-keymgr/internal/echconfig"
	"github.com/justinwoo280/ech-keymgr/internal/keystore"
	"github.com/justinwoo280/ech-keymgr/internal/reloader"
	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

// ErrBusy is returned by Rotate when another Rotate call for the
// same Rotator is already in progress.
var ErrBusy = errors.New("rotator: a rotation is already in progress")

// ErrNoCurrent is returned by helper paths that require an existing
// Current key (e.g. the "list = [new, old]" overlap step) when none
// is available. The Rotate path itself is tolerant: if no key exists
// it simply skips the overlap and publishes [K_new] directly.
var ErrNoCurrent = errors.New("rotator: no current key in keystore")

// Config bundles the per-domain configuration the Rotator needs.
//
// All durations have sensible defaults if zero (see DefaultConfig
// constants below); the daemon should hand a fully-resolved Config.
type Config struct {
	// RecordFQDN is the owner name for the HTTPS RR we manage.
	RecordFQDN string

	// PublicName is the value embedded into every ECHConfig we
	// generate. We never validate it (per docs/architecture.md §3.3).
	PublicName string

	// DNSZone is the zone passed to the dns provider (NOT the
	// owner). E.g. record_fqdn="hidden.example.com" + zone="example.com".
	DNSZone string

	// MaxNameLength is encoded into the ECHConfig per RFC 9849 §4.
	// 0 means "use len(PublicName)+padding=64".
	MaxNameLength uint8

	// CipherSuites are advertised in every ECHConfig. If empty,
	// DefaultCipherSuites are used.
	CipherSuites []echconfig.CipherSuite

	// KEMID selects the HPKE KEM. Defaults to X25519+HKDF-SHA256.
	KEMID uint16

	// SettleDelay is how long we wait between R4 (publish list with
	// new+old) and R6 (publish list with new only). Should be ≥
	// max DNS TTL we set, so caches see the bigger list at least
	// once before it shrinks.
	SettleDelay time.Duration

	// GracePeriod is how long a demoted key remains in the
	// keystore (file present, ssl_echkeydir loadable) after it
	// disappears from DNS. Stragglers using stale DNS caches use
	// it to complete handshakes.
	GracePeriod time.Duration

	// DNSTTL is the TTL we set on the HTTPS RR. Provider may clamp.
	DNSTTL uint32

	// ReloadTimeout bounds R3 and R9.
	ReloadTimeout time.Duration

	// Clock is used for all time observations. Default: real clock.
	// Tests inject a fake clock to avoid real sleeps.
	Clock Clock

	// Sleep blocks for the given duration, honouring ctx. Default:
	// time.Sleep with ctx polling. Tests inject a fake to make
	// SettleDelay zero-cost.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Clock abstracts wall-clock time so tests can advance it
// deterministically.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Defaults applied when a Config field is zero.
const (
	DefaultSettleDelay   = 30 * time.Second // covers most DNS TTLs we set
	DefaultGracePeriod   = 6 * time.Hour    // = 2 × default rotation interval
	DefaultDNSTTL        = uint32(300)
	DefaultReloadTimeout = 30 * time.Second
)

// DefaultCipherSuites are advertised when Config.CipherSuites is
// empty. AES-128-GCM and ChaCha20-Poly1305 cover both server
// preferences and constrained clients (mobile / embedded).
var DefaultCipherSuites = []echconfig.CipherSuite{
	{KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADAES128GCM},
	{KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADChaCha20Poly1305},
}

// applyDefaults fills in zero-valued fields with the defaults above.
func (c *Config) applyDefaults() {
	if c.SettleDelay == 0 {
		c.SettleDelay = DefaultSettleDelay
	}
	if c.GracePeriod == 0 {
		c.GracePeriod = DefaultGracePeriod
	}
	if c.DNSTTL == 0 {
		c.DNSTTL = DefaultDNSTTL
	}
	if c.ReloadTimeout == 0 {
		c.ReloadTimeout = DefaultReloadTimeout
	}
	if c.KEMID == 0 {
		c.KEMID = echconfig.KEMX25519HKDFSHA256
	}
	if len(c.CipherSuites) == 0 {
		c.CipherSuites = DefaultCipherSuites
	}
	if c.MaxNameLength == 0 {
		// Heuristic: pad to length 64 unless public_name itself
		// is longer; see RFC 9849 §6.1.3 for why padding matters.
		l := len(c.PublicName)
		if l > 64 {
			c.MaxNameLength = uint8(l)
		} else {
			c.MaxNameLength = 64
		}
	}
	if c.Clock == nil {
		c.Clock = realClock{}
	}
	if c.Sleep == nil {
		c.Sleep = realSleep
	}
}

// realSleep blocks for d, honouring ctx cancellation.
func realSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Deps bundles the pluggable collaborators a Rotator needs.
//
// All four are interfaces (not concrete types), so tests can supply
// fakes (e.g. providers/community/mock for DNS, reloader.Noop for
// the server side, an in-memory keystore wrapper, etc.).
type Deps struct {
	Store    *keystore.Store
	DNS      dns.Provider
	Reloader reloader.Reloader
}

// validate sanity-checks Deps; returns a precise error indicating
// which collaborator is missing.
func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("rotator: Deps.Store is required")
	}
	if d.DNS == nil {
		return errors.New("rotator: Deps.DNS is required")
	}
	if d.Reloader == nil {
		return errors.New("rotator: Deps.Reloader is required")
	}
	return nil
}
