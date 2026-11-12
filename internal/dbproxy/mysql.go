package dbproxy

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

// MySQL/MariaDB capability flags used here.
const (
	clientConnectWithDB      = 0x00000008
	clientSSL                = 0x00000800
	clientSecureConnection   = 0x00008000
	clientPluginAuthLenencCD = 0x00200000
)

// maxHandshakePacket bounds the packets read before a connection is forwarded.
const maxHandshakePacket = 64 << 10

func readPacket(r io.Reader) (seq byte, payload []byte, err error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := int(h[0]) | int(h[1])<<8 | int(h[2])<<16
	if n > maxHandshakePacket {
		return 0, nil, errors.New("packet too large")
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return h[3], payload, nil
}

func writePacket(w io.Writer, seq byte, payload []byte) error {
	n := len(payload)
	b := make([]byte, 4, 4+n)
	b[0], b[1], b[2], b[3] = byte(n), byte(n>>8), byte(n>>16), seq
	_, err := w.Write(append(b, payload...))
	return err
}

// mysqlError is an ERR packet.
func mysqlError(code uint16, state, msg string) []byte {
	b := []byte{0xff, byte(code), byte(code >> 8), '#'}
	b = append(b, state...)
	return append(b, msg...)
}

// greetingCapsOffset finds where the lower capability flags of a server
// greeting (HandshakeV10) are.
func greetingCapsOffset(g []byte) (int, error) {
	if len(g) < 1 || g[0] != 10 {
		return 0, errors.New("unexpected server greeting")
	}
	nul := bytes.IndexByte(g[1:], 0)
	if nul < 0 {
		return 0, errors.New("unexpected server greeting")
	}
	off := 1 + nul + 1 + 4 + 8 + 1 // version, connection id, auth data part 1, filler
	if len(g) < off+2 {
		return 0, errors.New("unexpected server greeting")
	}
	return off, nil
}

// handshakeUser reads the user (and database, when sent) of a
// HandshakeResponse41.
func handshakeUser(p []byte) (user, database string, err error) {
	if len(p) < 33 {
		return "", "", errors.New("short handshake response")
	}
	caps := binary.LittleEndian.Uint32(p[0:4])
	rest := p[32:]
	nul := bytes.IndexByte(rest, 0)
	if nul < 0 {
		return "", "", errors.New("bad handshake response")
	}
	user, rest = string(rest[:nul]), rest[nul+1:]
	// Skip the auth response.
	switch {
	case caps&clientPluginAuthLenencCD != 0:
		n, size, ok := lenenc(rest)
		if !ok || uint64(len(rest)-size) < n {
			return user, "", nil
		}
		rest = rest[size+int(n):]
	case caps&clientSecureConnection != 0:
		if len(rest) < 1 || len(rest) < 1+int(rest[0]) {
			return user, "", nil
		}
		rest = rest[1+int(rest[0]):]
	default:
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			return user, "", nil
		}
		rest = rest[i+1:]
	}
	if caps&clientConnectWithDB != 0 {
		if i := bytes.IndexByte(rest, 0); i >= 0 {
			database = string(rest[:i])
		}
	}
	return user, database, nil
}

func lenenc(b []byte) (uint64, int, bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	switch b[0] {
	case 0xfc:
		if len(b) < 3 {
			return 0, 0, false
		}
		return uint64(binary.LittleEndian.Uint16(b[1:3])), 3, true
	case 0xfd:
		if len(b) < 4 {
			return 0, 0, false
		}
		return uint64(b[1]) | uint64(b[2])<<8 | uint64(b[3])<<16, 4, true
	case 0xfe:
		if len(b) < 9 {
			return 0, 0, false
		}
		return binary.LittleEndian.Uint64(b[1:9]), 9, true
	}
	return uint64(b[0]), 1, true
}

// startMySQL runs the start of a MariaDB connection: the server's greeting
// (advertising TLS), the client's TLS upgrade, its handshake response (which
// names the user), and the authentication exchange, renumbering packets as
// the server never sees the TLS request.
func (p *Proxy) startMySQL(c net.Conn, mine []Access) (net.Conn, net.Conn, error) {
	back, err := p.dial(MariaDB)
	if err != nil {
		return nil, nil, fmt.Errorf("server unavailable: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			back.Close()
		}
	}()
	_ = back.SetDeadline(timeNow().Add(handshakeTimeout))
	_, greeting, err := readPacket(back)
	if err != nil {
		return nil, nil, err
	}
	off, err := greetingCapsOffset(greeting)
	if err != nil {
		return nil, nil, err
	}
	caps := binary.LittleEndian.Uint16(greeting[off:])
	binary.LittleEndian.PutUint16(greeting[off:], caps|clientSSL)
	if err := writePacket(c, 0, greeting); err != nil {
		return nil, nil, err
	}
	seq, req, err := readPacket(c)
	if err != nil {
		return nil, nil, err
	}
	if len(req) < 4 || binary.LittleEndian.Uint32(req[0:4])&clientSSL == 0 {
		_ = writePacket(c, seq+1, mysqlError(3159, "HY000", "Remote connections to this server must use TLS (--ssl, or ssl-mode=REQUIRED)."))
		return nil, nil, errors.New("client did not ask for TLS")
	}
	tc := tls.Server(c, p.o.Cert.TLS)
	if err := tc.Handshake(); err != nil {
		return nil, nil, fmt.Errorf("TLS: %w", err)
	}
	seq, resp, err := readPacket(tc)
	if err != nil {
		return nil, nil, err
	}
	user, _, err := handshakeUser(resp)
	if err != nil {
		return nil, nil, err
	}
	found := false
	for _, a := range mine {
		found = found || a.User == user
	}
	if !found {
		_ = writePacket(tc, seq+1, mysqlError(1045, "28000", fmt.Sprintf("Access denied for user '%s' from this address", user)))
		return nil, nil, fmt.Errorf("user %q is not allowed from this address", user)
	}
	binary.LittleEndian.PutUint32(resp[0:4], binary.LittleEndian.Uint32(resp[0:4])&^clientSSL)
	if err := writePacket(back, seq-1, resp); err != nil {
		return nil, nil, err
	}
	// Authentication: the server's packets are one ahead for the client.
	for range 16 {
		sseq, pkt, err := readPacket(back)
		if err != nil {
			return nil, nil, err
		}
		if err := writePacket(tc, sseq+1, pkt); err != nil {
			return nil, nil, err
		}
		if len(pkt) > 0 && (pkt[0] == 0x00 || pkt[0] == 0xff) {
			if pkt[0] == 0xff {
				return nil, nil, errors.New("authentication failed")
			}
			ok = true
			return tc, back, nil
		}
		cseq, reply, err := readPacket(tc)
		if err != nil {
			return nil, nil, err
		}
		if err := writePacket(back, cseq-1, reply); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, errors.New("authentication did not finish")
}
