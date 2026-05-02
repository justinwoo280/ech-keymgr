// Package echconfig implements RFC 9849 §4 ECHConfig and ECHConfigList
// binary encode / decode using TLS Presentation Language (cryptobyte).
//
// This package is the wire-format authority for ech-keymgr. It is
// deliberately kept narrow: it only handles the version-0xFE0D ECH
// configuration (the only version standardized by RFC 9849 in March
// 2026). Unknown ECHConfig versions in an ECHConfigList are
// preserved as opaque bytes during decode → re-encode round-trips so
// we never silently drop a forward-compatible entry contributed by
// future protocol revisions.
//
// References:
//   - RFC 9849 §4 — Encrypted ClientHello Configuration
//   - RFC 9180   — HPKE (codepoints for KEM/KDF/AEAD)
//   - https://www.rfc-editor.org/rfc/rfc9849.html
//
// Wire format (RFC 9849 §4):
//
//	struct {
//	    uint16 version;
//	    uint16 length;          // length of contents in bytes
//	    select (version) {
//	      case 0xfe0d:          // ECHConfigContents
//	        uint8         config_id;
//	        HpkeKemId     kem_id;
//	        HpkePublicKey public_key;            // <1..2^16-1>
//	        HpkeSymmetricCipherSuite cipher_suites<4..2^16-4>;
//	        uint8         maximum_name_length;
//	        opaque        public_name<1..255>;
//	        Extension     extensions<0..2^16-1>;
//	    }
//	} ECHConfig;
//
//	ECHConfig ECHConfigList<4..2^16-1>;        // length-prefixed list
package echconfig
