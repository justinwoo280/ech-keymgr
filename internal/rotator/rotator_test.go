package rotator

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/justinwoo280/ech-keymgr/internal/echconfig"
	"github.com/justinwoo280/ech-keymgr/internal/keystore"
	"github.com/justinwoo280/ech-keymgr/internal/reloader"
	"github.com/justinwoo280/ech-keymgr/pkg/dns"
	"github.com/justinwoo280/ech-keymgr/pkg/svcb"
)

// memProv is an in-memory dns.Provider built specifically for these
// tests so we can pre-seed an existing HTTPS RR (with extra
// SvcParams to verify they're preserved across rotation).
type memProv struct {
	mu      sync.Mutex
	records map[string][]string
	gets    int
	puts    int
}

func newMemProv() *memProv { return &memProv{records: map[string][]string{}} }

func (m *memProv) Name() string { return "memprov" }

func (m *memProv) GetHTTPSRDATA(_ context.Context, zone, name string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	rs, ok := m.records[zone+"|"+name]
	if !ok || len(rs) == 0 {
		return nil, dns.ErrRecordNotFound
	}
	out := make([]string, len(rs))
	copy(out, rs)
	return out, nil
}

func (m *memProv) PutHTTPSRDATA(_ context.Context, zone, name string, _ uint32, rdata []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts++
	cp := make([]string, len(rdata))
	copy(cp, rdata)
	m.records[zone+"|"+name] = cp
	return nil
}

func (m *memProv) DeleteHTTPSRDATA(_ context.Context, zone, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, zone+"|"+name)
	return nil
}

// fixedClock allows tests to advance "now" deterministically.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// noSleep skips R5's wait so tests run instantaneously.
func noSleep(_ context.Context, _ time.Duration) error { return nil }

// ----------------------------------------------------------------
// fixtures
// ----------------------------------------------------------------

const (
	testZone = "example.com"
	testFQDN = "hidden.example.com"
	testPub  = "example.com"
)

// setup builds a fresh Rotator wired to in-memory deps with the
// HTTPS RR already seeded so dns.GetHTTPSRDATA returns the expected
// extra SvcParams.
func setup(t *testing.T) (*Rotator, *memProv, *keystore.Store, *fixedClock) {
	t.Helper()
	store, err := keystore.OpenOrInit(t.TempDir(), testFQDN, testPub)
	if err != nil {
		t.Fatal(err)
	}
	prov := newMemProv()
	// Pre-seed existing HTTPS RR with an alpn we need to preserve.
	_ = prov.PutHTTPSRDATA(context.Background(), testZone, "hidden", 0,
		[]string{`1 . alpn="h2,h3"`})
	clk := &fixedClock{t: time.Date(2026, 5, 2, 7, 0, 0, 0, time.UTC)}
	r, err := New(Config{
		RecordFQDN:  testFQDN,
		PublicName:  testPub,
		DNSZone:     testZone,
		SettleDelay: 1 * time.Hour, // would be slow without noSleep
		GracePeriod: 6 * time.Hour,
		Clock:       clk,
		Sleep:       noSleep,
	}, Deps{
		Store:    store,
		DNS:      prov,
		Reloader: reloader.Noop{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, prov, store, clk
}

// ----------------------------------------------------------------
// tests
// ----------------------------------------------------------------

func TestNew_RequiresFields(t *testing.T) {
	cases := []Config{
		{PublicName: "p", DNSZone: "z"},  // no RecordFQDN
		{RecordFQDN: "r", DNSZone: "z"},  // no PublicName
		{RecordFQDN: "r", PublicName: "p"}, // no DNSZone
	}
	store, _ := keystore.OpenOrInit(t.TempDir(), "x", "x")
	deps := Deps{Store: store, DNS: newMemProv(), Reloader: reloader.Noop{}}
	for _, c := range cases {
		if _, err := New(c, deps); err == nil {
			t.Errorf("expected error on missing field: %+v", c)
		}
	}
}

func TestNew_RequiresAllDeps(t *testing.T) {
	c := Config{RecordFQDN: "r", PublicName: "p", DNSZone: "z"}
	if _, err := New(c, Deps{}); err == nil {
		t.Errorf("expected error on empty Deps")
	}
}

// TestRotate_FirstRotation exercises the cold-start path: an empty
// keystore goes through R1..R9 and ends up with one Current key in
// the store and DNS containing just that key's ECHConfigList.
func TestRotate_FirstRotation(t *testing.T) {
	r, prov, store, _ := setup(t)
	if err := r.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Keystore should hold exactly one key, in StateCurrent.
	lst := store.List()
	if len(lst) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(lst))
	}
	if lst[0].State != keystore.StateCurrent {
		t.Errorf("state = %q, want current", lst[0].State)
	}
	cur, err := store.Current()
	if err != nil {
		t.Fatal(err)
	}

	// DNS should contain a single HTTPS RR with the new ech= value
	// AND the originally-seeded alpn preserved.
	rs, err := prov.GetHTTPSRDATA(context.Background(), testZone, "hidden")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 RR, got %d", len(rs))
	}
	rec, _ := svcb.Parse(rs[0])
	if v, _ := rec.GetParam("alpn"); v != "h2,h3" {
		t.Errorf("alpn lost across rotation: %q", v)
	}
	echB64, ok := svcb.GetECH(rec)
	if !ok || echB64 == "" {
		t.Fatalf("ech= missing")
	}
	listBytes, err := base64.StdEncoding.DecodeString(echB64)
	if err != nil {
		t.Fatal(err)
	}
	got, err := echconfig.UnmarshalList(listBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Configs) != 1 {
		t.Errorf("expected 1 ECHConfig in list, got %d", len(got.Configs))
	}
	if got.Configs[0].ConfigID != cur.ConfigID {
		t.Errorf("DNS ECHConfig.config_id != keystore Current.config_id")
	}
}

