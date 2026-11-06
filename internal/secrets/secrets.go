// Package secrets encrypts recoverable secrets (generated database
// passwords) at rest with AES-256-GCM. The key is derived from the daemon's
// secret.key with a purpose label, so it differs from the JWT signing key.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const version = "v1:"

// Box seals and opens secrets.
type Box struct{ aead cipher.AEAD }

// New derives a Box key from master for the given purpose.
func New(master []byte, purpose string) (*Box, error) {
	key, err := hkdf.Key(sha256.New, master, nil, "openplex/"+purpose, 32)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; the result is safe to store as text.
func (b *Box) Seal(plaintext string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return version + base64.RawStdEncoding.EncodeToString(ct), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(sealed string) (string, error) {
	if !strings.HasPrefix(sealed, version) {
		return "", errors.New("unknown secret format")
	}
	raw, err := base64.RawStdEncoding.DecodeString(sealed[len(version):])
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secret too short")
	}
	pt, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", errors.New("secret cannot be decrypted (was secret.key replaced?)")
	}
	return string(pt), nil
}

const alnum = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// Password returns an n-character alphanumeric password. Alphanumerics only, so
// it is safe inside SQL string literals and shell arguments.
func Password(n int) string {
	var sb strings.Builder
	max := big.NewInt(int64(len(alnum)))
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(fmt.Sprintf("crypto/rand failed: %v", err))
		}
		sb.WriteByte(alnum[v.Int64()])
	}
	return sb.String()
}
