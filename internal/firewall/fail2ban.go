package firewall

import (
	"context"
	"errors"
	"net/netip"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Jail is one Fail2Ban jail.
type Jail struct {
	Name            string   `json:"name"`
	CurrentlyFailed int      `json:"currently_failed"`
	TotalFailed     int      `json:"total_failed"`
	CurrentlyBanned int      `json:"currently_banned"`
	TotalBanned     int      `json:"total_banned"`
	Banned          []string `json:"banned"`
}

// Fail2BanStatus is what the dashboard shows.
type Fail2BanStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Jails     []Jail `json:"jails"`
}

// Errors.
var (
	ErrFail2BanDown = errors.New("Fail2Ban is not running on this server")
	ErrNoSuchJail   = errors.New("there is no such Fail2Ban jail")
	ErrBadAddress   = errors.New("invalid IP address")
)

var jailRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func (m *Manager) f2b(ctx context.Context, args ...string) (string, error) {
	p, err := exec.LookPath("fail2ban-client")
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return m.run(cctx, "", p, args...)
}

// Fail2Ban returns the status of every jail.
func (m *Manager) Fail2Ban(ctx context.Context) (*Fail2BanStatus, error) {
	st := &Fail2BanStatus{Jails: []Jail{}}
	if _, err := exec.LookPath("fail2ban-client"); err != nil {
		return st, nil
	}
	st.Installed = true
	out, err := m.f2b(ctx, "status")
	if err != nil {
		return st, nil // not running (socket missing)
	}
	st.Running = true
	for _, name := range parseJailList(out) {
		out, err := m.f2b(ctx, "status", name)
		if err != nil {
			continue
		}
		st.Jails = append(st.Jails, parseJail(name, out))
	}
	return st, nil
}

// Unban lifts a ban in one jail.
func (m *Manager) Unban(ctx context.Context, jail, ip string) error {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ErrBadAddress
	}
	if !jailRe.MatchString(jail) {
		return ErrNoSuchJail
	}
	out, err := m.f2b(ctx, "status")
	if err != nil {
		return ErrFail2BanDown
	}
	if !slices.Contains(parseJailList(out), jail) {
		return ErrNoSuchJail
	}
	if out, err := m.f2b(ctx, "set", jail, "unbanip", addr.Unmap().String()); err != nil {
		return errors.New("fail2ban-client: " + strings.TrimSpace(out))
	}
	return nil
}

// statusFields parses fail2ban-client's tree output ("|- Key:\tvalue").
func statusFields(out string) map[string]string {
	f := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimLeft(line, "|`- \t")
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return f
}

func parseJailList(out string) []string {
	var names []string
	for _, n := range strings.Split(statusFields(out)["jail list"], ",") {
		if n = strings.TrimSpace(n); jailRe.MatchString(n) {
			names = append(names, n)
		}
	}
	return names
}

func parseJail(name, out string) Jail {
	f := statusFields(out)
	num := func(k string) int { n, _ := strconv.Atoi(f[k]); return n }
	j := Jail{Name: name, CurrentlyFailed: num("currently failed"), TotalFailed: num("total failed"),
		CurrentlyBanned: num("currently banned"), TotalBanned: num("total banned"), Banned: []string{}}
	for _, ip := range strings.Fields(f["banned ip list"]) {
		if a, err := netip.ParseAddr(ip); err == nil {
			j.Banned = append(j.Banned, a.String())
		}
	}
	return j
}
