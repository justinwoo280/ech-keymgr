//go:build akamai || all

package akamai

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

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v13/pkg/edgegrid"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

const httpTimeout = 30 * time.Second

func init() {
	dns.Register("akamai", New)
}

// Provider is the Akamai Edge DNS implementation of dns.Provider.
type Provider struct {
	cfg  *edgegrid.Config
	http *http.Client
}

var _ dns.Provider = (*Provider)(nil)

// New is the Factory consumed by pkg/dns.Build.
//
// Two credential shapes are accepted (mutually exclusive):
//
// 1. Inline:
//
//	host, client_token, client_secret, access_token   (required)
//	account_key                                       (optional)
//
// 2. .edgerc file:
//
//	edgerc_path  (required)
//	section      (optional, default "default")
func New(raw map[string]any) (dns.Provider, error) {
	cfg, err := buildEdgeGridConfig(raw)
	if err != nil {
		return nil, err
	}
	return &Provider{
		cfg:  cfg,
		http: &http.Client{Timeout: httpTimeout},
	}, nil
}

// buildEdgeGridConfig converts the YAML credential block into a
// validated edgegrid.Config. Either shape is accepted but mixing
// the two is an error.
func buildEdgeGridConfig(raw map[string]any) (*edgegrid.Config, error) {
	getStr := func(k string) string {
		if v, ok := raw[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}

	edgerc := getStr("edgerc_path")
	host := getStr("host")

	switch {
	case edgerc != "" && host != "":
		return nil, errors.New("akamai: specify either edgerc_path OR inline (host/client_token/...) credentials, not both")
	case edgerc != "":
		section := getStr("section")
		if section == "" {
			section = "default"
		}
		c, err := edgegrid.New(
			edgegrid.WithFile(edgerc),
			edgegrid.WithSection(section),
		)
		if err != nil {
			return nil, fmt.Errorf("akamai: load %s [%s]: %w", edgerc, section, err)
		}
		return c, nil
	case host != "":
		c := &edgegrid.Config{
			Host:         host,
			ClientToken:  getStr("client_token"),
			ClientSecret: getStr("client_secret"),
			AccessToken:  getStr("access_token"),
			AccountKey:   getStr("account_key"),
		}
		if c.ClientToken == "" || c.ClientSecret == "" || c.AccessToken == "" {
			return nil, errors.New("akamai: inline credentials require host + client_token + client_secret + access_token")
		}
		// edgegrid.Config has a Validate() method; let it complain
		// about anything else (e.g. trailing slash on host).
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("akamai: invalid inline credentials: %w", err)
		}
		return c, nil
	default:
		return nil, errors.New("akamai: credentials missing — set edgerc_path or inline host/client_token/client_secret/access_token")
	}
}

// Name implements dns.Provider.
func (p *Provider) Name() string { return "akamai" }

// GetHTTPSRDATA implements dns.Provider.
//
// Edge DNS returns 404 when the named RRset doesn't exist; we map
// that to dns.ErrRecordNotFound. All other non-2xx responses become
// wrapped errors with the API's `detail` field for diagnostics.
func (p *Provider) GetHTTPSRDATA(ctx context.Context, zone, name string) ([]string, error) {
	var resp recordsetResponse
	status, err := p.do(ctx, http.MethodGet, p.recordsetPath(zone, name), nil, &resp)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, dns.ErrRecordNotFound
	}
	if status >= 400 {
		return nil, fmt.Errorf("akamai: GET recordset: HTTP %d", status)
	}
	if len(resp.Rdata) == 0 {
		return nil, dns.ErrRecordNotFound
	}
	out := make([]string, len(resp.Rdata))
	for i, r := range resp.Rdata {
		out[i] = strings.TrimSpace(r)
	}
	return out, nil
}

