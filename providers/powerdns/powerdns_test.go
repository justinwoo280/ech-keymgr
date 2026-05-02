//go:build powerdns || all

package powerdns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

type fakePDNS struct {
	t       *testing.T
	server  *httptest.Server
	apiKey  string
	gotKey  string
	zone    *zoneResp
	patches []patchBody
}

func newFakePDNS(t *testing.T, initialRRsets []rrSet) *fakePDNS {
	f := &fakePDNS{
		t:      t,
		apiKey: "test-key",
		zone: &zoneResp{
			ID:     "example.com.",
			Name:   "example.com.",
			RRsets: initialRRsets,
		},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakePDNS) handle(w http.ResponseWriter, r *http.Request) {
	f.gotKey = r.Header.Get("X-API-Key")
	if !strings.Contains(r.URL.Path, "/zones/") {
		http.Error(w, "unhandled path "+r.URL.Path, 404)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.zone)
	case http.MethodPatch:
		var pb patchBody
		if err := json.NewDecoder(r.Body).Decode(&pb); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		f.patches = append(f.patches, pb)
		// Apply patch to in-memory zone so subsequent GETs reflect it.
		for _, ch := range pb.RRsets {
			f.applyPatch(ch)
		}
		w.WriteHeader(204)
	default:
		http.Error(w, "unhandled "+r.Method, http.StatusMethodNotAllowed)
	}
}

func (f *fakePDNS) applyPatch(ch rrSet) {
	// remove any existing rrset with same name+type
	out := f.zone.RRsets[:0]
	for _, rs := range f.zone.RRsets {
		if rs.Name != ch.Name || rs.Type != ch.Type {
			out = append(out, rs)
		}
	}
	f.zone.RRsets = out
	if ch.ChangeType == "REPLACE" && len(ch.Records) > 0 {
		f.zone.RRsets = append(f.zone.RRsets, ch)
	}
}

