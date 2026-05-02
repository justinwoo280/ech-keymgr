//go:build akamai || all

package akamai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v13/pkg/edgegrid"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

// fakeAkamai is an httptest.Server that mimics the small subset of
// the Edge DNS API ech-keymgr touches.
type fakeAkamai struct {
	t       *testing.T
	server  *httptest.Server
	gotAuth string
	got     []reqRecord
	rrset   *recordsetResponse
}

type reqRecord struct {
	method string
	path   string
	body   string
}

func newFakeAkamai(t *testing.T) *fakeAkamai {
	f := &fakeAkamai{t: t}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeAkamai) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.gotAuth = r.Header.Get("Authorization")
	f.got = append(f.got, reqRecord{method: r.Method, path: r.URL.Path, body: string(body)})

	switch r.Method {
	case http.MethodGet:
		if f.rrset == nil {
			http.Error(w, `{"detail":"not found"}`, 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.rrset)
	case http.MethodPut:
		if f.rrset == nil {
			http.Error(w, `{"detail":"not found"}`, 404)
			return
		}
		var rr recordsetResponse
		_ = json.Unmarshal(body, &rr)
		f.rrset = &rr
		w.WriteHeader(http.StatusOK)
	case http.MethodPost:
		var rr recordsetResponse
		_ = json.Unmarshal(body, &rr)
		f.rrset = &rr
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		if f.rrset == nil {
			http.Error(w, `{"detail":"not found"}`, 404)
			return
		}
		f.rrset = nil
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
	}
}

// newProvider builds a Provider whose underlying HTTP client and
// EdgeGrid Host are pointed at the fake server.
func newProvider(t *testing.T, f *fakeAkamai) *Provider {
	t.Helper()
	u, _ := url.Parse(f.server.URL)
	// Inline credentials with a fake host that won't actually be
	// dialed — we override the http client below with one that
	// rewrites the host transparently.
	cfg := &edgegrid.Config{
		Host:         u.Host,
		ClientToken:  "test-client-token",
		ClientSecret: "test-client-secret",
		AccessToken:  "test-access-token",
	}
	return &Provider{
		cfg: cfg,
		http: &http.Client{
			Transport: &rewriteTransport{base: http.DefaultTransport, scheme: u.Scheme},
		},
	}
}

// rewriteTransport rewrites the URL scheme on every outgoing
// request to match the test server's scheme (http instead of the
// hard-coded https). Host is unchanged because we set it in the
// EdgeGrid Config to the test server's host.
type rewriteTransport struct {
	base   http.RoundTripper
	scheme string
}

func (t *rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = t.scheme
	return t.base.RoundTrip(r)
}

// ----------------------------------------------------------------
// factory
// ----------------------------------------------------------------

func TestNew_RejectsMissingCredentials(t *testing.T) {
	if _, err := New(map[string]any{}); err == nil {
		t.Errorf("expected error on empty credentials")
	}
	if _, err := New(map[string]any{"host": "h"}); err == nil {
		t.Errorf("expected error on host-only")
	}
}

func TestNew_RejectsMixedCredentials(t *testing.T) {
	if _, err := New(map[string]any{
		"edgerc_path":   "/tmp/edgerc",
		"host":          "h",
		"client_token":  "x",
		"client_secret": "y",
		"access_token":  "z",
	}); err == nil {
		t.Errorf("expected error on mixed inline + edgerc")
	}
}

func TestNew_AcceptsInline(t *testing.T) {
	p, err := New(map[string]any{
		"host":          "akab-foo.akamaiapis.net",
		"client_token":  "akab-c",
		"client_secret": "s",
		"access_token":  "akab-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "akamai" {
		t.Errorf("Name = %q", p.Name())
	}
}

// ----------------------------------------------------------------
// API surface
// ----------------------------------------------------------------

func TestGet_NotFound(t *testing.T) {
	f := newFakeAkamai(t)
	p := newProvider(t, f)
	_, err := p.GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if !errors.Is(err, dns.ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
	// Confirm the request was actually signed (Authorization
	// header containing the EdgeGrid scheme).
	if !strings.HasPrefix(f.gotAuth, "EG1-HMAC-SHA256 ") {
		t.Errorf("expected EdgeGrid Authorization header, got %q", f.gotAuth)
	}
}

func TestGet_ReturnsRDATA(t *testing.T) {
	f := newFakeAkamai(t)
	f.rrset = &recordsetResponse{
		Name:  "hidden.example.com",
		Type:  "HTTPS",
		TTL:   300,
		Rdata: []string{`1 . alpn="h2,h3" ech="AEX"`},
	}
	p := newProvider(t, f)
	got, err := p.GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != `1 . alpn="h2,h3" ech="AEX"` {
		t.Errorf("got %v", got)
	}
}

func TestPut_FallsBackToPostOn404(t *testing.T) {
	f := newFakeAkamai(t)
	// fakeAkamai.rrset is nil, so PUT returns 404; provider must POST next.
	p := newProvider(t, f)
	if err := p.PutHTTPSRDATA(context.Background(), "example.com", "hidden", 300, []string{`1 . ech="X"`}); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 2 {
		t.Fatalf("expected 2 requests (PUT then POST), got %d", len(f.got))
	}
	if f.got[0].method != http.MethodPut || f.got[1].method != http.MethodPost {
		t.Errorf("expected PUT then POST, got %s then %s", f.got[0].method, f.got[1].method)
	}
}

func TestPut_ReplacesExisting(t *testing.T) {
	f := newFakeAkamai(t)
	f.rrset = &recordsetResponse{
		Name: "hidden.example.com", Type: "HTTPS", TTL: 300,
		Rdata: []string{`1 . ech="OLD"`},
	}
	p := newProvider(t, f)
	if err := p.PutHTTPSRDATA(context.Background(), "example.com", "hidden", 600,
		[]string{`1 . ech="NEW"`}); err != nil {
		t.Fatal(err)
	}
	got, _ := p.GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if len(got) != 1 || got[0] != `1 . ech="NEW"` {
		t.Errorf("after Put, got %v", got)
	}
	if f.rrset.TTL != 600 {
		t.Errorf("TTL not propagated: %d", f.rrset.TTL)
	}
}

func TestDelete_Idempotent(t *testing.T) {
	f := newFakeAkamai(t)
	p := newProvider(t, f)
	if err := p.DeleteHTTPSRDATA(context.Background(), "example.com", "absent"); err != nil {
		t.Errorf("delete on absent should be nil, got %v", err)
	}
}

func TestRecordsetPath(t *testing.T) {
	p := &Provider{cfg: &edgegrid.Config{Host: "x"}}
	got := p.recordsetPath("example.com", "hidden")
	want := "/config-dns/v2/zones/example.com/names/hidden.example.com/types/HTTPS"
	if got != want {
		t.Errorf("recordsetPath = %q, want %q", got, want)
	}
}

func TestFQDN(t *testing.T) {
	cases := []struct{ name, zone, want string }{
		{"@", "example.com", "example.com"},
		{"", "example.com", "example.com"},
		{"foo", "example.com", "foo.example.com"},
		{"foo.example.com", "example.com", "foo.example.com"},
		{"FOO.EXAMPLE.COM", "example.com", "FOO.EXAMPLE.COM"},
	}
	for _, c := range cases {
		if got := fqdn(c.name, c.zone); got != c.want {
			t.Errorf("fqdn(%q,%q)=%q want %q", c.name, c.zone, got, c.want)
		}
	}
}
