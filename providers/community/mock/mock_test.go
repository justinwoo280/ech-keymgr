//go:build mock || community || all

package mock

import (
	"context"
	"errors"
	"testing"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

func TestRoundTrip(t *testing.T) {
	p, _ := New(nil)
	ctx := context.Background()

	if _, err := p.GetHTTPSRDATA(ctx, "example.com", "hidden"); !errors.Is(err, dns.ErrRecordNotFound) {
		t.Fatalf("initial Get should be NotFound, got %v", err)
	}

	in := []string{`1 . alpn="h2" ech="AEX"`}
	if err := p.PutHTTPSRDATA(ctx, "example.com", "hidden", 300, in); err != nil {
		t.Fatal(err)
	}
	got, err := p.GetHTTPSRDATA(ctx, "example.com", "hidden")
	if err != nil || len(got) != 1 || got[0] != in[0] {
		t.Errorf("Get=%v err=%v", got, err)
	}

	// Idempotent delete.
	if err := p.DeleteHTTPSRDATA(ctx, "example.com", "hidden"); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteHTTPSRDATA(ctx, "example.com", "hidden"); err != nil {
		t.Errorf("second Delete should be nil, got %v", err)
	}
	if _, err := p.GetHTTPSRDATA(ctx, "example.com", "hidden"); !errors.Is(err, dns.ErrRecordNotFound) {
		t.Errorf("after Delete should be NotFound")
	}
}

func TestPutReplacesPrevious(t *testing.T) {
	p, _ := New(nil)
	ctx := context.Background()
	_ = p.PutHTTPSRDATA(ctx, "example.com", "x", 300, []string{`1 . ech="OLD"`})
	_ = p.PutHTTPSRDATA(ctx, "example.com", "x", 300, []string{`1 . ech="NEW"`})
	got, _ := p.GetHTTPSRDATA(ctx, "example.com", "x")
	if len(got) != 1 || got[0] != `1 . ech="NEW"` {
		t.Errorf("got %v", got)
	}
}

func TestSnapshotIsCopy(t *testing.T) {
	p, _ := New(nil)
	mp := p.(*Provider)
	_ = mp.PutHTTPSRDATA(context.Background(), "example.com", "x", 0, []string{`a`})
	snap := mp.Snapshot()
	snap["example.com|x.example.com"][0] = "MUTATED"
	got, _ := mp.GetHTTPSRDATA(context.Background(), "example.com", "x")
	if got[0] != "a" {
		t.Errorf("Snapshot returned aliased state: %v", got)
	}
}

func TestRegisteredViaInit(t *testing.T) {
	f, ok := dns.Lookup("mock")
	if !ok {
		t.Fatal("mock provider not registered")
	}
	if _, err := f(nil); err != nil {
		t.Fatal(err)
	}
}
