//go:build cloudflare || all

package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

const (
	defaultAPIBase = "https://api.cloudflare.com/client/v4"
	httpTimeout    = 30 * time.Second
)

// init self-registers this provider so users can reference it by name
// in config.yaml. Building with -tags=cloudflare or -tags=all (the
// default) compiles this package in.
func init() {
	dns.Register("cloudflare", New)
}

// Provider is the Cloudflare implementation of dns.Provider.
type Provider struct {
	apiBase  string
	apiToken string
	http     *http.Client

	mu      sync.RWMutex
	zoneIDs map[string]string // zone name → zone id (LRU-less; zones are O(10))
}

// compile-time interface assertion.
var _ dns.Provider = (*Provider)(nil)

// New is the Factory consumed by pkg/dns.Build. It validates the
// provided credential block and returns a usable Provider.
//
// Required cfg keys:
//
//	api_token: string  (Bearer token)
//
// Optional cfg keys:
//
//	api_base:  string  (override the API base URL, e.g. for tests)
func New(cfg map[string]any) (dns.Provider, error) {
	tok, _ := cfg["api_token"].(string)
	if strings.TrimSpace(tok) == "" {
		return nil, errors.New("cloudflare: api_token is required (got empty string)")
	}
	base, _ := cfg["api_base"].(string)
	if base == "" {
		base = defaultAPIBase
	}
	return &Provider{
		apiBase:  strings.TrimRight(base, "/"),
		apiToken: tok,
		http:     &http.Client{Timeout: httpTimeout},
		zoneIDs:  map[string]string{},
	}, nil
}

// Name implements dns.Provider.
func (p *Provider) Name() string { return "cloudflare" }

// GetHTTPSRDATA implements dns.Provider. It returns every HTTPS RR at
// owner `name` within `zone` in RFC 9460 presentation form, or
// dns.ErrRecordNotFound when the RRset is empty.
func (p *Provider) GetHTTPSRDATA(ctx context.Context, zone, name string) ([]string, error) {
	zoneID, err := p.zoneIDFor(ctx, zone)
	if err != nil {
		return nil, err
	}
	owner := fqdn(name, zone)

	q := url.Values{}
	q.Set("type", "HTTPS")
	q.Set("name", owner)
	q.Set("per_page", "100")

	var resp listRecordsResponse
	if err := p.do(ctx, http.MethodGet,
		fmt.Sprintf("/zones/%s/dns_records?%s", zoneID, q.Encode()),
		nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.Result) == 0 {
		return nil, dns.ErrRecordNotFound
	}

	out := make([]string, 0, len(resp.Result))
	for _, rec := range resp.Result {
		out = append(out, rec.toRDATA())
	}
	return out, nil
}

// PutHTTPSRDATA implements dns.Provider. It REPLACES the complete
// HTTPS RRset at owner `name` within `zone`. Old RRs are deleted,
// then the new set is created. Order is best-effort.
//
// We do delete-then-create rather than per-record diff because the
// caller (ech-keymgr core) already produced the canonical desired
// state via pkg/svcb; we never need to preserve any subset.
func (p *Provider) PutHTTPSRDATA(ctx context.Context, zone, name string, ttl uint32, rdata []string) error {
	zoneID, err := p.zoneIDFor(ctx, zone)
	if err != nil {
		return err
	}
	owner := fqdn(name, zone)

	// 1. List existing HTTPS records at the owner.
	q := url.Values{}
	q.Set("type", "HTTPS")
	q.Set("name", owner)
	q.Set("per_page", "100")
	var listResp listRecordsResponse
	if err := p.do(ctx, http.MethodGet,
		fmt.Sprintf("/zones/%s/dns_records?%s", zoneID, q.Encode()),
		nil, &listResp); err != nil {
		return err
	}

	// 2. Create new records first, so that on failure the old set
	//    survives and we don't leave the owner with no ECH at all.
	created := make([]string, 0, len(rdata))
	for _, line := range rdata {
		body, err := buildCreateBody(owner, ttl, line)
		if err != nil {
			// Roll back any records we already created.
			p.bestEffortDelete(ctx, zoneID, created)
			return err
		}
		var cr createRecordResponse
		if err := p.do(ctx, http.MethodPost,
			fmt.Sprintf("/zones/%s/dns_records", zoneID),
			body, &cr); err != nil {
			p.bestEffortDelete(ctx, zoneID, created)
			return fmt.Errorf("cloudflare: create HTTPS record %q: %w", owner, err)
		}
		created = append(created, cr.Result.ID)
	}

	// 3. Now delete the previously-existing records. If a deletion
	//    fails we surface the error but do not roll back creates;
	//    operator can run `ech-keymgr verify` to detect the dup.
	for _, rec := range listResp.Result {
		if err := p.deleteByID(ctx, zoneID, rec.ID); err != nil {
			return fmt.Errorf("cloudflare: delete stale HTTPS record %s: %w", rec.ID, err)
		}
	}
	return nil
}

