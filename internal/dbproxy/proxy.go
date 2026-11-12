// Package dbproxy gives remote access to the shared MariaDB and PostgreSQL
// servers. openplexd listens on the usual ports, requires TLS (terminated
// here, with the panel's own certificate), reads which user and database a
// client asks for from the start of the protocol, and forwards the
// connection to the server on the internal database network only when the
// client's address is allowed for that database.
package dbproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"openplex.dev/openplex/internal/msg"
)

// Engines served.
const (
	MariaDB  = "mariadb"
	Postgres = "postgres"
)

// Access describes who may reach a database remotely.
type Access struct {
	Engine   string
	Database string
	User     string
	Allow    []netip.Prefix
}

// Options configure a Proxy.
type Options struct {
	// Listen is the address of each engine's listener (":3306", ":5432").
	Listen map[string]string
	// Databases lists the databases with remote access.
	Databases func(ctx context.Context) ([]Access, error)
	// Backend returns the address of an engine's server on the internal network.
	Backend func(ctx context.Context, engine string) (string, error)
	Cert    *Cert
	Log     *slog.Logger
	// OnSync is told, after each Sync, the port open per engine and the
	// databases with remote access (to open the host firewall to them).
	OnSync func(ctx context.Context, open map[string]int, dbs []Access) error
}

// Proxy runs the listeners of the engines that have remote databases.
type Proxy struct {
	o         Options
	syncMu    sync.Mutex
	mu        sync.Mutex
	listeners map[string]net.Listener
	perIP     map[netip.Addr]int
	ctx       context.Context
}

// maxPerIP bounds the open connections from one address.
const maxPerIP = 64

// handshakeTimeout bounds the start of a connection (until it is forwarded).
const handshakeTimeout = 20 * time.Second

// New creates a Proxy; nothing listens until Sync.
func New(ctx context.Context, o Options) *Proxy {
	return &Proxy{o: o, listeners: map[string]net.Listener{}, perIP: map[netip.Addr]int{}, ctx: ctx}
}

// Port is the TCP port engine listens on (0 when not configured).
func (p *Proxy) Port(engine string) int {
	_, port, err := net.SplitHostPort(p.o.Listen[engine])
	if err != nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(port, "%d", &n)
	return n
}

// Certificate returns the certificate clients see and its fingerprint.
func (p *Proxy) Certificate() ([]byte, string) { return p.o.Cert.PEM, p.o.Cert.Fingerprint }

// Sync starts the listener of every engine with a remote database and stops
// the others, then calls OnSync. It returns the errors of listeners that
// could not start (a port already in use).
func (p *Proxy) Sync(ctx context.Context) error {
	p.syncMu.Lock()
	defer p.syncMu.Unlock()
	dbs, err := p.o.Databases(ctx)
	if err != nil {
		return err
	}
	open, err := p.listen(dbs)
	if p.o.OnSync != nil {
		if ferr := p.o.OnSync(ctx, open, dbs); ferr != nil {
			err = errors.Join(err, fmt.Errorf("firewall: %w", ferr))
		}
	}
	return err
}

func (p *Proxy) listen(dbs []Access) (map[string]int, error) {
	want := map[string]bool{}
	for _, d := range dbs {
		want[d.Engine] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var errs []error
	open := map[string]int{}
	for _, engine := range []string{MariaDB, Postgres} {
		l, running := p.listeners[engine]
		switch {
		case want[engine] && !running:
			addr := p.o.Listen[engine]
			if addr == "" {
				errs = append(errs, fmt.Errorf("no listen address for %s", engine))
				continue
			}
			nl, err := net.Listen("tcp", addr)
			if err != nil {
				errs = append(errs, fmt.Errorf("remote %s access on %s: %w", engine, addr, err))
				continue
			}
			p.listeners[engine] = nl
			p.o.Log.Info("remote database access listening", "engine", engine, "addr", addr)
			go p.serve(engine, nl)
			open[engine] = p.Port(engine)
		case !want[engine] && running:
			_ = l.Close()
			delete(p.listeners, engine)
			p.o.Log.Info("remote database access closed", "engine", engine)
		case running:
			open[engine] = p.Port(engine)
		}
	}
	return open, errors.Join(errs...)
}

// Close stops every listener.
func (p *Proxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for e, l := range p.listeners {
		_ = l.Close()
		delete(p.listeners, e)
	}
}

func (p *Proxy) serve(engine string, l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			p.o.Log.Warn("remote database accept", "engine", engine, "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go p.handle(engine, c)
	}
}

func remoteAddr(c net.Conn) netip.Addr {
	ap, err := netip.ParseAddrPort(c.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

func allowed(list []netip.Prefix, ip netip.Addr) bool {
	for _, p := range list {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func (p *Proxy) handle(engine string, c net.Conn) {
	defer c.Close()
	ip := remoteAddr(c)
	dbs, err := p.o.Databases(p.ctx)
	if err != nil {
		p.o.Log.Warn("remote database lookup", "err", err)
		return
	}
	var mine []Access
	for _, d := range dbs {
		if d.Engine == engine && allowed(d.Allow, ip) {
			mine = append(mine, d)
		}
	}
	// An address allowed for no database gets nothing, not even a greeting.
	// Logged at debug level: scanners knock on these ports all day.
	if len(mine) == 0 {
		p.o.Log.Debug("remote database connection refused", "engine", engine, "ip", ip.String())
		return
	}
	if !p.acquire(ip) {
		p.o.Log.Warn("remote database connection refused: too many connections", "engine", engine, "ip", ip.String())
		return
	}
	defer p.release(ip)
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	var front, back net.Conn
	switch engine {
	case MariaDB:
		front, back, err = p.startMySQL(c, mine)
	case Postgres:
		front, back, err = p.startPostgres(c, mine)
	}
	if err != nil {
		p.o.Log.Info("remote database connection refused", "engine", engine, "ip", ip.String(), "reason", err.Error())
		return
	}
	defer back.Close()
	_ = front.SetDeadline(time.Time{})
	_ = back.SetDeadline(time.Time{})
	pipe(front, back)
}

func (p *Proxy) acquire(ip netip.Addr) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.perIP[ip] >= maxPerIP {
		return false
	}
	p.perIP[ip]++
	return true
}

func (p *Proxy) release(ip netip.Addr) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.perIP[ip]--; p.perIP[ip] <= 0 {
		delete(p.perIP, ip)
	}
}

func (p *Proxy) dial(engine string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	addr, err := p.o.Backend(ctx, engine)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

// pipe copies both ways until either side closes.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

// ParseAllow normalizes an allow-list: addresses or networks (CIDR), at most
// 20, no duplicates.
func ParseAllow(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		if s == "" {
			continue
		}
		var p netip.Prefix
		if a, err := netip.ParseAddr(s); err == nil {
			a = a.Unmap()
			p = netip.PrefixFrom(a, a.BitLen())
		} else if p, err = netip.ParsePrefix(s); err == nil {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()).Masked()
		} else {
			return nil, msg.New("{value} is not an IP address or network (CIDR)", "value", s)
		}
		if !p.IsValid() || p.Addr().Zone() != "" || strings.Contains(s, "%") {
			return nil, msg.New("{value} is not an IP address or network (CIDR)", "value", s)
		}
		dup := false
		for _, q := range out {
			dup = dup || q == p
		}
		if !dup {
			out = append(out, p)
		}
	}
	if len(out) > 20 {
		return nil, msg.New("at most 20 addresses or networks")
	}
	return out, nil
}
