package update

import (
	"crypto/ed25519"
	"encoding/base64"
)

// TrustedKeys verify OpenPlex releases (SHA256SUMS.sig). The private keys
// are kept offline by Avakiam; a release signed with anything else is
// refused, even if it comes from the official repository.
var TrustedKeys = map[string]ed25519.PublicKey{
	"avakiam-release-2026": mustKey("0/RuzQESO+QqAcg7qOQN4NrWOgypqSsjZ5p9F3F4osI="),
}

func mustKey(b64 string) ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != ed25519.PublicKeySize {
		panic("update: bad trusted key")
	}
	return b
}