// DeleteHTTPSRDATA implements dns.Provider. It is idempotent: if no
// HTTPS RR exists at the owner, nil is returned.
func (p *Provider) DeleteHTTPSRDATA(ctx context.Context, zone, name string) error {
	zoneID, err := p.zoneIDFor(ctx, zone)
	if err != nil {
		return err
	}
	owner := fqdn(name, zone)

	q := url.Values{}
	q.Set("type", "HTTPS")
	q.Set("name", owner)
	q.Set("per_page", "100")
	var listResp listRecordsResponse
	if err := p.do(ctx, http.MethodGet,
		fmt.Sprintf("/zones/%s/dns_records?%s", zoneID, q.Encode()),
		nil, &listResp); err != nil {
		return err
	}
	for _, rec := range listResp.Result {
		if err := p.deleteByID(ctx, zoneID, rec.ID); err != nil {
			return err
		}
	}
	return nil
}

// ----------------------------------------------------------------
// internals
// ----------------------------------------------------------------

func (p *Provider) deleteByID(ctx context.Context, zoneID, recordID string) error {
	var resp envelope
	return p.do(ctx, http.MethodDelete,
		fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID),
		nil, &resp)
}

func (p *Provider) bestEffortDelete(ctx context.Context, zoneID string, ids []string) {
	for _, id := range ids {
		_ = p.deleteByID(ctx, zoneID, id)
	}
}

