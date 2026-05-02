package pemfile

import (
	"bytes"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/justinwoo280/ech-keymgr/internal/echconfig"
	"github.com/justinwoo280/ech-keymgr/internal/hpke"
)

// PEM block type names per draft-farrell-tls-pemesni.
const (
	blockTypePrivateKey = "PRIVATE KEY"
	blockTypeECHConfig  = "ECHCONFIG"
)

// oidX25519 is the algorithm OID for X25519 keys per RFC 8410.
// 1.3.101.110
var oidX25519 = asn1.ObjectIdentifier{1, 3, 101, 110}

// File is the in-memory view of a parsed .ech file.
//
// KeyPair.PrivateKey holds raw HPKE private key octets; PKCS#8
// wrapping/unwrapping happens transparently inside Marshal/Parse.
//
// ConfigListBytes holds the raw ECHConfigList wire bytes (NOT
// base64). Use the echconfig package to inspect them.
type File struct {
	KeyPair         *hpke.KeyPair
	ConfigListBytes []byte
}

// Marshal returns the textual .ech file representation of f.
//
// We always emit private key first, base64-wrapped at 64 chars per
// line as required by RFC 7468.
func Marshal(f *File) ([]byte, error) {
	if f == nil || f.KeyPair == nil || len(f.ConfigListBytes) == 0 {
		return nil, errors.New("pemfile: incomplete File (missing key pair or config list)")
	}
	pkcs8, err := wrapPKCS8(f.KeyPair)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	if err := pem.Encode(&out, &pem.Block{
		Type:  blockTypePrivateKey,
		Bytes: pkcs8,
	}); err != nil {
		return nil, fmt.Errorf("pemfile: encode private key block: %w", err)
	}
	if err := pem.Encode(&out, &pem.Block{
		Type:  blockTypeECHConfig,
		Bytes: f.ConfigListBytes,
	}); err != nil {
		return nil, fmt.Errorf("pemfile: encode ECHCONFIG block: %w", err)
	}
	return out.Bytes(), nil
}

// Parse reads a .ech file. It tolerates either block order, leading
// or trailing whitespace, and base64-encoded ECHCONFIG bodies (some
// tooling emits the body double-base64-wrapped — pem.Decode handles
// the outer layer; if the inner bytes don't look like a valid
// ECHConfigList we attempt one extra base64 decode).
func Parse(raw []byte) (*File, error) {
	var (
		keyBlock *pem.Block
		echBlock *pem.Block
	)
	rest := raw
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		switch strings.ToUpper(blk.Type) {
		case blockTypePrivateKey:
			if keyBlock != nil {
				return nil, errors.New("pemfile: multiple PRIVATE KEY blocks")
			}
			keyBlock = blk
		case blockTypeECHConfig:
			if echBlock != nil {
				return nil, errors.New("pemfile: multiple ECHCONFIG blocks")
			}
			echBlock = blk
		default:
			// Ignore unknown blocks — keeps us forward-compatible
			// with future tooling that may add e.g. comments.
		}
	}
	if keyBlock == nil {
		return nil, errors.New("pemfile: missing PRIVATE KEY block")
	}
	if echBlock == nil {
		return nil, errors.New("pemfile: missing ECHCONFIG block")
	}
	kp, err := unwrapPKCS8(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	cfgBytes := echBlock.Bytes
	// Some encoders inadvertently double-base64 the ECHCONFIG body.
	// Try once more if the bytes look like base64 ASCII.
	if looksLikeBase64(cfgBytes) {
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(cfgBytes))); err == nil {
			cfgBytes = dec
		}
	}
	return &File{
		KeyPair:         kp,
		ConfigListBytes: cfgBytes,
	}, nil
}

// ParseFromString is a convenience wrapper around Parse for callers
// that have the file contents as a string (e.g. from config).
func ParseFromString(s string) (*File, error) { return Parse([]byte(s)) }

// ECHConfigBase64 returns the base64-encoded ECHConfigList suitable
// for direct insertion into the `ech=` SvcParam of an HTTPS RR.
func (f *File) ECHConfigBase64() string {
	return base64.StdEncoding.EncodeToString(f.ConfigListBytes)
}

// ----------------------------------------------------------------
// PKCS#8 wrapping for raw HPKE private keys
// ----------------------------------------------------------------