// TestRotate_SecondRotation_OverlapsThenConverges proves R5 + R6 +
// R7 work in sequence: after the second rotation, DNS holds the
// latest ECHConfigList of length 1, the previously-current key is
// in Grace state, and its drop deadline is GracePeriod from now.
func TestRotate_SecondRotation_OverlapsThenConverges(t *testing.T) {
	r, prov, store, clk := setup(t)
	if err := r.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, _ := store.Current()

	clk.Advance(3 * time.Hour) // simulate the rotation interval
	if err := r.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, _ := store.Current()
	if second.ConfigID == first.ConfigID {
		t.Fatalf("second rotation reused config_id %02x", first.ConfigID)
	}

	// Old key should now be StateGrace with drop deadline = now + GracePeriod.
	prevEntry, err := store.Lookup(first.ConfigID)
	if err != nil {
		t.Fatalf("first key missing from store: %v", err)
	}
	if prevEntry.State != keystore.StateGrace {
		t.Errorf("first key state = %q, want grace", prevEntry.State)
	}
	expectedDrop := clk.Now().Add(6 * time.Hour)
	if !prevEntry.ScheduledDropAt.Equal(expectedDrop) {
		t.Errorf("ScheduledDropAt = %v, want %v", prevEntry.ScheduledDropAt, expectedDrop)
	}

	// DNS should now contain ONLY the new key's ECHConfig (R6 already shrunk it).
	rs, _ := prov.GetHTTPSRDATA(context.Background(), testZone, "hidden")
	rec, _ := svcb.Parse(rs[0])
	echB64, _ := svcb.GetECH(rec)
	listBytes, _ := base64.StdEncoding.DecodeString(echB64)
	got, _ := echconfig.UnmarshalList(listBytes)
	if len(got.Configs) != 1 {
		t.Errorf("after R6, DNS ECHConfigList should have 1 entry; got %d", len(got.Configs))
	}
	if got.Configs[0].ConfigID != second.ConfigID {
		t.Errorf("DNS holds wrong key after R6")
	}
}

// TestRotate_PrunesOnceGraceExpires advances the clock past the
// grace period and verifies the next Rotate's R8 prunes the file.
func TestRotate_PrunesOnceGraceExpires(t *testing.T) {
	r, _, store, clk := setup(t)
	_ = r.Rotate(context.Background())
	first, _ := store.Current()
	_ = r.Rotate(context.Background()) // second rotation puts first in Grace

	// Advance well past the grace window and rotate once more.
	clk.Advance(7 * time.Hour)
	_ = r.Rotate(context.Background())

	if _, err := store.Lookup(first.ConfigID); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("expected first key to be pruned, got err=%v", err)
	}
}