// zoneIDFor resolves a zone name (e.g. "example.com") to a Cloudflare
// zone_id. Result is cached in-memory for the process lifetime.
func (p *Provider) zoneIDFor(ctx context.Context, zone string) (string, error) {
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	p.mu.RLock()
	id, ok := p.zoneIDs[zone]
	p.mu.RUnlock()
	if ok {
		return id, nil
	}

	q := url.Values{}
	q.Set("name", zone)
	var resp listZonesResponse
	if err := p.do(ctx, http.MethodGet,
		"/zones?"+q.Encode(), nil, &resp); err != nil {
		return "", err
	}
	for _, z := range resp.Result {
		if strings.EqualFold(z.Name, zone) {
			p.mu.Lock()
			p.zoneIDs[zone] = z.ID
			p.mu.Unlock()
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("cloudflare: zone %q not found in account (token may lack Zone:Read)", zone)
}

// do performs an authenticated request and decodes the JSON envelope.
// Cloudflare wraps every response in {success, errors, messages, result}.
func (p *Provider) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cloudflare: marshal request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.apiBase+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB
	if err != nil {
		return fmt.Errorf("cloudflare: read body: %w", err)
	}

	// Cloudflare returns 200 even for some logical errors; rely on the
	// envelope's success flag rather than just the HTTP status.
	if out == nil {
		out = &envelope{}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("cloudflare: %s %s: HTTP %d: bad JSON: %w (body: %s)",
			method, path, resp.StatusCode, err, snippet(raw))
	}
	if env, ok := out.(envelopeReader); ok {
		if !env.GetSuccess() {
			return fmt.Errorf("cloudflare: %s %s: HTTP %d: %s",
				method, path, resp.StatusCode, env.GetErrorsJoined())
		}
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("cloudflare: %s %s: HTTP %d: %s",
			method, path, resp.StatusCode, snippet(raw))
	}
	return nil
}

func snippet(b []byte) string {
	if len(b) <= 200 {
		return string(b)
	}
	return string(b[:200]) + "...(truncated)"
}

// ----------------------------------------------------------------
// API DTOs
// ----------------------------------------------------------------

type envelope struct {
	Success  bool       `json:"success"`
	Errors   []apiError `json:"errors"`
	Messages []apiError `json:"messages"`
}

type envelopeReader interface {
	GetSuccess() bool
	GetErrorsJoined() string
}

func (e envelope) GetSuccess() bool { return e.Success }
func (e envelope) GetErrorsJoined() string {
	if len(e.Errors) == 0 {
		return "(no error details)"
	}
	parts := make([]string, 0, len(e.Errors))
	for _, x := range e.Errors {
		parts = append(parts, fmt.Sprintf("[%d] %s", x.Code, x.Message))
	}
	return strings.Join(parts, "; ")
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type listZonesResponse struct {
	envelope
	Result []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
}

type listRecordsResponse struct {
	envelope
	Result []httpsRecord `json:"result"`
}

type createRecordResponse struct {
	envelope
	Result struct {
		ID string `json:"id"`
	} `json:"result"`
}

// httpsRecord captures the subset of Cloudflare's HTTPS RR shape that
// we care about. Cloudflare returns HTTPS records with a structured
// `data` object: { priority, target, value } where `value` is the
// SvcParam list in RFC 9460 presentation form (without priority/target).
//
// Some Cloudflare accounts also expose a flattened `content` field
// containing the full presentation form ("1 . alpn=..."); we prefer
// `data` when present, fall back to `content` otherwise.
type httpsRecord struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Type    string           `json:"type"`
	TTL     uint32           `json:"ttl"`
	Content string           `json:"content"`
	Data    *httpsRecordData `json:"data,omitempty"`
}

type httpsRecordData struct {
	Priority uint16 `json:"priority"`
	Target   string `json:"target"`
	Value    string `json:"value"`
}

// toRDATA serializes one httpsRecord back to RFC 9460 presentation form.
func (r httpsRecord) toRDATA() string {
	if r.Data != nil {
		tgt := r.Data.Target
		if tgt == "" {
			tgt = "."
		}
		v := strings.TrimSpace(r.Data.Value)
		if v == "" {
			return fmt.Sprintf("%d %s", r.Data.Priority, tgt)
		}
		return fmt.Sprintf("%d %s %s", r.Data.Priority, tgt, v)
	}
	// Some API revisions return only `content`; trust it as-is.
	return strings.TrimSpace(r.Content)
}

// buildCreateBody turns one RDATA presentation-form line into the JSON
// body Cloudflare expects when creating an HTTPS record.
func buildCreateBody(owner string, ttl uint32, rdataLine string) (map[string]any, error) {
	pri, target, params, err := splitRDATA(rdataLine)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: invalid HTTPS RDATA %q: %w", rdataLine, err)
	}
	body := map[string]any{
		"type": "HTTPS",
		"name": owner,
		"data": map[string]any{
			"priority": pri,
			"target":   target,
			"value":    params,
		},
	}
	if ttl > 0 {
		body["ttl"] = ttl
	}
	return body, nil
}

// splitRDATA splits "1 . alpn=\"h2\" ech=\"AEX\"" into
// (priority=1, target=".", params=`alpn="h2" ech="AEX"`).
//
// We do not parse params here (the caller already validated them via
// pkg/svcb); we just hand the SvcParam tail back as opaque text.
func splitRDATA(s string) (priority uint16, target string, params string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, "", "", errors.New("empty RDATA")
	}
	// priority
	idx := strings.IndexAny(s, " \t")
	if idx < 0 {
		return 0, "", "", errors.New("missing target")
	}
	p64, perr := strconv.ParseUint(strings.TrimSpace(s[:idx]), 10, 16)
	if perr != nil {
		return 0, "", "", fmt.Errorf("invalid priority: %w", perr)
	}
	s = strings.TrimLeft(s[idx:], " \t")
	// target
	idx = strings.IndexAny(s, " \t")
	if idx < 0 {
		return uint16(p64), s, "", nil
	}
	target = s[:idx]
	params = strings.TrimLeft(s[idx:], " \t")
	return uint16(p64), target, params, nil
}

// fqdn converts an owner-relative name + zone into the absolute name
// Cloudflare's API expects (no trailing dot).
//
//	("@",   "example.com")  → "example.com"
//	("",    "example.com")  → "example.com"
//	("foo", "example.com")  → "foo.example.com"
//	("foo.example.com", _)  → "foo.example.com"   (already FQDN)
func fqdn(name, zone string) string {
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	name = strings.TrimSuffix(name, ".")
	if name == "" || name == "@" {
		return zone
	}
	if strings.HasSuffix(strings.ToLower(name), "."+zone) || strings.EqualFold(name, zone) {
		return name
	}
	return name + "." + zone
}
