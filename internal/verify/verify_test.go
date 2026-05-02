package verify

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/justinwoo280/ech-keymgr/internal/echconfig"
	"github.com/justinwoo280/ech-keymgr/internal/keystore"
	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

// memSource is a minimal in-memory verify.Source for tests.
type memSource struct {
	rdata []string
	err   error
	name  string
}

func (m *memSource) Name() string { return "test:" + m.name }
func (m *memSource) GetHTTPSRDATA(_ context.Context, _, _ string) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([]string, len(m.rdata))
	copy(out, m.rdata)
	return out, nil
}

// helper: make a freshly-initialised store with N entries and known
// config_ids; entries 0 are StateCurrent, the rest StatePrevious.
func storeWithIDs(t *testing.T, ids ...uint8) *keystore.Store {
	t.Helper()
	s, err := keystore.OpenOrInit(t.TempDir(), "hidden.example.com", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := s.Add([]byte("PEM-"+string(id)), id); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// helper: build a base64 ECHConfigList containing the given IDs.
// Each entry has a tiny but valid wire shape per RFC 9849.
func b64ListWithIDs(t *testing.T, ids ...uint8) string {
	t.Helper()
	cfgs := make([]echconfig.Config, 0, len(ids))
	for _, id := range ids {
		cfgs = append(cfgs, echconfig.Config{
			ConfigID:  id,
			KEMID:     echconfig.KEMX25519HKDFSHA256,
			PublicKey: make([]byte, 32),
			CipherSuites: []echconfig.CipherSuite{
				{KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADAES128GCM},
			},
			MaximumNameLength: 16,
			PublicName:        []byte("example.com"),
		})
	}
	raw, err := echconfig.MarshalList(&echconfig.List{Configs: cfgs})
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// helper: build the full RFC 9460 RDATA string given the base64 ECH.
func rdataWithECH(echB64 string) string {
	return `1 . alpn="h2,h3" ech="` + echB64 + `"`
}

// ----------------------------------------------------------------
// reports
// ----------------------------------------------------------------

func findFinding(rep *Report, code string) *Finding {
	for i := range rep.Findings {
		if rep.Findings[i].Code == code {
			return &rep.Findings[i]
		}
	}
	return nil
}

// ----------------------------------------------------------------
// tests
// ----------------------------------------------------------------

func TestVerify_RequiresSourceAndStore(t *testing.T) {
	if _, err := Verify(context.Background(), Request{}); err == nil {
		t.Errorf("expected error when both nil")
	}
	if _, err := Verify(context.Background(), Request{Source: &memSource{}}); err == nil {
		t.Errorf("expected error when Store nil")
	}
}

func TestVerify_RecordMissing_Warns(t *testing.T) {
	store := storeWithIDs(t)
	src := &memSource{err: dns.ErrRecordNotFound, name: "miss"}
	rep, err := Verify(context.Background(), Request{
		RecordFQDN: "hidden.example.com", DNSZone: "example.com", OwnerRel: "hidden",
		Source: src, Store: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Warns() {
		t.Errorf("expected a warning")
	}
	if findFinding(rep, CodeRRMissing) == nil {
		t.Errorf("expected RR_MISSING finding")
	}
}

func TestVerify_HappyPath_AllOK(t *testing.T) {
	store := storeWithIDs(t, 0xAA)
	src := &memSource{rdata: []string{rdataWithECH(b64ListWithIDs(t, 0xAA))}, name: "ok"}
	rep, err := Verify(context.Background(), Request{
		RecordFQDN: "hidden.example.com", DNSZone: "example.com", OwnerRel: "hidden",
		Source: src, Store: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Warns() {
		t.Errorf("expected no warnings, got: %s", rep)
	}
	if findFinding(rep, CodeKeyInDNSAndStore) == nil {
		t.Errorf("expected KEY_IN_DNS_AND_STORE")
	}
}

func TestVerify_KeyInDNSNotStore_Warns(t *testing.T) {
	store := storeWithIDs(t) // empty
	src := &memSource{       // DNS has 0xAA
		rdata: []string{rdataWithECH(b64ListWithIDs(t, 0xAA))}, name: "drift",
	}
	rep, _ := Verify(context.Background(), Request{
		RecordFQDN: "hidden.example.com", DNSZone: "example.com", OwnerRel: "hidden",
		Source: src, Store: store,
	})
	f := findFinding(rep, CodeKeyInDNSNotStore)
	if f == nil || f.Severity != SeverityWarn {
		t.Errorf("expected warn finding KEY_IN_DNS_NOT_STORE")
	}
}

func TestVerify_KeyExpectedButMissingFromDNS_Warns(t *testing.T) {
	store := storeWithIDs(t, 0xBB) // local has Current 0xBB
	src := &memSource{             // DNS publishes only 0xAA
		rdata: []string{rdataWithECH(b64ListWithIDs(t, 0xAA))}, name: "drift",
	}
	rep, _ := Verify(context.Background(), Request{
		RecordFQDN: "hidden.example.com", DNSZone: "example.com", OwnerRel: "hidden",
		Source: src, Store: store,
	})
	f := findFinding(rep, CodeKeyExpectedNotInDNS)
	if f == nil || f.Severity != SeverityWarn {
		t.Errorf("expected warn finding KEY_EXPECTED_NOT_IN_DNS")
	}
	if !strings.Contains(f.Detail, "0xbb") {
		t.Errorf("expected hex 0xbb in detail, got %q", f.Detail)
	}
}

func TestVerify_GraceKey_NotInDNS_OK(t *testing.T) {
	store := storeWithIDs(t, 0xCC)
	// Move 0xCC into Grace state with a future drop time.
	if err := store.SetState(0xCC, keystore.StateGrace, time.Time{}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// DNS publishes a different key — that's fine, grace shouldn't be there.
	src := &memSource{rdata: []string{rdataWithECH(b64ListWithIDs(t, 0xDD))}, name: "grace"}
	rep, _ := Verify(context.Background(), Request{
		RecordFQDN: "hidden.example.com", DNSZone: "example.com", OwnerRel: "hidden",
		Source: src, Store: store,
	})
	// Grace key should produce an OK (not warn) finding.
	for _, f := range rep.Findings {
		if f.Code == CodeKeyExpectedNotInDNS {
			t.Errorf("grace keys must NOT trigger KEY_EXPECTED_NOT_IN_DNS warn")
		}
	}
}

func TestVerify_BadECHBase64_Warns(t *testing.T) {
	store := storeWithIDs(t)
	src := &memSource{rdata: []string{`1 . ech="!!!not-base64!!!"`}, name: "bad"}
	rep, _ := Verify(context.Background(), Request{
		RecordFQDN: "x", DNSZone: "x", OwnerRel: "x",
		Source: src, Store: store,
	})
	if findFinding(rep, CodeECHBadBase64) == nil {
		t.Errorf("expected DNS_ECH_BAD_BASE64")
	}
}

func TestVerify_NoECHParam_Warns(t *testing.T) {
	store := storeWithIDs(t)
	src := &memSource{rdata: []string{`1 . alpn="h2"`}, name: "no-ech"}
	rep, _ := Verify(context.Background(), Request{
		RecordFQDN: "x", DNSZone: "x", OwnerRel: "x",
		Source: src, Store: store,
	})
	if findFinding(rep, CodeECHParamMissing) == nil {
		t.Errorf("expected DNS_ECH_PARAM_MISSING")
	}
}

func TestVerify_MultipleRRs_Warns(t *testing.T) {
	store := storeWithIDs(t, 0xAA)
	src := &memSource{rdata: []string{
		rdataWithECH(b64ListWithIDs(t, 0xAA)),
		rdataWithECH(b64ListWithIDs(t, 0xAA)),
	}, name: "multi"}
	rep, _ := Verify(context.Background(), Request{
		RecordFQDN: "x", DNSZone: "x", OwnerRel: "x",
		Source: src, Store: store,
	})
	if findFinding(rep, CodeRRMultiple) == nil {
		t.Errorf("expected DNS_HTTPS_RR_MULTIPLE warning")
	}
}

func TestVerify_BadECHListBytes_Warns(t *testing.T) {
	store := storeWithIDs(t)
	// base64-encode some random bytes that aren't a valid ECHConfigList
	badList := base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x02})
	src := &memSource{rdata: []string{`1 . ech="` + badList + `"`}, name: "bad-list"}
	rep, _ := Verify(context.Background(), Request{
		RecordFQDN: "x", DNSZone: "x", OwnerRel: "x",
		Source: src, Store: store,
	})
	if findFinding(rep, CodeECHListBadFormat) == nil {
		t.Errorf("expected DNS_ECH_LIST_BAD_FORMAT")
	}
}

func TestReport_StringRendering(t *testing.T) {
	rep := &Report{RecordFQDN: "x.example", Source: "test:s"}
	rep.Add(SeverityOK, "OK_CODE", "all good")
	rep.Add(SeverityWarn, "WARN_CODE", "drift detected")
	out := rep.String()
	// Warn must appear before OK in the rendered output.
	if strings.Index(out, "WARN_CODE") > strings.Index(out, "OK_CODE") {
		t.Errorf("warn should be listed before ok:\n%s", out)
	}
	if !strings.Contains(out, "x.example") {
		t.Errorf("missing FQDN in output")
	}
}

func TestProviderSource_NilProtected(t *testing.T) {
	ps := ProviderSource{}
	if ps.Name() != "provider:<nil>" {
		t.Errorf("nil source name: %s", ps.Name())
	}
	if _, err := ps.GetHTTPSRDATA(context.Background(), "z", "n"); !errors.Is(err, errors.New("verify: ProviderSource.P is nil")) && err == nil {
		t.Errorf("expected error on nil P")
	}
}
