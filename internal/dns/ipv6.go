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

// probeServes connects to the websites' ports (443 and 80) on addr from the
// server itself: it fails when the edge is not published on IPv6 or the host
// firewall drops it. Filters outside the server (the provider's firewall)
// are not seen from here.
func probeServes(ctx context.Context, addr netip.Addr) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	for _, port := range []uint16{443, 80} {
		c, err := d.DialContext(ctx, "tcp6", netip.AddrPortFrom(addr, port).String())
		if err != nil {
			return err
		}
		c.Close()
	}
	return nil
}

// IPv6Check is the result of testing an IPv6 address of this server.
type IPv6Check struct {
	Address string `json:"address"`
	// Outbound: the address reaches a public IPv6 service.
	Outbound bool `json:"outbound"`
	// Serves: the websites' ports 443 and 80 answer on the address.
	Serves bool `json:"serves"`
	// Works: both, so AAAA records can point at it.
	Works bool   `json:"works"`
	Error string `json:"error,omitempty"`
}

// CheckIPv6 tests addr, or the first public IPv6 address of the server's
// interfaces when addr is empty ("" address: the server has none): that it
// reaches the internet and that the websites answer on it.
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
		return out, nil
	}
	out.Outbound = true
	if err := m.serve6(ctx, a); err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.Serves, out.Works = true, true
	return out, nil
}
