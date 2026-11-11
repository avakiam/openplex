package extensions

import (
	"crypto/ed25519"
	"encoding/base64"
)

// trustedKeys are the public keys whose signatures mark a package as made
// by the OpenPlex project (Avakiam). The private keys are kept offline.
var trustedKeys = map[string]string{
	"avakiam-2026": "yyznJhRAeKanRpxzTEzd+IBAkXLzxRyJrxqngB9bXpI=",
}

// TrustedKeys returns the decoded trusted keys.
func TrustedKeys() map[string]ed25519.PublicKey {
	out := map[string]ed25519.PublicKey{}
	for id, k := range trustedKeys {
		if b, err := base64.StdEncoding.DecodeString(k); err == nil && len(b) == ed25519.PublicKeySize {
			out[id] = ed25519.PublicKey(b)
		}
	}
	return out
}
