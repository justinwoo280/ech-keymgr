package hpke

import (
	"bytes"
	"testing"
)

func TestGenerateKeyPair_X25519(t *testing.T) {
	kp, err := GenerateKeyPair(KEMX25519HKDFSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if kp.KEMID != KEMX25519HKDFSHA256 {
		t.Errorf("KEMID drift: 0x%04x", kp.KEMID)
	}
	if len(kp.PublicKey) != 32 {
		t.Errorf("public key size = %d (want 32)", len(kp.PublicKey))
	}
	if len(kp.PrivateKey) != 32 {
		t.Errorf("private key size = %d (want 32)", len(kp.PrivateKey))
	}
}

func TestGenerateKeyPair_RejectsUnknownKEM(t *testing.T) {
	if _, err := GenerateKeyPair(0xDEAD); err == nil {
		t.Errorf("expected error on unknown KEM")
	}
}

func TestPublicAndPrivateKeySize(t *testing.T) {
	pub, err := PublicKeySize(KEMX25519HKDFSHA256)
	if err != nil || pub != 32 {
		t.Errorf("PublicKeySize: %d, err=%v", pub, err)
	}
	priv, err := PrivateKeySize(KEMX25519HKDFSHA256)
	if err != nil || priv != 32 {
		t.Errorf("PrivateKeySize: %d, err=%v", priv, err)
	}
}

func TestValidateKeyPair_RoundTripOK(t *testing.T) {
	kp, _ := GenerateKeyPair(KEMX25519HKDFSHA256)
	if err := ValidateKeyPair(kp); err != nil {
		t.Errorf("ValidateKeyPair on freshly generated kp: %v", err)
	}
}

func TestValidateKeyPair_DetectsTamper(t *testing.T) {
	kp, _ := GenerateKeyPair(KEMX25519HKDFSHA256)
	// Flip one bit in the public key — derivation will mismatch.
	bad := *kp
	bad.PublicKey = append([]byte(nil), kp.PublicKey...)
	bad.PublicKey[0] ^= 0x01
	if err := ValidateKeyPair(&bad); err == nil {
		t.Errorf("expected mismatch error on tampered public key")
	}
}

func TestValidateKeyPair_RejectsWrongSize(t *testing.T) {
	kp, _ := GenerateKeyPair(KEMX25519HKDFSHA256)
	short := *kp
	short.PrivateKey = kp.PrivateKey[:16]
	if err := ValidateKeyPair(&short); err == nil {
		t.Errorf("expected size error")
	}
}

func TestUniqueness(t *testing.T) {
	a, _ := GenerateKeyPair(KEMX25519HKDFSHA256)
	b, _ := GenerateKeyPair(KEMX25519HKDFSHA256)
	if bytes.Equal(a.PrivateKey, b.PrivateKey) {
		t.Errorf("two consecutive private keys are identical — RNG broken?")
	}
}

func TestValidateKeyPair_NilSafe(t *testing.T) {
	if err := ValidateKeyPair(nil); err == nil {
		t.Errorf("expected error on nil")
	}
}
