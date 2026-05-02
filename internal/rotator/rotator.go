package rotator

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/justinwoo280/ech-keymgr/internal/echconfig"
	"github.com/justinwoo280/ech-keymgr/internal/hpke"
	"github.com/justinwoo280/ech-keymgr/internal/keystore"
	"github.com/justinwoo280/ech-keymgr/internal/pemfile"
	"github.com/justinwoo280/ech-keymgr/pkg/svcb"
)

// Rotator drives the §5 state machine for a single managed domain.
type Rotator struct {
	cfg  Config
	deps Deps

	mu sync.Mutex // serializes Rotate calls
}

// New constructs a Rotator. Config is filled with defaults; Deps must
// be populated.
func New(cfg Config, deps Deps) (*Rotator, error) {
	if cfg.RecordFQDN == "" {
		return nil, fmt.Errorf("rotator: Config.RecordFQDN is required")
	}
	if cfg.PublicName == "" {
		return nil, fmt.Errorf("rotator: Config.PublicName is required")
	}
	if cfg.DNSZone == "" {
		return nil, fmt.Errorf("rotator: Config.DNSZone is required")
	}
	if err := deps.validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return &Rotator{cfg: cfg, deps: deps}, nil
}

// Rotate runs one full R1..R9 cycle.
//
// On any error, the caller should log and continue: ech-keymgr's
// state model is "eventually consistent" — subsequent Rotate calls
// re-derive the desired state from keystore + DNS without needing
// to know which R-step failed last time.
func (r *Rotator) Rotate(ctx context.Context) error {
	if !r.mu.TryLock() {
		return ErrBusy
	}
	defer r.mu.Unlock()

	// R1: generate fresh HPKE key pair
	kp, err := hpke.GenerateKeyPair(r.cfg.KEMID)
	if err != nil {
		return fmt.Errorf("rotator: R1 GenerateKeyPair: %w", err)
	}

	// R1.5: pick a non-colliding config_id
	cfgID, err := r.pickConfigID()
	if err != nil {
		return fmt.Errorf("rotator: R1 pickConfigID: %w", err)
	}

	// Build the new ECHConfig.
	newConfig := echconfig.Config{
		ConfigID:          cfgID,
		KEMID:             r.cfg.KEMID,
		PublicKey:         kp.PublicKey,
		CipherSuites:      r.cfg.CipherSuites,
		MaximumNameLength: r.cfg.MaxNameLength,
		PublicName:        []byte(r.cfg.PublicName),
	}

	// R2: persist the new key as a .ech file (atomic, demotes prev Current).
	pemBytes, err := r.buildPEMBytes(kp, newConfig)
	if err != nil {
		return fmt.Errorf("rotator: R2 build PEM: %w", err)
	}
	newEntry, err := r.deps.Store.Add(pemBytes, cfgID)
	if err != nil {
		return fmt.Errorf("rotator: R2 keystore.Add: %w", err)
	}

	// R3: reload server so it picks up the new .ech file alongside
	// any existing ones. We bound this with the configured timeout
	// so a wedged server doesn't stall the rotation.
	if err := r.reload(ctx); err != nil {
		return fmt.Errorf("rotator: R3 reload: %w", err)
	}

	// R4: publish DNS = ECHConfigList containing the new key first,
	// then every Previous key currently in the store. We
	// deliberately exclude Grace keys: they're no longer advertised.
	overlapList, configsInOverlap, err := r.buildOverlapList(newConfig)
	if err != nil {
		return fmt.Errorf("rotator: R4 build list: %w", err)
	}
	if err := r.publishDNS(ctx, overlapList); err != nil {
		return fmt.Errorf("rotator: R4 publishDNS overlap: %w", err)
	}
	if err := r.markInDNS(configsInOverlap); err != nil {
		return fmt.Errorf("rotator: R4 markInDNS: %w", err)
	}

	// R5: wait for caches to age out. Real clock; tests inject a
	// no-op Sleep to skip the wait.
	if err := r.cfg.Sleep(ctx, r.cfg.SettleDelay); err != nil {
		return fmt.Errorf("rotator: R5 sleep: %w", err)
	}

	// R6: shrink DNS to just the new key.
	soloList, err := r.buildSoloList(newConfig)
	if err != nil {
		return fmt.Errorf("rotator: R6 build list: %w", err)
	}
	if err := r.publishDNS(ctx, soloList); err != nil {
		return fmt.Errorf("rotator: R6 publishDNS solo: %w", err)
	}

	// R7: any key that was Previous is now Grace, with a wall-clock
	// drop deadline of GracePeriod from now.
	dropAt := r.cfg.Clock.Now().Add(r.cfg.GracePeriod)
	for _, e := range r.deps.Store.List() {
		if e.State == keystore.StatePrevious {
			if err := r.deps.Store.SetState(e.ConfigID, keystore.StateGrace, e.InDNSSince, dropAt); err != nil {
				return fmt.Errorf("rotator: R7 SetState grace: %w", err)
			}
		}
	}

	// R8: prune any Grace entries whose deadline already passed.
	if _, err := r.deps.Store.PruneExpired(r.cfg.Clock.Now()); err != nil {
		return fmt.Errorf("rotator: R8 PruneExpired: %w", err)
	}

	// R9: reload again so the server drops the just-pruned files.
	// Even if no files were pruned this cycle, reloading is harmless
	// and lets servers refresh ECH internal counters.
	if err := r.reload(ctx); err != nil {
		return fmt.Errorf("rotator: R9 reload: %w", err)
	}

	_ = newEntry // keep symbol referenced for future telemetry hook
	return nil
}

