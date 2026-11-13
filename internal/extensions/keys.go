package extensions

import (
	"crypto/ed25519"
	"encoding/base64"
)

// trustedKeys are the public keys OpenPlex trusts: avakiam-* marks a package
// made by the OpenPlex project (Avakiam; the private keys are kept offline),
// marketplace-* one reviewed and signed by the OpenPlex marketplace (made by
// a third-party developer).
var trustedKeys = map[string]string{
	"avakiam-2026":     "yyznJhRAeKanRpxzTEzd+IBAkXLzxRyJrxqngB9bXpI=",
	"marketplace-2026": "1cE7NRaT1xC9Fm77w68BsLRUhWTsqYcpOwrVxqgScbk=",
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
