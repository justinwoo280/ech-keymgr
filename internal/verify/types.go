package verify

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

// Severity classifies a Finding. We deliberately keep only two
// levels: it's either OK (a positive observation worth surfacing
// for the user's confidence) or WARN (drift detected). There is no
// ERROR severity because verify never aborts a rotation.
type Severity string

const (
	SeverityOK   Severity = "ok"
	SeverityWarn Severity = "warn"
)

// Finding is one observation produced during a verify run. Code is
// a stable machine-readable identifier so users can grep for it in
// logs / dashboards; Detail is the operator-facing prose.
type Finding struct {
	Severity Severity
	Code     string // e.g. "ECH_DECODES", "DNS_LIST_MISSING_KEY"
	Detail   string
}

// Stable codes (kept short, machine-readable, ALL_CAPS):
const (
	CodeRRPresent           = "DNS_HTTPS_RR_PRESENT"
	CodeRRMissing           = "DNS_HTTPS_RR_MISSING"
	CodeRRMultiple          = "DNS_HTTPS_RR_MULTIPLE"
	CodeECHParamMissing     = "DNS_ECH_PARAM_MISSING"
	CodeECHBadBase64        = "DNS_ECH_BAD_BASE64"
	CodeECHListBadFormat    = "DNS_ECH_LIST_BAD_FORMAT"
	CodeECHListEntryCount   = "DNS_ECH_LIST_ENTRY_COUNT"
	CodeKeyInDNSAndStore    = "KEY_IN_DNS_AND_STORE"
	CodeKeyInDNSNotStore    = "KEY_IN_DNS_NOT_STORE"
	CodeKeyInStoreNotDNS    = "KEY_IN_STORE_NOT_DNS"
	CodeKeyExpectedNotInDNS = "KEY_EXPECTED_NOT_IN_DNS"
)

// Report is the result of one verify run. Multiple Findings is the
// normal case (e.g. one OK plus several DRIFT entries).
type Report struct {
	RecordFQDN string
	Source     string
	Findings   []Finding
}

// Add appends a Finding to the report.
func (r *Report) Add(s Severity, code, detail string) {
	r.Findings = append(r.Findings, Finding{Severity: s, Code: code, Detail: detail})
}

// Warns reports whether any Finding has SeverityWarn.
func (r *Report) Warns() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityWarn {
			return true
		}
	}
	return false
}

// String produces a stable human-readable rendering, suitable for
// CLI output. Findings are grouped: warnings first.
func (r *Report) String() string {
	if r == nil {
		return "<nil report>"
	}
	out := []Finding{}
	out = append(out, r.Findings...)
	sort.SliceStable(out, func(i, j int) bool {
		// warn first, then ok
		if out[i].Severity != out[j].Severity {
			return out[i].Severity == SeverityWarn
		}
		return out[i].Code < out[j].Code
	})
	var b strings.Builder
	fmt.Fprintf(&b, "verify report for %s (source: %s)\n", r.RecordFQDN, r.Source)
	for _, f := range out {
		mark := "  ✓"
		if f.Severity == SeverityWarn {
			mark = "  ⚠"
		}
		fmt.Fprintf(&b, "%s [%s] %s\n", mark, f.Code, f.Detail)
	}
	return b.String()
}

// Source is anything that can return a list of HTTPS RR
// presentation-form RDATA strings for an owner — same shape as
// dns.Provider.GetHTTPSRDATA so we can re-use the same providers
// pre-built for publishing.
type Source interface {
	// Name returns a human-readable identifier (e.g.
	// "provider:cloudflare", "resolver:1.1.1.1:53").
	Name() string

	// GetHTTPSRDATA returns the published HTTPS RR(s) at owner
	// `name` within `zone`. The returned slice MUST be in RFC 9460
	// presentation form — same contract as dns.Provider.
	GetHTTPSRDATA(ctx context.Context, zone, name string) ([]string, error)
}

// ProviderSource adapts a dns.Provider into a verify.Source.
//
// This is the primary backend used by the rotator and CLI: querying
// the same provider we publish through, so we see the authoritative
// state without recursive-resolver cache effects.
type ProviderSource struct {
	P dns.Provider
}

// Name implements Source.
func (p ProviderSource) Name() string {
	if p.P == nil {
		return "provider:<nil>"
	}
	return "provider:" + p.P.Name()
}

// GetHTTPSRDATA implements Source.
func (p ProviderSource) GetHTTPSRDATA(ctx context.Context, zone, name string) ([]string, error) {
	if p.P == nil {
		return nil, errors.New("verify: ProviderSource.P is nil")
	}
	return p.P.GetHTTPSRDATA(ctx, zone, name)
}
