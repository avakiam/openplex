// Package ssl renders the Caddy edge configuration. Certificates (Let's
// Encrypt with ZeroSSL fallback), HTTP/3 and OCSP stapling are handled by Caddy's
// automatic HTTPS; this package decides which hosts get TLS and how each vhost
// is served.
package ssl

import (
	"errors"
	"fmt"
	"net/netip"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Mount points inside the edge container (kept in sync with internal/docker).
const (
	EdgeSitesRoot = "/srv/sites"        // where a site directory is mounted in its own containers
	EdgeLogsRoot  = "/var/log/openplex" // writable per-site access logs
	EdgePanelSock = "/run/openplex/openplexd.sock"
)

// Backend says how a vhost is served.
type Backend string

const (
	BackendStatic   Backend = "static"   // the site's own file server, proxied
	BackendFastCGI  Backend = "fastcgi"  // the PHP container's web server, proxied
	BackendProxy    Backend = "proxy"    // reverse_proxy upstream:port
	BackendDisabled Backend = "disabled" // 503 page
)

// Site is one website as the edge sees it.
type Site struct {
	ID           int64
	Name         string   // primary hostname; also the site directory name
	Aliases      []string // extra hostnames
	DocumentRoot string   // relative to the site dir
	Backend      Backend
	Upstream     string // container host:port for fastcgi/proxy
	SSL          bool
	ForceHTTPS   bool
	Redirects    []Redirect
	WWW          []string // hosts whose www. name redirects to Name (permanent)
}

// Redirect rule.
type Redirect struct {
	Path   string
	Target string
	Code   int
}

// Config is the full edge configuration.
type Config struct {
	ACMEEmail   string
	PanelDomain string // optional hostname proxied to openplexd
	MailHost    string // mail server host: served so Caddy obtains its certificate
	// Webmail, when on, is served on MailHost and on WebmailHosts.
	WebmailUpstream string
	WebmailHosts    []string
	Sites           []Site

	// Set by extensions with the edge:write permission (see NormalizePrefix).
	TrustedProxies  []string            // CIDRs whose forwarding headers are believed
	ClientIPHeaders []string            // headers with the visitor's address, in order
	Guards          map[string][]string // website host → the only CIDRs it accepts
	// GuardExempt are sources a guard never blocks: the edge network's
	// gateways, from which the edge sees connections whose real source Docker
	// hides (the host's own requests; IPv6 visits if the network has no IPv6).
	GuardExempt []string
}

// NormalizePrefix validates a CIDR (or a single address) for the edge and
// returns it in canonical form. Ranges broader than /8 (IPv4) or /16 (IPv6)
// are refused: they would trust or allow most of the internet.
func NormalizePrefix(s string) (string, error) {
	s = strings.TrimSpace(s)
	p, err := netip.ParsePrefix(s)
	if err != nil {
		a, aerr := netip.ParseAddr(s)
		if aerr != nil || a.Zone() != "" {
			return "", fmt.Errorf("invalid network %q", s)
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	p = p.Masked()
	if a := p.Addr(); a.Zone() != "" || a.Is4In6() || (a.Is4() && p.Bits() < 8) || (a.Is6() && p.Bits() < 16) {
		return "", fmt.Errorf("network %q is too broad or not usable", s)
	}
	return p.String(), nil
}

var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ValidHeaderName reports whether h is a plain HTTP header name.
func ValidHeaderName(h string) bool { return headerNameRe.MatchString(h) }

// prefixList validates canonical CIDRs for rendering.
func prefixList(ps []string) (string, error) {
	if len(ps) == 0 {
		return "", errors.New("empty network list")
	}
	for _, p := range ps {
		if n, err := NormalizePrefix(p); err != nil || n != p {
			return "", fmt.Errorf("invalid network %q", p)
		}
	}
	return strings.Join(ps, " "), nil
}

var (
	hostRe     = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	emailRe    = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)
	upstreamRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*:[0-9]{1,5}$`)
	pathRe     = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/\-]*$`)
	targetRe   = regexp.MustCompile(`^(https?://[A-Za-z0-9.\-]+(:[0-9]+)?)?/?[A-Za-z0-9._~!$&'()*+,;=:@%/?#\-]*$`)
)

// ValidHostname reports whether h is a safe lowercase DNS name (optionally wildcard).
func ValidHostname(h string) bool { return len(h) <= 253 && hostRe.MatchString(h) }

// ValidDocumentRoot reports whether p is a clean relative path inside the site dir.
func ValidDocumentRoot(p string) bool {
	if p == "" || p == "." {
		return true
	}
	c := path.Clean(p)
	return c == p && !strings.HasPrefix(c, "/") && !strings.HasPrefix(c, "..") && docRootRe.MatchString(c)
}

var docRootRe = regexp.MustCompile(`^[A-Za-z0-9._/\-]+$`)

// ValidRedirect validates a redirect rule.
func ValidRedirect(r Redirect) error {
	if !pathRe.MatchString(r.Path) || len(r.Path) > 512 {
		return errors.New("source path must start with / and contain only URL-safe characters")
	}
	if r.Target == "" || !targetRe.MatchString(r.Target) || len(r.Target) > 2048 {
		return errors.New("target must be an absolute http(s) URL or a path, without spaces")
	}
	switch r.Code {
	case 301, 302, 307, 308:
	default:
		return errors.New("status code must be 301, 302, 307 or 308")
	}
	return nil
}

// Render produces a Caddyfile. Every interpolated value is re-validated so a
// bad DB row can never inject Caddyfile directives.
func Render(c Config) (string, error) {
	var b strings.Builder
	b.WriteString("# Generated by openplexd — do not edit; changes are overwritten.\n{\n")
	if c.ACMEEmail != "" {
		if !emailRe.MatchString(c.ACMEEmail) {
			return "", fmt.Errorf("invalid ACME email %q", c.ACMEEmail)
		}
		fmt.Fprintf(&b, "\temail %s\n", c.ACMEEmail)
	}
	b.WriteString("\tadmin localhost:2019\n")
	b.WriteString("\tservers {\n\t\tprotocols h1 h2 h3\n")
	if len(c.TrustedProxies) > 0 {
		list, err := prefixList(c.TrustedProxies)
		if err != nil {
			return "", fmt.Errorf("trusted proxies: %w", err)
		}
		fmt.Fprintf(&b, "\t\ttrusted_proxies static %s\n", list)
		if len(c.ClientIPHeaders) > 0 {
			for _, h := range c.ClientIPHeaders {
				if !ValidHeaderName(h) {
					return "", fmt.Errorf("invalid client IP header %q", h)
				}
			}
			fmt.Fprintf(&b, "\t\tclient_ip_headers %s\n", strings.Join(c.ClientIPHeaders, " "))
		}
	}
	b.WriteString("\t}\n}\n\n")
	b.WriteString("(opx_common) {\n\tencode zstd gzip\n\theader {\n\t\t-Server\n\t\tX-Content-Type-Options nosniff\n\t\tReferrer-Policy strict-origin-when-cross-origin\n\t}\n}\n\n")

	if c.PanelDomain != "" {
		if !ValidHostname(c.PanelDomain) {
			return "", fmt.Errorf("invalid panel domain %q", c.PanelDomain)
		}
		fmt.Fprintf(&b, "%s {\n\timport opx_common\n\theader Strict-Transport-Security \"max-age=31536000\"\n\treverse_proxy unix/%s {\n\t\tflush_interval -1\n\t}\n}\n\n", c.PanelDomain, EdgePanelSock)
	}

	sites := append([]Site(nil), c.Sites...)
	sort.Slice(sites, func(i, j int) bool { return sites[i].Name < sites[j].Name })
	for _, s := range sites {
		if err := renderSite(&b, s, c.Guards, c.GuardExempt); err != nil {
			return "", fmt.Errorf("site %s: %w", s.Name, err)
		}
	}
	if c.MailHost != "" {
		if !ValidHostname(c.MailHost) {
			return "", fmt.Errorf("invalid mail host %q", c.MailHost)
		}
		if c.WebmailUpstream == "" {
			fmt.Fprintf(&b, "# mail server (certificate for SMTP/IMAP)\n%s {\n\timport opx_common\n\trespond \"This is the mail server. Use it in your e-mail app.\" 200\n}\n\n", c.MailHost)
		}
	}
	if c.WebmailUpstream != "" {
		if !upstreamRe.MatchString(c.WebmailUpstream) {
			return "", fmt.Errorf("invalid webmail upstream %q", c.WebmailUpstream)
		}
		hosts := append([]string(nil), c.WebmailHosts...)
		sort.Strings(hosts)
		if c.MailHost != "" {
			hosts = append([]string{c.MailHost}, hosts...)
		}
		for _, h := range hosts {
			if !ValidHostname(h) || strings.HasPrefix(h, "*.") {
				return "", fmt.Errorf("invalid webmail host %q", h)
			}
		}
		if len(hosts) > 0 {
			// The mail host also gives SMTP/IMAP their certificate.
			fmt.Fprintf(&b, "# webmail\n%s {\n\timport opx_common\n\trequest_body {\n\t\tmax_size 30MB\n\t}\n\treverse_proxy %s\n}\n\n", strings.Join(hosts, ", "), c.WebmailUpstream)
		}
	}
	// Catch-all: plain HTTP to a hostname nobody configured (or to the bare
	// IP) gets a clear 404 instead of Caddy's empty 200. Host-specific blocks
	// and the automatic HTTP→HTTPS redirects always take precedence.
	b.WriteString("# unknown hosts\n:80 {\n\timport opx_common\n\trespond \"No website is configured for this address.\" 404\n}\n")
	return b.String(), nil
}

func renderSite(b *strings.Builder, s Site, guards map[string][]string, exempt []string) error {
	hosts := append([]string{s.Name}, s.Aliases...)
	for _, h := range hosts {
		if !ValidHostname(h) {
			return fmt.Errorf("invalid hostname %q", h)
		}
	}
	if !ValidDocumentRoot(s.DocumentRoot) {
		return fmt.Errorf("invalid document root %q", s.DocumentRoot)
	}
	if err := renderWWW(b, s); err != nil {
		return err
	}
	var addrs []string
	for _, h := range hosts {
		switch {
		case !s.SSL:
			addrs = append(addrs, "http://"+h)
		case s.ForceHTTPS:
			addrs = append(addrs, h)
		default:
			addrs = append(addrs, "http://"+h, "https://"+h)
		}
	}
	fmt.Fprintf(b, "# site %d\n%s {\n\timport opx_common\n", s.ID, strings.Join(addrs, ", "))
	fmt.Fprintf(b, "\tlog {\n\t\toutput file %s/%s/access.log {\n\t\t\troll_size 50MiB\n\t\t\troll_keep 5\n\t\t}\n\t\tformat json\n\t}\n", EdgeLogsRoot, s.Name)

	// Hosts restricted by an extension answer 403 to any other network
	// (certificate challenges excepted).
	for i, h := range hosts {
		allow, ok := guards[h]
		if !ok {
			continue
		}
		if len(allow) == 0 {
			return fmt.Errorf("restriction of %s: empty network list", h)
		}
		list, err := prefixList(append(append([]string(nil), allow...), exempt...))
		if err != nil {
			return fmt.Errorf("restriction of %s: %w", h, err)
		}
		fmt.Fprintf(b, "\t@opx_guard%d {\n\t\thost %s\n\t\tnot remote_ip %s\n\t\tnot path /.well-known/acme-challenge/*\n\t}\n", i, h, list)
		fmt.Fprintf(b, "\trespond @opx_guard%d \"This website only accepts visits through its CDN.\" 403\n", i)
	}
	if s.Backend == BackendDisabled {
		b.WriteString("\trespond \"Site temporarily unavailable\" 503\n}\n\n")
		return nil
	}
	for _, r := range s.Redirects {
		if err := ValidRedirect(r); err != nil {
			return err
		}
		fmt.Fprintf(b, "\tredir %s %s %d\n", r.Path, r.Target, r.Code)
	}
	switch s.Backend {
	case BackendStatic, BackendFastCGI, BackendProxy:
		// Static and PHP sites run their own web server (see docker.WebPort):
		// the edge only proxies and never reads tenant files.
		if !upstreamRe.MatchString(s.Upstream) {
			return fmt.Errorf("invalid upstream %q", s.Upstream)
		}
		fmt.Fprintf(b, "\treverse_proxy %s\n", s.Upstream)
	default:
		return fmt.Errorf("unknown backend %q", s.Backend)
	}
	b.WriteString("}\n\n")
	return nil
}

// renderWWW adds www.<host> blocks that redirect to the site's main name.
func renderWWW(b *strings.Builder, s Site) error {
	scheme := "https"
	if !s.SSL {
		scheme = "http"
	}
	for _, h := range s.WWW {
		w := "www." + h
		if !ValidHostname(w) || strings.HasPrefix(h, "*.") {
			return fmt.Errorf("invalid www host %q", w)
		}
		addr := w
		if !s.SSL {
			addr = "http://" + w
		}
		fmt.Fprintf(b, "# site %d www\n%s {\n\timport opx_common\n\tredir %s://%s{uri} 308\n}\n\n", s.ID, addr, scheme, s.Name)
	}
	return nil
}
