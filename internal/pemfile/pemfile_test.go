package pemfile

import (
	"bytes"
	"strings"
	"testing"

	"github.com/justinwoo280/ech-keymgr/internal/echconfig"
	"github.com/justinwoo280/ech-keymgr/internal/hpke"
)

// makeFile builds an end-to-end .ech File: fresh HPKE key pair, one
// ECHConfig referencing its public key, wrapped in an ECHConfigList.
func makeFile(t *testing.T, publicName string) *File {
	t.Helper()
	kp, err := hpke.GenerateKeyPair(hpke.KEMX25519HKDFSHA256)
	if err != nil {
		t.Fatal(err)
	}
	cfg := echconfig.Config{
		ConfigID:  0x42,
		KEMID:     echconfig.KEMX25519HKDFSHA256,
		PublicKey: kp.PublicKey,
		CipherSuites: []echconfig.CipherSuite{
			{KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADAES128GCM},
		},
		MaximumNameLength: 32,
		PublicName:        []byte(publicName),
	}
	listBytes, err := echconfig.MarshalList(&echconfig.List{Configs: []echconfig.Config{cfg}})
	if err != nil {
		t.Fatal(err)
	}
	return &File{KeyPair: kp, ConfigListBytes: listBytes}
}

func TestMarshalParse_RoundTrip(t *testing.T) {
	f := makeFile(t, "example.com")
	pem, err := Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Parse(pem)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.KeyPair.KEMID != f.KeyPair.KEMID {
		t.Errorf("KEMID drift")
	}
	if !bytes.Equal(got.KeyPair.PrivateKey, f.KeyPair.PrivateKey) {
		t.Errorf("private key drift")
	}
	if !bytes.Equal(got.KeyPair.PublicKey, f.KeyPair.PublicKey) {
		t.Errorf("public key drift (derivation broken?)")
	}
	if !bytes.Equal(got.ConfigListBytes, f.ConfigListBytes) {
		t.Errorf("ECHConfigList bytes drift")
	}
}

func TestMarshal_OutputShape(t *testing.T) {
	f := makeFile(t, "example.com")
	pem, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	s := string(pem)
	if !strings.Contains(s, "-----BEGIN PRIVATE KEY-----") {
		t.Errorf("missing PRIVATE KEY armour")
	}
	if !strings.Contains(s, "-----BEGIN ECHCONFIG-----") {
		t.Errorf("missing ECHCONFIG armour")
	}
	// PRIVATE KEY must come before ECHCONFIG (matches OpenSSL ECH branch).
	if strings.Index(s, "BEGIN PRIVATE KEY") >= strings.Index(s, "BEGIN ECHCONFIG") {
		t.Errorf("PRIVATE KEY block must precede ECHCONFIG block")
	}
}

func TestParse_AcceptsReversedBlockOrder(t *testing.T) {
	f := makeFile(t, "example.com")
	pemBytes, _ := Marshal(f)
	// Swap the two blocks.
	parts := strings.SplitAfter(string(pemBytes), "-----END PRIVATE KEY-----\n")
	if len(parts) != 2 {
		t.Fatalf("unexpected PEM split: %d parts", len(parts))
	}
	swapped := parts[1] + parts[0]
	got, err := Parse([]byte(swapped))
	if err != nil {
		t.Fatalf("Parse on reversed-order PEM: %v", err)
	}
	if !bytes.Equal(got.ConfigListBytes, f.ConfigListBytes) {
		t.Errorf("config list drift on reversed-order PEM")
	}
}

func TestParse_RejectsMissingBlocks(t *testing.T) {
	if _, err := Parse([]byte("garbage")); err == nil {
		t.Errorf("expected error on garbage")
	}
	if _, err := Parse(nil); err == nil {
		t.Errorf("expected error on nil")
	}
}

func TestECHConfigBase64(t *testing.T) {
	f := makeFile(t, "example.com")
	b64 := f.ECHConfigBase64()
	if b64 == "" || strings.Contains(b64, "\n") {
		t.Errorf("base64 output suspect: %q", b64)
	}
	// Length should be ~ ceil(4/3 * len(raw))
	if len(b64) < (len(f.ConfigListBytes)*4/3) {
		t.Errorf("base64 too short")
	}
}

func TestVerifyECHConfigList_OK(t *testing.T) {
	f := makeFile(t, "example.com")
	if err := f.VerifyECHConfigList(); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestVerifyECHConfigList_DetectsMismatch(t *testing.T) {
	// Build a list whose ECHConfig points at a DIFFERENT public key.
	other, _ := hpke.GenerateKeyPair(hpke.KEMX25519HKDFSHA256)
	cfg := echconfig.Config{
		ConfigID:  1, KEMID: echconfig.KEMX25519HKDFSHA256,
		PublicKey: other.PublicKey,
		CipherSuites: []echconfig.CipherSuite{
			{KDF: echconfig.KDFHKDFSHA256, AEAD: echconfig.AEADAES128GCM},
		},
		MaximumNameLength: 16, PublicName: []byte("x.example"),
	}
	list, _ := echconfig.MarshalList(&echconfig.List{Configs: []echconfig.Config{cfg}})

	mine, _ := hpke.GenerateKeyPair(hpke.KEMX25519HKDFSHA256)
	f := &File{KeyPair: mine, ConfigListBytes: list}
	if err := f.VerifyECHConfigList(); err == nil {
		t.Errorf("expected mismatch error")
	}
}

func TestParse_DetectsDuplicateBlocks(t *testing.T) {
	f := makeFile(t, "example.com")
	pem, _ := Marshal(f)
	dup := append([]byte{}, pem...)
	dup = append(dup, pem...) // two copies
	if _, err := Parse(dup); err == nil {
		t.Errorf("expected error on duplicate blocks")
	}
}

func TestMarshal_RejectsIncomplete(t *testing.T) {
	if _, err := Marshal(nil); err == nil {
		t.Errorf("expected error on nil")
	}
	if _, err := Marshal(&File{KeyPair: nil}); err == nil {
		t.Errorf("expected error on missing key pair")
	}
}
