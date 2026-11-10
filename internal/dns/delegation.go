package dns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// parentNS asks the servers of the parent zone (e.g. the .com registry for
// example.com) which nameservers zone is delegated to. Unlike the system
// resolver this sees a registrar change within minutes, not after caches
// expire. name has a trailing dot.
func parentNS(ctx context.Context, name string) ([]*net.NS, error) {
	zone := strings.TrimSuffix(name, ".")
	labels := strings.Split(zone, ".")
	var lastErr error = errors.New("no parent zone found")
	for i := 1; i < len(labels); i++ {
		parent := strings.Join(labels[i:], ".") + "."
		servers, err := net.DefaultResolver.LookupNS(ctx, parent)
		if err != nil || len(servers) == 0 {
			continue // not a zone cut (e.g. a subdomain of a subdomain); go up
		}
		for j, s := range servers {
			if j == 3 {
				break
			}
			addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", s.Host)
			if err != nil || len(addrs) == 0 {
				lastErr = err
				continue
			}
			hosts, err := queryNS(ctx, addrs[0], zone+".")
			if err == nil {
				out := make([]*net.NS, len(hosts))
				for k, h := range hosts {
					out[k] = &net.NS{Host: h}
				}
				return out, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	return nil, lastErr
}

// queryNS sends one non-recursive NS query for zone to server and returns
// the nameservers from the answer or the referral (authority section).
func queryNS(ctx context.Context, server netip.Addr, zone string) ([]string, error) {
	qname, err := dnsmessage.NewName(zone)
	if err != nil {
		return nil, err
	}
	id := uint16(time.Now().UnixNano())
	msg := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id},
		Questions: []dnsmessage.Question{{Name: qname, Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET}},
	}
	packet, err := msg.Pack()
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", netip.AddrPortFrom(server, 53).String())
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write(packet); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	var resp dnsmessage.Message
	if err := resp.Unpack(buf[:n]); err != nil {
		return nil, err
	}
	if resp.ID != id {
		return nil, errors.New("mismatched DNS response")
	}
	if resp.RCode == dnsmessage.RCodeNameError {
		return nil, nil // not registered
	}
	if resp.RCode != dnsmessage.RCodeSuccess {
		return nil, errors.New("DNS error " + resp.RCode.String())
	}
	var hosts []string
	for _, rr := range append(resp.Answers, resp.Authorities...) {
		if ns, ok := rr.Body.(*dnsmessage.NSResource); ok && strings.EqualFold(rr.Header.Name.String(), zone) {
			hosts = append(hosts, ns.NS.String())
		}
	}
	return hosts, nil
}
