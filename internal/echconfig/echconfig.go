package echconfig

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/cryptobyte"
)

// Version is the wire codepoint for the version of the ECH
// extension we implement. RFC 9849 fixes this at 0xFE0D and uses the
// same value for the TLS extension type.
const Version uint16 = 0xFE0D

// HPKE codepoints we care about (full registry lives in RFC 9180).
const (
	KEMX25519HKDFSHA256 uint16 = 0x0020

	KDFHKDFSHA256 uint16 = 0x0001

	AEADAES128GCM        uint16 = 0x0001
	AEADAES256GCM        uint16 = 0x0002
	AEADChaCha20Poly1305 uint16 = 0x0003
)

// CipherSuite is the (KDF, AEAD) pair an ECH client may use to
// encrypt the ClientHelloInner under the published HPKE public key.
type CipherSuite struct {
	KDF  uint16
	AEAD uint16
}

// Extension is an ECHConfig extension as defined in RFC 9849 §4.2.
//
// Critical extensions (high bit of Type set) MUST be understood by a
// client; this library does NOT validate that, since we only generate
// configs that we ourselves wrote — we never assemble unknown
// extensions on the encode path.
type Extension struct {
	Type uint16
	Data []byte
}

// Config is the parsed view of a single ECHConfig in the
// version 0xFE0D ECHConfigContents form (RFC 9849 §4).
//
// Length and validity constraints (1 ≤ len(PublicKey) ≤ 65535,
// 1 ≤ len(PublicName) ≤ 255, etc.) are enforced by Marshal /
// Unmarshal; callers are not expected to validate them themselves.
type Config struct {
	ConfigID          uint8
	KEMID             uint16
	PublicKey         []byte
	CipherSuites      []CipherSuite
	MaximumNameLength uint8
	PublicName        []byte // ASCII / U-label bytes
	Extensions        []Extension
}

// List is an ECHConfigList per RFC 9849 §4: a length-prefixed
// sequence of ECHConfig entries, ordered by decreasing client
// preference (i.e. the first entry is the one we want clients to
// pick first).
//
// Entries with versions other than 0xFE0D are preserved as opaque
// bytes inside Unknown so a future protocol bump won't be silently
// stripped on round-trip.
type List struct {
	Configs []Config
	Unknown []UnknownEntry
}

// UnknownEntry preserves the raw wire bytes of any non-0xFE0D
// ECHConfig encountered during Unmarshal so it can be re-emitted
// verbatim by Marshal. The on-the-wire ordering relative to
// Configs is NOT preserved (we always emit known configs first).
type UnknownEntry struct {
	Version uint16
	Body    []byte // contents only, NOT including the leading version+length
}

// MarshalBinary serializes a single Config into the on-the-wire
// ECHConfig bytes (16-bit version + 16-bit length + body).
func (c *Config) MarshalBinary() ([]byte, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	var b cryptobyte.Builder
	b.AddUint16(Version)
	b.AddUint16LengthPrefixed(func(body *cryptobyte.Builder) {
		body.AddUint8(c.ConfigID)
		body.AddUint16(c.KEMID)
		body.AddUint16LengthPrefixed(func(pk *cryptobyte.Builder) {
			pk.AddBytes(c.PublicKey)
		})
		body.AddUint16LengthPrefixed(func(cs *cryptobyte.Builder) {
			for _, suite := range c.CipherSuites {
				cs.AddUint16(suite.KDF)
				cs.AddUint16(suite.AEAD)
			}
		})
		body.AddUint8(c.MaximumNameLength)
		body.AddUint8LengthPrefixed(func(pn *cryptobyte.Builder) {
			pn.AddBytes(c.PublicName)
		})
		body.AddUint16LengthPrefixed(func(ext *cryptobyte.Builder) {
			for _, e := range c.Extensions {
				ext.AddUint16(e.Type)
				ext.AddUint16LengthPrefixed(func(ed *cryptobyte.Builder) {
					ed.AddBytes(e.Data)
				})
			}
		})
	})
	return b.Bytes()
}

// UnmarshalBinary parses one ECHConfig from raw, version-prefixed wire
// bytes. It rejects any version other than 0xFE0D — for forward-
// compatible parsing of mixed lists, use UnmarshalList.
func (c *Config) UnmarshalBinary(data []byte) error {
	s := cryptobyte.String(data)
	var version uint16
	if !s.ReadUint16(&version) {
		return errors.New("echconfig: short read on version")
	}
	if version != Version {
		return fmt.Errorf("echconfig: unsupported version 0x%04x (want 0x%04x)", version, Version)
	}
	var body cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&body) {
		return errors.New("echconfig: short read on length-prefixed body")
	}
	if !s.Empty() {
		return errors.New("echconfig: trailing bytes after ECHConfig")
	}
	return c.parseBody(&body)
}

