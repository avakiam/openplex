package mail

import (
	"strings"
	"testing"
)

// Vectors from the SHA-crypt specification (akkadia.org/drepper/SHA-crypt.txt).
func TestSHA512Crypt(t *testing.T) {
	cases := []struct {
		salt, password string
		rounds         int
		want           string
	}{
		{"saltstring", "Hello world!", 5000, "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"saltstringsaltstring", "Hello world!", 10000, "$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v."},
		{"toolongsaltstring", "This is just a test", 5000, "$6$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQzQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0"},
		{"roundstoolow", "the minimum number is still observed", 10, "$6$rounds=1000$roundstoolow$kUMsbe306n21p9R.FRkW3IGn.S9NPN0x50YhH1xhLsPuWGsUSklZt58jaTfF4ZEQpyUNGc0dqbpBYYBaHHrsX."},
	}
	for _, c := range cases {
		if got := sha512Crypt(c.password, c.salt, c.rounds); got != c.want {
			t.Errorf("sha512Crypt(%q, %q, %d)\n got  %s\n want %s", c.password, c.salt, c.rounds, got, c.want)
		}
	}
	h := HashPassword("correct horse")
	if !strings.HasPrefix(h, "{SHA512-CRYPT}$6$rounds=50000$") || HashPassword("correct horse") == h {
		t.Errorf("HashPassword = %s", h)
	}
}
