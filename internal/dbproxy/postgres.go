package dbproxy

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// PostgreSQL request codes.
const (
	pgSSLRequest    = 80877103
	pgGSSENCRequest = 80877104
	pgCancelRequest = 80877102
)

// timeNow is replaced in tests.
var timeNow = time.Now

// readStartup reads a length-prefixed startup-phase message.
func readStartup(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n < 8 || n > 10000 {
		return nil, errors.New("bad startup message length")
	}
	m := make([]byte, n)
	copy(m, h[:])
	if _, err := io.ReadFull(r, m[4:]); err != nil {
		return nil, err
	}
	return m, nil
}

// startupParams reads user and database of a StartupMessage (protocol 3.x).
func startupParams(m []byte) (user, database string, err error) {
	if len(m) < 8 || binary.BigEndian.Uint32(m[4:8])>>16 != 3 {
		return "", "", errors.New("unsupported protocol version")
	}
	params := bytes.Split(bytes.TrimRight(m[8:], "\x00"), []byte{0})
	for i := 0; i+1 < len(params); i += 2 {
		switch string(params[i]) {
		case "user":
			user = string(params[i+1])
		case "database":
			database = string(params[i+1])
		}
	}
	if user == "" {
		return "", "", errors.New("no user in the startup message")
	}
	if database == "" {
		database = user
	}
	return user, database, nil
}

// pgError is a FATAL ErrorResponse.
func pgError(code, msg string) []byte {
	var b bytes.Buffer
	for _, f := range [][2]string{{"S", "FATAL"}, {"V", "FATAL"}, {"C", code}, {"M", msg}} {
		b.WriteString(f[0])
		b.WriteString(f[1])
		b.WriteByte(0)
	}
	b.WriteByte(0)
	out := []byte{'E', 0, 0, 0, 0}
	binary.BigEndian.PutUint32(out[1:], uint32(b.Len()+4))
	return append(out, b.Bytes()...)
}

// startPostgres runs the start of a PostgreSQL connection: the client's TLS
// request (TLS is terminated here) and its startup message, which names the
// user and the database; it is then sent on to the server.
func (p *Proxy) startPostgres(c net.Conn, mine []Access) (net.Conn, net.Conn, error) {
	var front net.Conn = c
	tlsDone := false
	for range 3 {
		m, err := readStartup(front)
		if err != nil {
			return nil, nil, err
		}
		code := binary.BigEndian.Uint32(m[4:8])
		switch {
		case len(m) == 8 && code == pgGSSENCRequest:
			if _, err := front.Write([]byte{'N'}); err != nil {
				return nil, nil, err
			}
		case len(m) == 8 && code == pgSSLRequest && !tlsDone:
			if _, err := front.Write([]byte{'S'}); err != nil {
				return nil, nil, err
			}
			tc := tls.Server(c, p.o.Cert.TLS)
			if err := tc.Handshake(); err != nil {
				return nil, nil, fmt.Errorf("TLS: %w", err)
			}
			front, tlsDone = tc, true
		case len(m) == 16 && code == pgCancelRequest:
			// The cancel key is the secret; the address is allowed for some database.
			back, err := p.dial(Postgres)
			if err == nil {
				_, _ = back.Write(m)
				back.Close()
			}
			return nil, nil, errors.New("cancel request forwarded")
		default:
			if !tlsDone {
				_, _ = front.Write(pgError("28000", "remote connections to this server must use TLS (sslmode=require)"))
				return nil, nil, errors.New("client did not ask for TLS")
			}
			user, database, err := startupParams(m)
			if err != nil {
				_, _ = front.Write(pgError("08P01", err.Error()))
				return nil, nil, err
			}
			found := false
			for _, a := range mine {
				found = found || (a.User == user && a.Database == database)
			}
			if !found {
				_, _ = front.Write(pgError("28000", fmt.Sprintf("no remote access to database %q for user %q from this address", database, user)))
				return nil, nil, fmt.Errorf("database %q user %q not allowed from this address", database, user)
			}
			back, err := p.dial(Postgres)
			if err != nil {
				_, _ = front.Write(pgError("08006", "the database server is not available"))
				return nil, nil, fmt.Errorf("server unavailable: %w", err)
			}
			if _, err := back.Write(m); err != nil {
				back.Close()
				return nil, nil, err
			}
			return front, back, nil
		}
	}
	return nil, nil, errors.New("too many startup requests")
}