// TestRotate_PreservesOtherSvcParams hardens the "we never touch
// alpn/ipv4hint/etc." invariant across multiple rotations.
func TestRotate_PreservesOtherSvcParams(t *testing.T) {
	r, prov, _, _ := setup(t)
	// Replace the seeded record with one carrying many params.
	_ = prov.PutHTTPSRDATA(context.Background(), testZone, "hidden", 0, []string{
		`1 . alpn="h2,h3" ipv4hint="1.2.3.4" port="443"`,
	})
	for i := 0; i < 3; i++ {
		if err := r.Rotate(context.Background()); err != nil {
			t.Fatalf("rotation %d: %v", i, err)
		}
	}
	rs, _ := prov.GetHTTPSRDATA(context.Background(), testZone, "hidden")
	rec, _ := svcb.Parse(rs[0])
	for _, k := range []string{"alpn", "ipv4hint", "port"} {
		if _, ok := rec.GetParam(k); !ok {
			t.Errorf("SvcParam %q lost across rotations: %q", k, rs[0])
		}
	}
}

// TestRotate_FailsWhenDNSRRMissing surfaces ErrRecordNotFound from
// the provider so the operator gets the "run init" hint.
func TestRotate_FailsWhenDNSRRMissing(t *testing.T) {
	store, _ := keystore.OpenOrInit(t.TempDir(), testFQDN, testPub)
	prov := newMemProv() // empty; no seeded RR
	r, _ := New(Config{
		RecordFQDN: testFQDN, PublicName: testPub, DNSZone: testZone,
		Sleep: noSleep,
	}, Deps{Store: store, DNS: prov, Reloader: reloader.Noop{}})

	err := r.Rotate(context.Background())
	if err == nil {
		t.Fatalf("expected error on missing RR")
	}
	if !errors.Is(err, dns.ErrRecordNotFound) {
		t.Errorf("expected ErrRecordNotFound chain, got: %v", err)
	}
}

// TestRotate_BusyRejectsConcurrent confirms ErrBusy is returned to
// a second goroutine that tries to Rotate while the first is mid-cycle.
func TestRotate_BusyRejectsConcurrent(t *testing.T) {
	r, _, _, _ := setup(t)
	// Replace Sleep with a blocker we control.
	gate := make(chan struct{})
	r.cfg.Sleep = func(ctx context.Context, _ time.Duration) error {
		<-gate
		return nil
	}
	errCh := make(chan error, 1)
	go func() { errCh <- r.Rotate(context.Background()) }()
	// Give the first goroutine a moment to acquire the lock.
	time.Sleep(50 * time.Millisecond)

	if err := r.Rotate(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("expected ErrBusy, got %v", err)
	}
	close(gate)
	if err := <-errCh; err != nil {
		t.Errorf("first Rotate failed: %v", err)
	}
}

// TestRelName covers the FQDN→relative owner conversion edge cases.
func TestRelName(t *testing.T) {
	cases := []struct{ fqdn, zone, want string }{
		{"example.com", "example.com", "@"},
		{"hidden.example.com", "example.com", "hidden"},
		{"a.b.example.com", "example.com", "a.b"},
		{"EXAMPLE.com.", "example.com", "@"},
	}
	for _, c := range cases {
		if got := relName(c.fqdn, c.zone); got != c.want {
			t.Errorf("relName(%q,%q)=%q want %q", c.fqdn, c.zone, got, c.want)
		}
	}
}

func TestPickConfigID_AvoidsCollision(t *testing.T) {
	// Direct unit test against a populated store.
	store, _ := keystore.OpenOrInit(t.TempDir(), "x", "x")
	for id := uint8(0); id < 10; id++ {
		if _, err := store.Add([]byte{byte(id)}, id); err != nil {
			t.Fatal(err)
		}
	}
	r := &Rotator{deps: Deps{Store: store}}
	for i := 0; i < 50; i++ {
		got, err := r.pickConfigID()
		if err != nil {
			t.Fatal(err)
		}
		if got < 10 {
			t.Errorf("got config_id %d, which collides with seeded entries", got)
		}
	}
}

// strings is used inside this file via grep — keep the import
// stable in case future tests want substring assertions.
var _ = strings.Contains
