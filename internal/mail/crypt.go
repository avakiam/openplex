package mail

import (
	"crypto/rand"
	"crypto/sha512"
	"strconv"
	"strings"
)

// SHA512-CRYPT ("$6$", Ulrich Drepper's specification), the password scheme
// docker-mailserver writes for Dovecot. The Go standard library has no
// implementation; this one is checked against the specification's vectors.

const itoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// cryptRounds is above the 5000 default to slow down offline guessing while
// keeping an IMAP login fast (~25 ms).
const cryptRounds = 50000

// HashPassword returns a Dovecot-ready {SHA512-CRYPT} hash with a random salt.
func HashPassword(password string) string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	salt := make([]byte, 16)
	for i, b := range raw {
		salt[i] = itoa64[int(b)%64]
	}
	return "{SHA512-CRYPT}" + sha512Crypt(password, string(salt), cryptRounds)
}

// sha512Crypt computes "$6$[rounds=N$]salt$hash".
func sha512Crypt(password, salt string, rounds int) string {
	if len(salt) > 16 {
		salt = salt[:16]
	}
	custom := rounds != 5000
	rounds = min(max(rounds, 1000), 999999999)
	p, s := []byte(password), []byte(salt)

	alt := sha512.New()
	alt.Write(p)
	alt.Write(s)
	alt.Write(p)
	b := alt.Sum(nil)

	a := sha512.New()
	a.Write(p)
	a.Write(s)
	i := len(p)
	for ; i > 64; i -= 64 {
		a.Write(b)
	}
	a.Write(b[:i])
	for i = len(p); i > 0; i >>= 1 {
		if i&1 != 0 {
			a.Write(b)
		} else {
			a.Write(p)
		}
	}
	c := a.Sum(nil)

	dp := sha512.New()
	for range len(p) {
		dp.Write(p)
	}
	pBytes := repeat(dp.Sum(nil), len(p))

	ds := sha512.New()
	for range 16 + int(c[0]) {
		ds.Write(s)
	}
	sBytes := repeat(ds.Sum(nil), len(s))

	for r := range rounds {
		h := sha512.New()
		if r&1 != 0 {
			h.Write(pBytes)
		} else {
			h.Write(c)
		}
		if r%3 != 0 {
			h.Write(sBytes)
		}
		if r%7 != 0 {
			h.Write(pBytes)
		}
		if r&1 != 0 {
			h.Write(c)
		} else {
			h.Write(pBytes)
		}
		c = h.Sum(nil)
	}

	var out strings.Builder
	out.WriteString("$6$")
	if custom {
		out.WriteString("rounds=" + strconv.Itoa(rounds) + "$")
	}
	out.WriteString(salt)
	out.WriteByte('$')
	groups := [][3]int{
		{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48},
		{28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13},
		{56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41},
	}
	for _, g := range groups {
		b64(&out, uint32(c[g[0]])<<16|uint32(c[g[1]])<<8|uint32(c[g[2]]), 4)
	}
	b64(&out, uint32(c[63]), 2)
	return out.String()
}

func repeat(digest []byte, n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, digest[:min(len(digest), n-len(out))]...)
	}
	return out
}

func b64(out *strings.Builder, w uint32, n int) {
	for range n {
		out.WriteByte(itoa64[w&0x3f])
		w >>= 6
	}
}