// PutHTTPSRDATA implements dns.Provider. Edge DNS exposes both
// "create" (POST) and "replace" (PUT) endpoints; we always try PUT
// first and fall back to POST when PUT returns 404 (record absent),
// giving the caller create-or-replace semantics in one call.
func (p *Provider) PutHTTPSRDATA(ctx context.Context, zone, name string, ttl uint32, rdata []string) error {
	if ttl == 0 {
		ttl = 300
	}
	body := recordsetRequest{
		Name:  fqdn(name, zone),
		Type:  "HTTPS",
		TTL:   int(ttl),
		Rdata: append([]string(nil), rdata...),
	}
	status, err := p.do(ctx, http.MethodPut, p.recordsetPath(zone, name), body, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		// Record didn't exist; create instead.
		status, err = p.do(ctx, http.MethodPost, p.recordsetPath(zone, name), body, nil)
		if err != nil {
			return err
		}
	}
	if status >= 400 {
		return fmt.Errorf("akamai: PUT/POST recordset: HTTP %d", status)
	}
	return nil
}

// DeleteHTTPSRDATA implements dns.Provider. Idempotent: 404 from
// the API is mapped to a nil return.
func (p *Provider) DeleteHTTPSRDATA(ctx context.Context, zone, name string) error {
	status, err := p.do(ctx, http.MethodDelete, p.recordsetPath(zone, name), nil, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || status == http.StatusNoContent || status < 400 {
		return nil
	}
	return fmt.Errorf("akamai: DELETE recordset: HTTP %d", status)
}

// recordsetPath builds the per-RRset URL path.
//
// `name` is the owner relative to `zone` ("@" for apex); we
// expand it to a full FQDN before URL-escaping.
func (p *Provider) recordsetPath(zone, name string) string {
	full := fqdn(name, zone)
	return fmt.Sprintf("/config-dns/v2/zones/%s/names/%s/types/HTTPS",
		url.PathEscape(strings.TrimSuffix(strings.ToLower(zone), ".")),
		url.PathEscape(strings.TrimSuffix(full, ".")),
	)
}

// do performs an EdgeGrid-signed request and JSON-decodes the body
// into `out` (if non-nil). Returns (status_code, error).
//
// The error return is reserved for transport / signing / JSON
// parsing failures; HTTP 4xx/5xx come back as a non-zero status
// with err == nil so the caller can distinguish 404 specifically.
func (p *Provider) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("akamai: marshal request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}

	// EdgeGrid Host is just the hostname (no scheme); we always
	// use HTTPS — Akamai requires it.
	u := "https://" + strings.TrimRight(p.cfg.Host, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// SignRequest sets Authorization with the EdgeGrid scheme.
	p.cfg.SignRequest(req)

	resp, err := p.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("akamai: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("akamai: read body: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, nil
	}
	if resp.StatusCode >= 400 {
		// Try to surface the structured error detail if present.
		var aerr apiError
		if jerr := json.Unmarshal(raw, &aerr); jerr == nil && aerr.Detail != "" {
			return resp.StatusCode, fmt.Errorf("akamai: %s %s: HTTP %d: %s",
				method, path, resp.StatusCode, aerr.Detail)
		}
		return resp.StatusCode, fmt.Errorf("akamai: %s %s: HTTP %d: %s",
			method, path, resp.StatusCode, snippet(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("akamai: bad JSON: %w (body: %s)", err, snippet(raw))
		}
	}
	return resp.StatusCode, nil
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

type recordsetRequest struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	TTL   int      `json:"ttl"`
	Rdata []string `json:"rdata"`
}

type recordsetResponse struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	TTL   int      `json:"ttl"`
	Rdata []string `json:"rdata"`
}

// apiError mirrors Edge DNS's RFC 7807 problem+json shape.
type apiError struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// ----------------------------------------------------------------
// name helpers
// ----------------------------------------------------------------

// fqdn converts an owner relative to zone into the absolute FQDN
// Edge DNS expects (no trailing dot).
//
//	("@",   "example.com")    → "example.com"
//	("",    "example.com")    → "example.com"
//	("foo", "example.com")    → "foo.example.com"
//	("foo.example.com", _)    → "foo.example.com"
func fqdn(name, zone string) string {
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	name = strings.TrimSuffix(name, ".")
	if name == "" || name == "@" {
		return zone
	}
	if strings.EqualFold(name, zone) || strings.HasSuffix(strings.ToLower(name), "."+zone) {
		return name
	}
	return name + "." + zone
}
