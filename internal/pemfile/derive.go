package pemfile

import (
	"fmt"

	circlhpke "github.com/cloudflare/circl/hpke"
	"github.com/justinwoo280/ech-keymgr/internal/hpke"
)

// derivePublicViaCIRCL recovers the wire-form HPKE public key from a
// raw private key using CIRCL's KEM scheme machinery.
//
// We isolate this dependency in its own file so the rest of pemfile
// has no direct CIRCL import — keeping imports symmetrical with
// internal/hpke, which is the only other place CIRCL appears.
func derivePublicViaCIRCL(kemID uint16, privBytes []byte) ([]byte, error) {
	var kem circlhpke.KEM
	switch kemID {
	case hpke.KEMX25519HKDFSHA256:
		kem = circlhpke.KEM_X25519_HKDF_SHA256
	default:
		return nil, fmt.Errorf("pemfile: cannot derive public key for KEM 0x%04x", kemID)
	}
	priv, err := kem.Scheme().UnmarshalBinaryPrivateKey(privBytes)
	if err != nil {
		return nil, fmt.Errorf("pemfile: unmarshal private key: %w", err)
	}
	pub, err := priv.Public().MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("pemfile: marshal derived public key: %w", err)
	}
	return pub, nil
}