// parseBody parses an ECHConfigContents body (everything after the
// version + length prefix) according to RFC 9849 §4.
func (c *Config) parseBody(s *cryptobyte.String) error {
	if !s.ReadUint8(&c.ConfigID) {
		return errors.New("echconfig: short read config_id")
	}
	if !s.ReadUint16(&c.KEMID) {
		return errors.New("echconfig: short read kem_id")
	}
	var pk cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&pk) {
		return errors.New("echconfig: short read public_key")
	}
	c.PublicKey = append([]byte(nil), pk...)

	var cs cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&cs) {
		return errors.New("echconfig: short read cipher_suites")
	}
	if cs.Empty() || len(cs)%4 != 0 {
		return errors.New("echconfig: cipher_suites length not a multiple of 4 / empty")
	}
	for !cs.Empty() {
		var suite CipherSuite
		if !cs.ReadUint16(&suite.KDF) || !cs.ReadUint16(&suite.AEAD) {
			return errors.New("echconfig: short read cipher_suite entry")
		}
		c.CipherSuites = append(c.CipherSuites, suite)
	}

	if !s.ReadUint8(&c.MaximumNameLength) {
		return errors.New("echconfig: short read maximum_name_length")
	}
	var pn cryptobyte.String
	if !s.ReadUint8LengthPrefixed(&pn) {
		return errors.New("echconfig: short read public_name")
	}
	if len(pn) == 0 {
		return errors.New("echconfig: public_name must be non-empty (RFC 9849 §4)")
	}
	c.PublicName = append([]byte(nil), pn...)

	var ext cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&ext) {
		return errors.New("echconfig: short read extensions")
	}
	for !ext.Empty() {
		var e Extension
		var ed cryptobyte.String
		if !ext.ReadUint16(&e.Type) || !ext.ReadUint16LengthPrefixed(&ed) {
			return errors.New("echconfig: short read extension entry")
		}
		e.Data = append([]byte(nil), ed...)
		c.Extensions = append(c.Extensions, e)
	}
	if !s.Empty() {
		return errors.New("echconfig: trailing bytes inside ECHConfig body")
	}
	return nil
}

func (c *Config) validate() error {
	if len(c.PublicKey) == 0 || len(c.PublicKey) > 0xFFFF {
		return fmt.Errorf("echconfig: public_key length %d out of bounds [1..65535]", len(c.PublicKey))
	}
	if len(c.CipherSuites) == 0 {
		return errors.New("echconfig: at least one cipher_suite is required")
	}
	if len(c.PublicName) == 0 || len(c.PublicName) > 255 {
		return fmt.Errorf("echconfig: public_name length %d out of bounds [1..255]", len(c.PublicName))
	}
	return nil
}

// MarshalList encodes l as an ECHConfigList: a uint16-prefixed
// concatenation of every ECHConfig (known + unknown) in l. RFC 9849
// requires the prefixed length to be at least 4.
//
// Encoding order: every entry from l.Configs first, in the order
// they appear, followed by every entry from l.Unknown.
func MarshalList(l *List) ([]byte, error) {
	var entries [][]byte
	for i := range l.Configs {
		raw, err := l.Configs[i].MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("echconfig: encode entry %d: %w", i, err)
		}
		entries = append(entries, raw)
	}
	for _, u := range l.Unknown {
		var b cryptobyte.Builder
		b.AddUint16(u.Version)
		b.AddUint16LengthPrefixed(func(body *cryptobyte.Builder) {
			body.AddBytes(u.Body)
		})
		raw, err := b.Bytes()
		if err != nil {
			return nil, err
		}
		entries = append(entries, raw)
	}
	if len(entries) == 0 {
		return nil, errors.New("echconfig: refuse to marshal an empty ECHConfigList")
	}

	var outer cryptobyte.Builder
	outer.AddUint16LengthPrefixed(func(inner *cryptobyte.Builder) {
		for _, e := range entries {
			inner.AddBytes(e)
		}
	})
	return outer.Bytes()
}

// UnmarshalList parses an ECHConfigList. Entries with version
// 0xFE0D are decoded into List.Configs; entries with any other
// version are preserved verbatim in List.Unknown so a round-trip
// through Marshal/Unmarshal does not silently drop them.
func UnmarshalList(data []byte) (*List, error) {
	s := cryptobyte.String(data)
	var inner cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&inner) {
		return nil, errors.New("echconfig: short read on outer length")
	}
	if !s.Empty() {
		return nil, errors.New("echconfig: trailing bytes after ECHConfigList")
	}
	if len(inner) < 4 {
		return nil, fmt.Errorf("echconfig: ECHConfigList contents too short (%d < 4)", len(inner))
	}

	var l List
	for !inner.Empty() {
		var version uint16
		if !inner.ReadUint16(&version) {
			return nil, errors.New("echconfig: short read entry version")
		}
		var body cryptobyte.String
		if !inner.ReadUint16LengthPrefixed(&body) {
			return nil, errors.New("echconfig: short read entry length")
		}
		if version == Version {
			var c Config
			if err := c.parseBody(&body); err != nil {
				return nil, fmt.Errorf("echconfig: decode entry: %w", err)
			}
			l.Configs = append(l.Configs, c)
		} else {
			l.Unknown = append(l.Unknown, UnknownEntry{
				Version: version,
				Body:    append([]byte(nil), body...),
			})
		}
	}
	return &l, nil
}
