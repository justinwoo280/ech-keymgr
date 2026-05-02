package echconfig

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

// makeConfig returns a small but valid ECHConfig for tests.
func makeConfig(id uint8, name string) Config {
	return Config{
		ConfigID:  id,
		KEMID:     KEMX25519HKDFSHA256,
		PublicKey: bytes.Repeat([]byte{0xAB}, 32), // X25519 = 32 bytes
		CipherSuites: []CipherSuite{
			{KDF: KDFHKDFSHA256, AEAD: AEADAES128GCM},
			{KDF: KDFHKDFSHA256, AEAD: AEADChaCha20Poly1305},
		},
		MaximumNameLength: 16,
		PublicName:        []byte(name),
	}
}

func TestRoundTrip_Single(t *testing.T) {
	in := makeConfig(0x42, "example.com")
	enc, err := in.MarshalBinary()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out Config
	if err := out.UnmarshalBinary(enc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.ConfigID != in.ConfigID || out.KEMID != in.KEMID {
		t.Errorf("header drift: %+v", out)
	}
	if !bytes.Equal(out.PublicKey, in.PublicKey) {
		t.Errorf("public_key drift")
	}
	if len(out.CipherSuites) != 2 || out.CipherSuites[1].AEAD != AEADChaCha20Poly1305 {
		t.Errorf("cipher_suites drift: %+v", out.CipherSuites)
	}
	if !bytes.Equal(out.PublicName, in.PublicName) {
		t.Errorf("public_name drift: %q", out.PublicName)
	}
}

func TestRoundTrip_List(t *testing.T) {
	l := &List{Configs: []Config{
		makeConfig(0x01, "a.example"),
		makeConfig(0x02, "b.example"),
		makeConfig(0x03, "c.example"),
	}}
	raw, err := MarshalList(l)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalList(raw)
	if err != nil {
		t.Fatalf("UnmarshalList: %v", err)
	}
	if len(got.Configs) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got.Configs))
	}
	for i, c := range got.Configs {
		if c.ConfigID != l.Configs[i].ConfigID {
			t.Errorf("entry %d ConfigID drift", i)
		}
	}
	if len(got.Unknown) != 0 {
		t.Errorf("unexpected unknown entries: %v", got.Unknown)
	}
}

func TestUnknownVersion_PreservedOnRoundTrip(t *testing.T) {
	known := makeConfig(0x10, "known.example")
	unknownBody := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	l := &List{
		Configs: []Config{known},
		Unknown: []UnknownEntry{
			{Version: 0x9999, Body: unknownBody},
		},
	}
	raw, err := MarshalList(l)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalList(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Configs) != 1 {
		t.Errorf("Configs count: %d", len(got.Configs))
	}
	if len(got.Unknown) != 1 || got.Unknown[0].Version != 0x9999 {
		t.Errorf("Unknown drift: %+v", got.Unknown)
	}
	if !bytes.Equal(got.Unknown[0].Body, unknownBody) {
		t.Errorf("Unknown body drift: %x", got.Unknown[0].Body)
	}
}

func TestMarshal_RejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"empty public_key", func(c *Config) { c.PublicKey = nil }},
		{"empty cipher_suites", func(c *Config) { c.CipherSuites = nil }},
		{"empty public_name", func(c *Config) { c.PublicName = nil }},
		{"public_name too long", func(c *Config) { c.PublicName = bytes.Repeat([]byte("a"), 256) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := makeConfig(0x01, "x.example")
			tc.mut(&c)
			if _, err := c.MarshalBinary(); err == nil {
				t.Errorf("expected error")
			}
		})
	}
}

func TestUnmarshal_DetectsTrailingGarbage(t *testing.T) {
	c := makeConfig(0x01, "x.example")
	enc, _ := c.MarshalBinary()
	enc = append(enc, 0xFF)
	var out Config
	if err := out.UnmarshalBinary(enc); err == nil {
		t.Errorf("expected error on trailing bytes")
	}
}

func TestUnmarshal_RejectsForeignVersion(t *testing.T) {
	// Version 0x1234 + length=0
	bad := []byte{0x12, 0x34, 0x00, 0x00}
	var out Config
	if err := out.UnmarshalBinary(bad); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("expected version error, got %v", err)
	}
}

func TestUnmarshalList_EmptyInner(t *testing.T) {
	// Outer length-prefixed body of 0 bytes — RFC requires ≥ 4.
	if _, err := UnmarshalList([]byte{0x00, 0x00}); err == nil {
		t.Errorf("expected error on too-short list")
	}
}

func TestMarshalList_RejectsEmpty(t *testing.T) {
	if _, err := MarshalList(&List{}); err == nil {
		t.Errorf("empty list should error")
	}
}

func TestStableEncoding(t *testing.T) {
	// Two identical Configs must encode to identical bytes.
	c1 := makeConfig(7, "stable.example")
	c2 := makeConfig(7, "stable.example")
	a, _ := c1.MarshalBinary()
	b, _ := c2.MarshalBinary()
	if !bytes.Equal(a, b) {
		t.Errorf("encoding not deterministic:\n  a=%s\n  b=%s", hex.EncodeToString(a), hex.EncodeToString(b))
	}
}
