package svcb

import (
	"strings"
	"testing"
)

func TestParse_ServiceMode_Typical(t *testing.T) {
	in := `1 . alpn="h2,http/1.1" ipv4hint="1.2.3.4" ech="AEX+DQBB"`
	r, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Priority != 1 {
		t.Errorf("Priority = %d, want 1", r.Priority)
	}
	if r.Target != "." {
		t.Errorf("Target = %q, want .", r.Target)
	}
	if v, ok := r.GetParam("alpn"); !ok || v != "h2,http/1.1" {
		t.Errorf("alpn = %q,%v", v, ok)
	}
	if v, ok := r.GetParam("ipv4hint"); !ok || v != "1.2.3.4" {
		t.Errorf("ipv4hint = %q,%v", v, ok)
	}
	if v, ok := GetECH(r); !ok || v != "AEX+DQBB" {
		t.Errorf("ech = %q,%v", v, ok)
	}
}

func TestParse_AliasMode(t *testing.T) {
	r, err := Parse(`0 cdn.example.net.`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !r.IsAliasMode() {
		t.Errorf("expected AliasMode")
	}
	if r.Target != "cdn.example.net." {
		t.Errorf("Target = %q", r.Target)
	}
	if len(r.Params) != 0 {
		t.Errorf("AliasMode should have no params, got %v", r.Params)
	}
}

func TestParse_BareValue(t *testing.T) {
	r, err := Parse(`1 . port=8443 ech=AEX`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, _ := r.GetParam("port"); v != "8443" {
		t.Errorf("port = %q", v)
	}
	if v, _ := r.GetParam("ech"); v != "AEX" {
		t.Errorf("ech = %q", v)
	}
}

func TestParse_KeylessParam(t *testing.T) {
	r, err := Parse(`1 . no-default-alpn alpn="h3"`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, ok := r.GetParam("no-default-alpn"); !ok || v != "" {
		t.Errorf("no-default-alpn = %q,%v", v, ok)
	}
	if v, _ := r.GetParam("alpn"); v != "h3" {
		t.Errorf("alpn = %q", v)
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []string{
		``,
		`abc . ech="x"`,            // non-numeric priority
		`1 . ech="missing-quote`,   // unterminated quote
		`1 . =val`,                 // empty key
	}
	for _, c := range cases {
		if _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q): expected error", c)
		}
	}
}

func TestSetECH_PreservesOtherParams(t *testing.T) {
	in := `1 . alpn="h2,h3" ipv4hint="1.2.3.4" ech="OLD"`
	r, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := SetECH(r, "NEW")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := out.GetParam("ech"); v != "NEW" {
		t.Errorf("ech = %q", v)
	}
	if v, _ := out.GetParam("alpn"); v != "h2,h3" {
		t.Errorf("alpn lost: %q", v)
	}
	if v, _ := out.GetParam("ipv4hint"); v != "1.2.3.4" {
		t.Errorf("ipv4hint lost: %q", v)
	}
	// Original record must NOT be mutated.
	if v, _ := r.GetParam("ech"); v != "OLD" {
		t.Errorf("original record was mutated: ech = %q", v)
	}
}

func TestSetECH_AppendsWhenAbsent(t *testing.T) {
	r, _ := Parse(`1 . alpn="h2"`)
	out, err := SetECH(r, "AEX")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := out.GetParam("ech"); v != "AEX" {
		t.Errorf("ech = %q", v)
	}
	// alpn should still be present and come first (stable order).
	if out.Params[0].Key != "alpn" {
		t.Errorf("expected alpn first, got %v", out.Params)
	}
}

func TestSetECH_RejectsAliasMode(t *testing.T) {
	r, _ := Parse(`0 cdn.example.net.`)
	if _, err := SetECH(r, "AEX"); err == nil {
		t.Errorf("expected error on AliasMode")
	}
}

func TestRoundTrip(t *testing.T) {
	in := `1 . alpn="h2,http/1.1" ech="AEX+DQBB"`
	r, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	got := r.String()
	// Re-parse and compare semantically.
	r2, err := Parse(got)
	if err != nil {
		t.Fatalf("re-parse %q: %v", got, err)
	}
	if r2.Priority != r.Priority || r2.Target != r.Target {
		t.Errorf("priority/target drift: %v vs %v", r2, r)
	}
	for _, p := range r.Params {
		if v, _ := r2.GetParam(p.Key); v != p.Value {
			t.Errorf("param %s drift: %q vs %q", p.Key, v, p.Value)
		}
	}
	// Output should be quoted for determinism.
	if !strings.Contains(got, `ech="`) {
		t.Errorf("expected ech to be quoted in output, got %q", got)
	}
}