func newProvider(t *testing.T, f *fakePDNS) *Provider {
	t.Helper()
	p, err := New(map[string]any{
		"api_url": f.server.URL,
		"api_key": f.apiKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Provider)
}

// ----------------------------------------------------------------

func TestNew_RequiresURLAndKey(t *testing.T) {
	if _, err := New(map[string]any{"api_key": "k"}); err == nil {
		t.Errorf("expected error on missing api_url")
	}
	if _, err := New(map[string]any{"api_url": "http://x"}); err == nil {
		t.Errorf("expected error on missing api_key")
	}
}

func TestName(t *testing.T) {
	f := newFakePDNS(t, nil)
	if newProvider(t, f).Name() != "powerdns" {
		t.Errorf("Name mismatch")
	}
}

func TestGet_NotFound(t *testing.T) {
	f := newFakePDNS(t, nil)
	p := newProvider(t, f)
	_, err := p.GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if !errors.Is(err, dns.ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
}

func TestGet_FiltersByOwnerAndType(t *testing.T) {
	f := newFakePDNS(t, []rrSet{
		{
			Name: "hidden.example.com.", Type: "HTTPS", TTL: 300,
			Records: []rrRecord{{Content: `1 . alpn="h2" ech="AEX"`}},
		},
		{
			Name: "hidden.example.com.", Type: "A", TTL: 300,
			Records: []rrRecord{{Content: `1.2.3.4`}},
		},
		{
			Name: "other.example.com.", Type: "HTTPS", TTL: 300,
			Records: []rrRecord{{Content: `1 . ech="OTHER"`}},
		},
	})
	p := newProvider(t, f)
	got, err := p.GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != `1 . alpn="h2" ech="AEX"` {
		t.Errorf("got %v", got)
	}
	if f.gotKey != "test-key" {
		t.Errorf("X-API-Key header = %q", f.gotKey)
	}
}

func TestGet_SkipsDisabledRecords(t *testing.T) {
	f := newFakePDNS(t, []rrSet{{
		Name: "hidden.example.com.", Type: "HTTPS", TTL: 300,
		Records: []rrRecord{
			{Content: `1 . ech="AEX"`, Disabled: false},
			{Content: `1 . ech="DEAD"`, Disabled: true},
		},
	}})
	got, err := newProvider(t, f).GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if err != nil || len(got) != 1 || got[0] != `1 . ech="AEX"` {
		t.Errorf("got %v err=%v", got, err)
	}
}

func TestPut_AtomicReplace(t *testing.T) {
	f := newFakePDNS(t, []rrSet{{
		Name: "hidden.example.com.", Type: "HTTPS", TTL: 300,
		Records: []rrRecord{{Content: `1 . ech="OLD"`}},
	}})
	p := newProvider(t, f)

	rdata := []string{`1 . alpn="h2" ech="NEW1"`, `2 . ech="NEW2"`}
	if err := p.PutHTTPSRDATA(context.Background(), "example.com", "hidden", 600, rdata); err != nil {
		t.Fatal(err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("expected exactly 1 PATCH, got %d", len(f.patches))
	}
	pb := f.patches[0]
	if len(pb.RRsets) != 1 {
		t.Fatalf("expected 1 rrset in patch, got %d", len(pb.RRsets))
	}
	rs := pb.RRsets[0]
	if rs.Name != "hidden.example.com." {
		t.Errorf("name = %q", rs.Name)
	}
	if rs.Type != "HTTPS" {
		t.Errorf("type = %q", rs.Type)
	}
	if rs.TTL != 600 {
		t.Errorf("ttl = %d", rs.TTL)
	}
	if rs.ChangeType != "REPLACE" {
		t.Errorf("changetype = %q", rs.ChangeType)
	}
	if len(rs.Records) != 2 {
		t.Errorf("records len = %d", len(rs.Records))
	}
	if rs.Records[0].Content != `1 . alpn="h2" ech="NEW1"` {
		t.Errorf("rec0.Content = %q", rs.Records[0].Content)
	}
	// Confirm the new state is visible via GET.
	got, err := p.GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if err != nil || len(got) != 2 {
		t.Errorf("after Put, GET=%v err=%v", got, err)
	}
}

func TestPut_DefaultsTTL(t *testing.T) {
	f := newFakePDNS(t, nil)
	if err := newProvider(t, f).PutHTTPSRDATA(
		context.Background(), "example.com", "hidden", 0, []string{`1 . ech="X"`},
	); err != nil {
		t.Fatal(err)
	}
	if f.patches[0].RRsets[0].TTL != 300 {
		t.Errorf("default TTL not applied: %d", f.patches[0].RRsets[0].TTL)
	}
}

func TestDelete_IssuesDeleteChangetype(t *testing.T) {
	f := newFakePDNS(t, []rrSet{{
		Name: "hidden.example.com.", Type: "HTTPS",
		Records: []rrRecord{{Content: `1 . ech="X"`}},
	}})
	p := newProvider(t, f)
	if err := p.DeleteHTTPSRDATA(context.Background(), "example.com", "hidden"); err != nil {
		t.Fatal(err)
	}
	if f.patches[0].RRsets[0].ChangeType != "DELETE" {
		t.Errorf("expected DELETE changetype")
	}
	// Idempotent — second call also succeeds.
	if err := p.DeleteHTTPSRDATA(context.Background(), "example.com", "hidden"); err != nil {
		t.Errorf("second delete should be idempotent, got %v", err)
	}
}

func TestPdnsName(t *testing.T) {
	cases := []struct{ name, zone, want string }{
		{"@", "example.com", "example.com."},
		{"", "example.com", "example.com."},
		{"foo", "example.com", "foo.example.com."},
		{"foo.example.com", "example.com", "foo.example.com."},
		{"FOO.example.com.", "example.com", "foo.example.com."},
	}
	for _, c := range cases {
		if got := pdnsName(c.name, c.zone); got != c.want {
			t.Errorf("pdnsName(%q,%q)=%q want %q", c.name, c.zone, got, c.want)
		}
	}
}
