package dns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"
)

// ipv6Probes are public IPv6 services (Cloudflare's and Google's resolvers on
// port 443): reaching one shows the address has a working route.
var ipv6Probes = []string{"[2606:4700:4700::1111]:443", "[2001:4860:4860::8888]:443"}

// probeIPv6 opens a TCP connection from src to a public IPv6 service. A
// failure means the address is configured on the interface but not routed
// (common on VPSs whose IPv6 still needs its gateway set up): records
// pointing at it would break HTTPS for visitors and certificate issuance.
func probeIPv6(ctx context.Context, src netip.Addr) error {
	if !src.Is6() || src.Is4In6() {
		return errors.New("not an IPv6 address")
	}
	d := net.Dialer{Timeout: 4 * time.Second, LocalAddr: &net.TCPAddr{IP: src.AsSlice()}}
	var err error
	for _, target := range ipv6Probes {
		var c net.Conn
		if c, err = d.DialContext(ctx, "tcp6", target); err == nil {
			c.Close()
			return nil
		}
	}
	return err
}

// IPv6Check is the result of testing an IPv6 address of this server.
type IPv6Check struct {
	Address string `json:"address"`
	Works   bool   `json:"works"`
	Error   string `json:"error,omitempty"`
}

// CheckIPv6 tests addr, or the first public IPv6 address of the server's
// interfaces when addr is empty ("" address: the server has none).
func (m *Manager) CheckIPv6(ctx context.Context, addr string) (*IPv6Check, error) {
	if addr == "" {
		_, addr = defaultAddrs(m.addrs())
		if addr == "" {
			return &IPv6Check{}, nil
		}
	}
	a, err := netip.ParseAddr(addr)
	if err != nil || !a.Is6() || a.Is4In6() {
		return nil, errors.New("enter an IPv6 address, such as 2001:db8::10")
	}
	out := &IPv6Check{Address: a.String()}
	if err := m.probe6(ctx, a); err != nil {
		out.Error = err.Error()
	} else {
		out.Works = true
	}
	return out, nil
}