// ----------------------------------------------------------------
// helpers
// ----------------------------------------------------------------

// pickConfigID returns a uint8 not currently used by any entry in the
// keystore. Implements RFC 9849 §4.1's recommended rejection-sampling
// strategy. After 32 failed attempts we fall back to a deterministic
// scan; with a Store size << 256 collisions are vanishingly rare.
func (r *Rotator) pickConfigID() (uint8, error) {
	used := map[uint8]bool{}
	for _, e := range r.deps.Store.List() {
		used[e.ConfigID] = true
	}
	if len(used) >= 256 {
		return 0, fmt.Errorf("rotator: all 256 config_ids in use (cannot rotate)")
	}
	var b [1]byte
	for i := 0; i < 32; i++ {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		if !used[b[0]] {
			return b[0], nil
		}
	}
	for v := 0; v <= 255; v++ {
		if !used[uint8(v)] {
			return uint8(v), nil
		}
	}
	return 0, fmt.Errorf("rotator: unreachable") // len(used) < 256 guarded above
}

// buildPEMBytes assembles the .ech file contents for a fresh key.
// The single ECHConfig in the embedded list is the one we just made.
func (r *Rotator) buildPEMBytes(kp *hpke.KeyPair, c echconfig.Config) ([]byte, error) {
	listBytes, err := echconfig.MarshalList(&echconfig.List{Configs: []echconfig.Config{c}})
	if err != nil {
		return nil, err
	}
	f := &pemfile.File{
		KeyPair:         kp,
		ConfigListBytes: listBytes,
	}
	return pemfile.Marshal(f)
}

// buildOverlapList returns an ECHConfigList containing the new key
// followed by every Previous key currently known to the keystore.
// The Previous keys are read back from disk so we don't have to keep
// their ECHConfig structures alongside the metadata.
//
// Returns the wire bytes plus the list of (configID, state) pairs
// the caller should mark as in-DNS.
func (r *Rotator) buildOverlapList(newConfig echconfig.Config) ([]byte, []uint8, error) {
	configs := []echconfig.Config{newConfig}
	marked := []uint8{newConfig.ConfigID}

	for _, e := range r.deps.Store.List() {
		if e.State != keystore.StatePrevious {
			continue
		}
		raw, err := r.deps.Store.Read(e)
		if err != nil {
			return nil, nil, fmt.Errorf("read previous key %02x: %w", e.ConfigID, err)
		}
		f, err := pemfile.Parse(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("parse previous key %02x: %w", e.ConfigID, err)
		}
		oldList, err := echconfig.UnmarshalList(f.ConfigListBytes)
		if err != nil || len(oldList.Configs) == 0 {
			return nil, nil, fmt.Errorf("decode previous key %02x: %w", e.ConfigID, err)
		}
		configs = append(configs, oldList.Configs[0])
		marked = append(marked, e.ConfigID)
	}

	listBytes, err := echconfig.MarshalList(&echconfig.List{Configs: configs})
	if err != nil {
		return nil, nil, err
	}
	return listBytes, marked, nil
}

