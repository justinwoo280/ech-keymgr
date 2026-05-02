//go:build mock || community || all

package mock

import (
	"context"
	"strings"
	"sync"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

func init() {
	dns.Register("mock", New)
}

// Provider is an in-memory dns.Provider implementation. Safe for
// concurrent use.
type Provider struct {
	mu      sync.RWMutex
	records map[string][]string // "zone|owner" → []rdata
}

var _ dns.Provider = (*Provider)(nil)

// New constructs a fresh in-memory provider. Configuration is ignored.
func New(_ map[string]any) (dns.Provider, error) {
	return &Provider{records: map[string][]string{}}, nil
}

// Name implements dns.Provider.
func (p *Provider) Name() string { return "mock" }

// GetHTTPSRDATA implements dns.Provider.
func (p *Provider) GetHTTPSRDATA(_ context.Context, zone, name string) ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	rs, ok := p.records[key(zone, name)]
	if !ok || len(rs) == 0 {
		return nil, dns.ErrRecordNotFound
	}
	out := make([]string, len(rs))
	copy(out, rs)
	return out, nil
}

// PutHTTPSRDATA implements dns.Provider with full-RRset replacement.
func (p *Provider) PutHTTPSRDATA(_ context.Context, zone, name string, _ uint32, rdata []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]string, len(rdata))
	copy(cp, rdata)
	p.records[key(zone, name)] = cp
	return nil
}

// DeleteHTTPSRDATA implements dns.Provider; idempotent.
func (p *Provider) DeleteHTTPSRDATA(_ context.Context, zone, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.records, key(zone, name))
	return nil
}

// Snapshot returns a deep copy of the entire underlying state, useful
// for tests asserting on rotator behaviour.
func (p *Provider) Snapshot() map[string][]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string][]string, len(p.records))
	for k, v := range p.records {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// key normalizes (zone, name) into a single map key. Both are
// lowercased and stripped of trailing dots so callers don't have to
// agree on a canonical form.
func key(zone, name string) string {
	z := strings.ToLower(strings.TrimSuffix(zone, "."))
	n := strings.ToLower(strings.TrimSuffix(name, "."))
	if n == "" || n == "@" {
		n = z
	} else if !strings.HasSuffix(n, "."+z) && n != z {
		n = n + "." + z
	}
	return z + "|" + n
}
