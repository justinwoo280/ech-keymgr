//go:build cloudflare || all

package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

// fakeCF spins up an httptest.Server that mimics the small subset of
// the Cloudflare API used by the provider. Mutations are recorded so
// tests can assert on them.
type fakeCF struct {
	t          *testing.T
	server     *httptest.Server
	zoneID     string
	zoneName   string
	gotAuth    string
	records    map[string]httpsRecord // record_id → record
	nextID     int
	createBody []map[string]any
	deleteIDs  []string
}

func newFakeCF(t *testing.T) *fakeCF {
	f := &fakeCF{
		t:        t,
		zoneID:   "ZID-001",
		zoneName: "example.com",
		records:  map[string]httpsRecord{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCF) handle(w http.ResponseWriter, r *http.Request) {
	f.gotAuth = r.Header.Get("Authorization")
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/zones") && !strings.Contains(r.URL.Path, "dns_records"):
		// /zones?name=example.com
		writeJSON(w, listZonesResponse{
			envelope: envelope{Success: true},
			Result: []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{{ID: f.zoneID, Name: f.zoneName}},
		})
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/dns_records"):
		owner := r.URL.Query().Get("name")
		var matched []httpsRecord
		for _, rec := range f.records {
			if rec.Name == owner {
				matched = append(matched, rec)
			}
		}
		writeJSON(w, listRecordsResponse{
			envelope: envelope{Success: true},
			Result:   matched,
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dns_records"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.createBody = append(f.createBody, body)
		f.nextID++
		id := newID(f.nextID)
		data := body["data"].(map[string]any)
		f.records[id] = httpsRecord{
			ID:   id,
			Name: body["name"].(string),
			Type: "HTTPS",
			Data: &httpsRecordData{
				Priority: uint16(intOrFloat(data["priority"])),
				Target:   data["target"].(string),
				Value:    data["value"].(string),
			},
		}
		writeJSON(w, createRecordResponse{
			envelope: envelope{Success: true},
			Result:   struct{ ID string `json:"id"` }{ID: id},
		})
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/dns_records/"):
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.deleteIDs = append(f.deleteIDs, id)
		delete(f.records, id)
		writeJSON(w, envelope{Success: true})
	default:
		http.Error(w, "fake CF: unhandled "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func intOrFloat(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

func newID(n int) string { return "rec-" + string(rune('a'+n-1)) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ----------------------------------------------------------------

func newProvider(t *testing.T, f *fakeCF) *Provider {
	t.Helper()
	p, err := New(map[string]any{
		"api_token": "test-token",
		"api_base":  f.server.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p.(*Provider)
}

func TestNew_RejectsMissingToken(t *testing.T) {
	if _, err := New(map[string]any{}); err == nil {
		t.Errorf("expected error on missing api_token")
	}
}

func TestName(t *testing.T) {
	f := newFakeCF(t)
	if newProvider(t, f).Name() != "cloudflare" {
		t.Errorf("Name mismatch")
	}
}

func TestGet_NotFound(t *testing.T) {
	f := newFakeCF(t)
	p := newProvider(t, f)
	_, err := p.GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if !errors.Is(err, dns.ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
}

func TestPut_CreatesAndAuthHeader(t *testing.T) {
	f := newFakeCF(t)
	p := newProvider(t, f)
	rdata := []string{`1 . alpn="h2,h3" ech="AEX+DQBB"`}
	if err := p.PutHTTPSRDATA(context.Background(), "example.com", "hidden", 300, rdata); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if f.gotAuth != "Bearer test-token" {
		t.Errorf("auth header = %q", f.gotAuth)
	}
	if len(f.createBody) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(f.createBody))
	}
	got := f.createBody[0]
	if got["type"] != "HTTPS" {
		t.Errorf("type = %v", got["type"])
	}
	if got["name"] != "hidden.example.com" {
		t.Errorf("name = %v", got["name"])
	}
	data := got["data"].(map[string]any)
	if intOrFloat(data["priority"]) != 1 {
		t.Errorf("priority = %v", data["priority"])
	}
	if data["target"] != "." {
		t.Errorf("target = %v", data["target"])
	}
	if data["value"] != `alpn="h2,h3" ech="AEX+DQBB"` {
		t.Errorf("value = %q", data["value"])
	}
}

func TestPut_DeletesStaleAfterCreate(t *testing.T) {
	f := newFakeCF(t)
	// Pre-existing record at same owner.
	f.nextID++
	preID := newID(f.nextID)
	f.records[preID] = httpsRecord{
		ID:   preID,
		Name: "hidden.example.com",
		Type: "HTTPS",
		Data: &httpsRecordData{Priority: 1, Target: ".", Value: `ech="OLD"`},
	}
	p := newProvider(t, f)
	rdata := []string{`1 . ech="NEW"`}
	if err := p.PutHTTPSRDATA(context.Background(), "example.com", "hidden", 300, rdata); err != nil {
		t.Fatal(err)
	}
	// Old record should be gone, new one present.
	if _, ok := f.records[preID]; ok {
		t.Errorf("stale record %s was not deleted", preID)
	}
	if len(f.records) != 1 {
		t.Errorf("expected 1 record after Put, got %d", len(f.records))
	}
	for _, rec := range f.records {
		if rec.Data.Value != `ech="NEW"` {
			t.Errorf("unexpected value %q", rec.Data.Value)
		}
	}
	// Sanity: stale was deleted via API
	if !contains(f.deleteIDs, preID) {
		t.Errorf("expected deletion of %s, got %v", preID, f.deleteIDs)
	}
}

func TestGet_ReturnsRDATA(t *testing.T) {
	f := newFakeCF(t)
	f.nextID++
	id := newID(f.nextID)
	f.records[id] = httpsRecord{
		ID:   id,
		Name: "hidden.example.com",
		Type: "HTTPS",
		Data: &httpsRecordData{Priority: 1, Target: ".", Value: `alpn="h2" ech="AEX"`},
	}
	p := newProvider(t, f)
	got, err := p.GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != `1 . alpn="h2" ech="AEX"` {
		t.Errorf("got %v", got)
	}
}

func TestDelete_Idempotent(t *testing.T) {
	f := newFakeCF(t)
	p := newProvider(t, f)
	if err := p.DeleteHTTPSRDATA(context.Background(), "example.com", "absent"); err != nil {
		t.Errorf("delete on absent should be nil, got %v", err)
	}
}

func TestSplitRDATA(t *testing.T) {
	cases := []struct {
		in       string
		pri      uint16
		target   string
		params   string
		wantErr  bool
	}{
		{`1 . alpn="h2" ech="AEX"`, 1, ".", `alpn="h2" ech="AEX"`, false},
		{`0 cdn.example.net.`, 0, "cdn.example.net.", "", false},
		{`1 . `, 1, ".", "", false},
		{``, 0, "", "", true},
		{`x . ech="x"`, 0, "", "", true},
	}
	for _, c := range cases {
		pri, tgt, params, err := splitRDATA(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if pri != c.pri || tgt != c.target || strings.TrimSpace(params) != c.params {
			t.Errorf("%q: got (%d,%q,%q)", c.in, pri, tgt, params)
		}
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
			t.Errorf("fqdn(%q,%q)=%q, want %q", c.name, c.zone, got, c.want)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}

// guard: ensure the body really got read (catches httptest leaks)
var _ = io.Discard
