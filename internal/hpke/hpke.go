package hpke

import (
	"errors"
	"fmt"

	circlhpke "github.com/cloudflare/circl/hpke"
)

// KEMID is the HPKE KEM codepoint (RFC 9180 §7.1) we support.
const KEMX25519HKDFSHA256 uint16 = 0x0020

// KeyPair is an HPKE key pair, stored as the marshaled wire bytes
// for both halves. We intentionally don't expose the in-memory
// PublicKey / PrivateKey types from CIRCL: callers only ever need
// raw bytes (to embed in ECHConfig and to write to .ech PEM files).
type KeyPair struct {
	KEMID      uint16
	PublicKey  []byte // raw form; for X25519 this is 32 bytes
	PrivateKey []byte // raw scalar; for X25519 this is 32 bytes
}

// GenerateKeyPair returns a fresh HPKE key pair for the given KEM.
// Currently the only supported KEM is X25519+HKDF-SHA256 (0x0020).
func GenerateKeyPair(kemID uint16) (*KeyPair, error) {
	scheme, err := scheme(kemID)
	if err != nil {
		return nil, err
	}
	pub, priv, err := scheme.Scheme().GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("hpke: GenerateKeyPair: %w", err)
	}
	pubBytes, err := pub.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("hpke: marshal public key: %w", err)
	}
	privBytes, err := priv.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("hpke: marshal private key: %w", err)
	}
	return &KeyPair{
		KEMID:      kemID,
		PublicKey:  pubBytes,
		PrivateKey: privBytes,
	}, nil
}

// PublicKeySize reports the wire size of a public key for the given
// KEM (handy for sanity checks before embedding into an ECHConfig).
func PublicKeySize(kemID uint16) (int, error) {
	s, err := scheme(kemID)
	if err != nil {
		return 0, err
	}
	return s.Scheme().PublicKeySize(), nil
}

// PrivateKeySize reports the wire size of a private key for the
// given KEM.
func PrivateKeySize(kemID uint16) (int, error) {
	s, err := scheme(kemID)
	if err != nil {
		return 0, err
	}
	return s.Scheme().PrivateKeySize(), nil
}

// scheme returns the CIRCL KEM enum value or an error for an
// unsupported KEM codepoint.
func scheme(kemID uint16) (circlhpke.KEM, error) {
	switch kemID {
	case KEMX25519HKDFSHA256:
		return circlhpke.KEM_X25519_HKDF_SHA256, nil
	default:
		return 0, fmt.Errorf("hpke: unsupported KEM 0x%04x", kemID)
	}
}

// ValidateKeyPair confirms that a (public, private) byte pair both
// parse against the named KEM and that the public key derived from
// the private key matches the supplied public key. Useful when
// loading a key pair from disk via the pemfile package.
func ValidateKeyPair(kp *KeyPair) error {
	if kp == nil {
		return errors.New("hpke: nil KeyPair")
	}
	s, err := scheme(kp.KEMID)
	if err != nil {
		return err
	}
	sch := s.Scheme()
	if len(kp.PublicKey) != sch.PublicKeySize() {
		return fmt.Errorf("hpke: public key length %d != expected %d", len(kp.PublicKey), sch.PublicKeySize())
	}
	if len(kp.PrivateKey) != sch.PrivateKeySize() {
		return fmt.Errorf("hpke: private key length %d != expected %d", len(kp.PrivateKey), sch.PrivateKeySize())
	}
	priv, err := sch.UnmarshalBinaryPrivateKey(kp.PrivateKey)
	if err != nil {
		return fmt.Errorf("hpke: unmarshal private key: %w", err)
	}
	derivedPub, err := priv.Public().MarshalBinary()
	if err != nil {
		return fmt.Errorf("hpke: re-marshal derived public key: %w", err)
	}
	// Constant-time compare not required: public keys are public.
	if string(derivedPub) != string(kp.PublicKey) {
		return errors.New("hpke: public key does not match derived value from private key")
	}
	return nil
}
