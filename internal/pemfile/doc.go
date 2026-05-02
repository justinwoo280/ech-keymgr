// Package pemfile reads and writes ".ech" PEM files in the format
// defined by draft-farrell-tls-pemesni and consumed by every
// ECH-enabled server we know of (NGINX 1.29.4+, Apache, lighttpd,
// HAProxy via the DEfO patches).
//
// The format is two PEM blocks concatenated in a single file:
//
//	-----BEGIN PRIVATE KEY-----
//	<PKCS#8 encoding of the raw HPKE private key>
//	-----END PRIVATE KEY-----
//	-----BEGIN ECHCONFIG-----
//	<base64 of the ECHConfigList wire bytes>
//	-----END ECHCONFIG-----
//
// "ECHCONFIG" is intentionally singular: the block contains the
// ECHConfigList (which itself is a list); the singular spelling is
// what every existing implementation uses.
//
// We accept (on read) both block orderings — some tooling emits
// ECHCONFIG first — but always emit private key first on write,
// matching the upstream IETF draft and the OpenSSL ECH branch.
//
// The PRIVATE KEY block is a PKCS#8 PrivateKeyInfo wrapping the raw
// HPKE private key octets. For DHKEM(X25519, HKDF-SHA256), the raw
// key is 32 bytes and we emit it under the OID for X25519
// (1.3.101.110), which is the form OpenSSL's ECH branch expects.
package pemfile
