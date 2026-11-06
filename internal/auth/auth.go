// Package auth implements credentials: password hashing, JWT access tokens,
// opaque refresh tokens, API keys, TOTP and SSH public-key validation.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

// ---------- passwords ----------

const bcryptCost = 12

// ErrWeakPassword is returned when a password fails policy.
var ErrWeakPassword = errors.New("password must be 10-128 characters and contain letters and digits")

// CheckPolicy validates password strength.
func CheckPolicy(pw string) error {
	if len(pw) < 10 || len(pw) > 128 {
		return ErrWeakPassword
	}
	var letter, digit bool
	for _, r := range pw {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	if !letter || !digit {
		return ErrWeakPassword
	}
	return nil
}

// HashPassword returns a bcrypt hash.
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(b), nil
}

// dummyHash is compared against when the user does not exist to keep timing uniform.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("openplex-timing-equalizer"), bcryptCost)

// VerifyPassword reports whether pw matches hash. An empty hash still costs one bcrypt compare.
func VerifyPassword(hash, pw string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// RandomPassword returns a URL-safe random password with n bytes of entropy.
func RandomPassword(n int) string {
	return "Op" + randToken(n) + "9"
}

// ---------- random tokens ----------

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the hex SHA-256 of a high-entropy token for storage.
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// NewRefreshToken returns a new opaque refresh token.
func NewRefreshToken() string { return randToken(32) }

// NewFamily returns a new refresh-token family identifier.
func NewFamily() string { return randToken(12) }

// APIKeyPrefix marks bearer credentials that are API keys rather than JWTs.
const APIKeyPrefix = "opx_"

// NewAPIKey returns (fullKey, displayPrefix). Only the hash of fullKey is stored.
func NewAPIKey() (string, string) {
	id := randToken(6)
	key := APIKeyPrefix + id + "_" + randToken(32)
	return key, APIKeyPrefix + id
}

// IsAPIKey reports whether a bearer token looks like an API key.
func IsAPIKey(tok string) bool { return strings.HasPrefix(tok, APIKeyPrefix) }

// ---------- JWT access tokens ----------

// Claims are the access-token claims.
type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Signer issues and verifies access tokens.
type Signer struct {
	key    []byte
	ttl    time.Duration
	issuer string
}

// NewSigner creates a Signer with an HMAC key.
func NewSigner(key []byte, ttl time.Duration) *Signer {
	return &Signer{key: key, ttl: ttl, issuer: "openplexd"}
}

// TTL returns the access-token lifetime.
func (s *Signer) TTL() time.Duration { return s.ttl }

// Issue returns a signed access token for a user.
func (s *Signer) Issue(userID int64, role string) (string, time.Time, error) {
	exp := time.Now().Add(s.ttl)
	c := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   fmt.Sprint(userID),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        randToken(9),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.key)
	return tok, exp, err
}

// Verify parses and validates an access token, returning the user ID.
func (s *Signer) Verify(tok string) (int64, *Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(tok, &c, func(t *jwt.Token) (any, error) { return s.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(10*time.Second))
	if err != nil {
		return 0, nil, err
	}
	var id int64
	if _, err := fmt.Sscan(c.Subject, &id); err != nil || id <= 0 {
		return 0, nil, errors.New("invalid subject")
	}
	return id, &c, nil
}

// ---------- TOTP ----------

// NewTOTP generates a TOTP secret for account at issuer "OpenPlex".
func NewTOTP(account string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{Issuer: "OpenPlex", AccountName: account})
}

// ValidateTOTP checks a 6-digit code with ±1 step skew.
func ValidateTOTP(secret, code string) bool {
	if secret == "" {
		return false
	}
	ok, err := totp.ValidateCustom(strings.TrimSpace(code), secret, time.Now().UTC(), totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// ---------- SSH keys ----------

// ParseSSHPublicKey validates an authorized_keys line, returning the
// normalized key and its SHA256 fingerprint. Only modern key types are accepted.
func ParseSSHPublicKey(line string) (normalized, fingerprint string, err error) {
	pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
	if err != nil {
		return "", "", fmt.Errorf("invalid public key: %w", err)
	}
	switch pk.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
		ssh.KeyAlgoSKED25519, ssh.KeyAlgoSKECDSA256:
	case ssh.KeyAlgoRSA:
		if ck, ok := pk.(ssh.CryptoPublicKey); ok {
			if rk, ok := ck.CryptoPublicKey().(interface{ Size() int }); ok && rk.Size()*8 < 2048 {
				return "", "", errors.New("RSA keys must be at least 2048 bits")
			}
		}
	default:
		return "", "", fmt.Errorf("unsupported key type %s", pk.Type())
	}
	normalized = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
	if comment != "" {
		normalized += " " + comment
	}
	return normalized, ssh.FingerprintSHA256(pk), nil
}
