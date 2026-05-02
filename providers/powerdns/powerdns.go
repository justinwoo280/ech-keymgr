//go:build powerdns || all

package powerdns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

const (
	defaultServerID = "localhost"
	httpTimeout     = 30 * time.Second
)

func init() {
	dns.Register("powerdns", New)
}

// Provider is the PowerDNS Authoritative implementation of dns.Provider.
type Provider struct {
	apiURL   string // e.g. https://pdns.example.com:8081
	apiKey   string
	serverID string
	http     *http.Client
}

var _ dns.Provider = (*Provider)(nil)

// New is the Factory consumed by pkg/dns.Build.
//
// Required cfg keys:
//
//	api_url: string  (e.g. http://127.0.0.1:8081)
//	api_key: string
//
// Optional cfg keys:
//
//	server_id: string (defaults to "localhost", PowerDNS's only built-in id)
func New(cfg map[string]any) (dns.Provider, error) {
	apiURL, _ := cfg["api_url"].(string)
	if strings.TrimSpace(apiURL) == "" {
		return nil, errors.New("powerdns: api_url is required")
	}
	apiKey, _ := cfg["api_key"].(string)
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("powerdns: api_key is required")
	}
	srv, _ := cfg["server_id"].(string)
	if srv == "" {
		srv = defaultServerID
	}
	return &Provider{
		apiURL:   strings.TrimRight(apiURL, "/"),
		apiKey:   apiKey,
		serverID: srv,
		http:     &http.Client{Timeout: httpTimeout},
	}, nil
}

// Name implements dns.Provider.
func (p *Provider) Name() string { return "powerdns" }

// GetHTTPSRDATA implements dns.Provider.
//
// PowerDNS GET /zones/<zone> returns every RRset for the zone, so we
// fetch once and filter to the requested owner+type. For zones with
// thousands of records this is wasteful; in practice ECH-managed
// owners are tiny and PowerDNS is fast.
func (p *Provider) GetHTTPSRDATA(ctx context.Context, zone, name string) ([]string, error) {
	z, err := p.getZone(ctx, zone)
	if err != nil {
		return nil, err
	}
	owner := pdnsName(name, zone)
	for _, rs := range z.RRsets {
		if rs.Type != "HTTPS" {
			continue
		}
		if !strings.EqualFold(rs.Name, owner) {
			continue
		}
		out := make([]string, 0, len(rs.Records))
		for _, r := range rs.Records {
			if r.Disabled {
				continue
			}
			out = append(out, strings.TrimSpace(r.Content))
		}
		if len(out) == 0 {
			return nil, dns.ErrRecordNotFound
		}
		return out, nil
	}
	return nil, dns.ErrRecordNotFound
}

// PutHTTPSRDATA implements dns.Provider via a single REPLACE in one
// PATCH transaction (a key advantage over Cloudflare's two-step
// approach: PowerDNS guarantees atomic swap).
func (p *Provider) PutHTTPSRDATA(ctx context.Context, zone, name string, ttl uint32, rdata []string) error {
	owner := pdnsName(name, zone)
	records := make([]rrRecord, 0, len(rdata))
	for _, line := range rdata {
		records = append(records, rrRecord{
			Content:  strings.TrimSpace(line),
			Disabled: false,
		})
	}
	if ttl == 0 {
		ttl = 300
	}
	body := patchBody{
		RRsets: []rrSet{{
			Name:       owner,
			Type:       "HTTPS",
			TTL:        ttl,
			ChangeType: "REPLACE",
			Records:    records,
		}},
	}
	return p.patchZone(ctx, zone, body)
}

// DeleteHTTPSRDATA implements dns.Provider. Idempotent: PowerDNS
// returns 204 even when deleting an absent RRset.
func (p *Provider) DeleteHTTPSRDATA(ctx context.Context, zone, name string) error {
	owner := pdnsName(name, zone)
	body := patchBody{
		RRsets: []rrSet{{
			Name:       owner,
			Type:       "HTTPS",
			ChangeType: "DELETE",
		}},
	}
	return p.patchZone(ctx, zone, body)
}

// ----------------------------------------------------------------
// internals
// ----------------------------------------------------------------

func (p *Provider) getZone(ctx context.Context, zone string) (*zoneResp, error) {
	zoneID := pdnsZone(zone)
	path := fmt.Sprintf("/api/v1/servers/%s/zones/%s",
		url.PathEscape(p.serverID), url.PathEscape(zoneID))
	var z zoneResp
	if err := p.do(ctx, http.MethodGet, path, nil, &z); err != nil {
		return nil, err
	}
	return &z, nil
}

func (p *Provider) patchZone(ctx context.Context, zone string, body patchBody) error {
	zoneID := pdnsZone(zone)
	path := fmt.Sprintf("/api/v1/servers/%s/zones/%s",
		url.PathEscape(p.serverID), url.PathEscape(zoneID))
	return p.do(ctx, http.MethodPatch, path, body, nil)
}

func (p *Provider) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("powerdns: marshal: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.apiURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("powerdns: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if resp.StatusCode == http.StatusNotFound {
		// Zone or server id not found.
		return fmt.Errorf("powerdns: %s %s: HTTP 404: %s", method, path, snippet(raw))
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("powerdns: %s %s: HTTP %d: %s",
			method, path, resp.StatusCode, snippet(raw))
	}
	// 204 No Content from PATCH carries no body.
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("powerdns: %s %s: bad JSON: %w (body: %s)",
			method, path, err, snippet(raw))
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

type zoneResp struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	RRsets []rrSet `json:"rrsets"`
}

type rrSet struct {
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	TTL        uint32     `json:"ttl,omitempty"`
	ChangeType string     `json:"changetype,omitempty"`
	Records    []rrRecord `json:"records,omitempty"`
}

type rrRecord struct {
	Content  string `json:"content"`
	Disabled bool   `json:"disabled"`
}

type patchBody struct {
	RRsets []rrSet `json:"rrsets"`
}

// ----------------------------------------------------------------
// name helpers
// ----------------------------------------------------------------

// pdnsZone returns the PowerDNS zone id, which by convention is the
// zone name with a trailing dot, in lowercase.
//
//	"example.com"  → "example.com."
//	"example.com." → "example.com."
func pdnsZone(zone string) string {
	z := strings.ToLower(strings.TrimSuffix(zone, "."))
	return z + "."
}

// pdnsName returns the FQDN form PowerDNS expects: lowercase, with a
// trailing dot.
//
//	("@",   "example.com") → "example.com."
//	("foo", "example.com") → "foo.example.com."
//	("foo.example.com", _) → "foo.example.com."
func pdnsName(name, zone string) string {
	zone = strings.ToLower(strings.TrimSuffix(zone, "."))
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" || name == "@" {
		return zone + "."
	}
	if name == zone || strings.HasSuffix(name, "."+zone) {
		return name + "."
	}
	return name + "." + zone + "."
}