// buildSoloList returns an ECHConfigList containing only newConfig.
func (r *Rotator) buildSoloList(newConfig echconfig.Config) ([]byte, error) {
	return echconfig.MarshalList(&echconfig.List{Configs: []echconfig.Config{newConfig}})
}

// publishDNS writes the given raw ECHConfigList bytes into the
// `ech=` SvcParam of our managed HTTPS RR, preserving every other
// SvcParam already present.
//
// If the HTTPS RR doesn't exist (dns.ErrRecordNotFound), we surface
// that to the caller — the operator should run `ech-keymgr init`.
func (r *Rotator) publishDNS(ctx context.Context, listBytes []byte) error {
	b64 := base64.StdEncoding.EncodeToString(listBytes)

	owner := relName(r.cfg.RecordFQDN, r.cfg.DNSZone)

	// 1. Read existing RDATA so we can preserve other SvcParams.
	current, err := r.deps.DNS.GetHTTPSRDATA(ctx, r.cfg.DNSZone, owner)
	if err != nil {
		return err
	}

	// 2. Mutate every record's `ech=` param (in practice there is
	// usually exactly one ServiceMode HTTPS RR per owner; we be
	// defensive in case of multi-record advertisement).
	out := make([]string, 0, len(current))
	for _, raw := range current {
		rec, err := svcb.Parse(raw)
		if err != nil {
			return fmt.Errorf("parse existing HTTPS RR %q: %w", raw, err)
		}
		if rec.IsAliasMode() {
			// AliasMode records have no SvcParams; skip.
			out = append(out, rec.String())
			continue
		}
		patched, err := svcb.SetECH(rec, b64)
		if err != nil {
			return err
		}
		out = append(out, patched.String())
	}

	// 3. Push the patched list back.
	return r.deps.DNS.PutHTTPSRDATA(ctx, r.cfg.DNSZone, owner, r.cfg.DNSTTL, out)
}

// markInDNS records the wall-clock time at which the listed configs
// became visible in DNS. Used by future verify / status output.
func (r *Rotator) markInDNS(ids []uint8) error {
	now := r.cfg.Clock.Now()
	for _, id := range ids {
		e, err := r.deps.Store.Lookup(id)
		if err != nil {
			return err
		}
		if err := r.deps.Store.SetState(id, e.State, now, e.ScheduledDropAt); err != nil {
			return err
		}
	}
	return nil
}

// reload calls the configured reloader with our reload timeout.
func (r *Rotator) reload(ctx context.Context) error {
	rctx, cancel := context.WithTimeout(ctx, r.cfg.ReloadTimeout)
	defer cancel()
	return r.deps.Reloader.Reload(rctx)
}

// relName converts an absolute FQDN into the owner name our DNS
// provider expects (relative to zone, with "@" used for the apex).
//
//	("example.com",      "example.com") → "@"
//	("hidden.example.com","example.com") → "hidden"
//	("a.b.example.com",  "example.com") → "a.b"
//
// Inputs are normalised to lower-case and stripped of trailing dots.
func relName(fqdn, zone string) string {
	fqdn = trimDot(toLower(fqdn))
	zone = trimDot(toLower(zone))
	if fqdn == zone {
		return "@"
	}
	if hasSuffix(fqdn, "."+zone) {
		return fqdn[:len(fqdn)-len(zone)-1]
	}
	// Caller passed an FQDN that doesn't match the zone — let the
	// provider surface a clearer error than we could here.
	return fqdn
}

func toLower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func trimDot(s string) string {
	for len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