// pkcs8 mirrors the PKCS#8 PrivateKeyInfo ASN.1 structure (RFC 5208).
type pkcs8 struct {
	Version    int
	Algo       pkcs8AlgID
	PrivateKey []byte
}

type pkcs8AlgID struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

// wrapPKCS8 wraps the raw HPKE private key octets in a PKCS#8
// PrivateKeyInfo structure. For X25519 the inner value is the
// curve scalar wrapped in an OCTET STRING (RFC 8410 §7).
func wrapPKCS8(kp *hpke.KeyPair) ([]byte, error) {
	switch kp.KEMID {
	case hpke.KEMX25519HKDFSHA256:
		// RFC 8410: PrivateKey is an OCTET STRING whose value is
		// the raw 32-byte scalar.
		inner, err := asn1.Marshal(kp.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("pemfile: marshal X25519 inner OCTET STRING: %w", err)
		}
		p := pkcs8{
			Version:    0,
			Algo:       pkcs8AlgID{Algorithm: oidX25519},
			PrivateKey: inner,
		}
		return asn1.Marshal(p)
	default:
		return nil, fmt.Errorf("pemfile: PKCS#8 wrapping not supported for KEM 0x%04x", kp.KEMID)
	}
}

// unwrapPKCS8 reverses wrapPKCS8, returning a populated hpke.KeyPair
// (with PublicKey re-derived from the private key).
func unwrapPKCS8(der []byte) (*hpke.KeyPair, error) {
	var p pkcs8
	if _, err := asn1.Unmarshal(der, &p); err != nil {
		return nil, fmt.Errorf("pemfile: parse PKCS#8: %w", err)
	}
	if !p.Algo.Algorithm.Equal(oidX25519) {
		return nil, fmt.Errorf("pemfile: unsupported PKCS#8 algorithm OID %v (only X25519 is supported)", p.Algo.Algorithm)
	}
	var raw []byte
	if _, err := asn1.Unmarshal(p.PrivateKey, &raw); err != nil {
		return nil, fmt.Errorf("pemfile: parse inner OCTET STRING: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("pemfile: X25519 private key length %d != 32", len(raw))
	}
	kp := &hpke.KeyPair{
		KEMID:      hpke.KEMX25519HKDFSHA256,
		PrivateKey: raw,
	}
	// Re-derive public key so callers always get a complete pair.
	if err := derivePublic(kp); err != nil {
		return nil, err
	}
	return kp, nil
}

// derivePublic populates kp.PublicKey from kp.PrivateKey using the
// hpke wrapper's validation path (which round-trips through CIRCL).
//
// We avoid pulling another curve library in here: the cleanest way
// is to ask CIRCL to unmarshal the private key, then ask it for
// .Public().
func derivePublic(kp *hpke.KeyPair) error {
	// Use a dummy KeyPair with derived pub == private's public.
	// The hpke package doesn't expose a direct "derive public"
	// helper because we usually have both halves; we add a tiny
	// stub here that uses ValidateKeyPair's machinery.
	derived, err := derivePublicViaCIRCL(kp.KEMID, kp.PrivateKey)
	if err != nil {
		return err
	}
	kp.PublicKey = derived
	return nil
}

// looksLikeBase64 returns true if b consists only of ASCII base64
// alphabet characters (and whitespace), suggesting it might be
// double-base64-encoded ECHCONFIG payload.
func looksLikeBase64(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	for _, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '+', c == '/', c == '=', c == '\n', c == '\r', c == ' ', c == '\t':
		default:
			return false
		}
	}
	return true
}

// VerifyECHConfigList sanity-checks that f.ConfigListBytes parse as a
// valid ECHConfigList (and at least one entry references the embedded
// HPKE public key). Useful as a pre-write integrity check.
func (f *File) VerifyECHConfigList() error {
	if f == nil || f.KeyPair == nil {
		return errors.New("pemfile: nil File")
	}
	list, err := echconfig.UnmarshalList(f.ConfigListBytes)
	if err != nil {
		return fmt.Errorf("pemfile: ECHConfigList does not parse: %w", err)
	}
	for _, c := range list.Configs {
		if bytes.Equal(c.PublicKey, f.KeyPair.PublicKey) {
			return nil
		}
	}
	return errors.New("pemfile: no ECHConfig in the list references the embedded HPKE public key")
}
