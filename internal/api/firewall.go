package api

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/firewall"
)

// firewallView adds what the dashboard needs to warn about lockouts.
type firewallView struct {
	*firewall.Status
	YourIP       string `json:"your_ip"`
	ConfirmSecs  int    `json:"confirm_seconds"`
	MaxRules     int    `json:"max_rules"`
	PanelPort    int    `json:"panel_port"`
	PanelDirect  bool   `json:"panel_direct"` // this request reached the panel port without the edge
	ViaPanelHost bool   `json:"via_panel_domain"`
}

func (s *Server) handleGetFirewall(w http.ResponseWriter, r *http.Request) {
	st, err := s.firewall.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	direct, port := s.panelDirect(r)
	writeJSON(w, http.StatusOK, firewallView{
		Status: st, YourIP: clientIP(r), ConfirmSecs: int(firewall.ConfirmWindow / time.Second), MaxRules: firewall.MaxRules,
		PanelPort: port, PanelDirect: direct, ViaPanelHost: r.RemoteAddr == "@" || r.RemoteAddr == "",
	})
}

// panelDirect reports whether r came straight to the TCP listener from a
// non-loopback address (so the input chain must let it in), and the port.
func (s *Server) panelDirect(r *http.Request) (bool, int) {
	_, p, _ := net.SplitHostPort(s.cfg.Listen)
	port, _ := strconv.Atoi(p)
	if r.RemoteAddr == "@" || r.RemoteAddr == "" {
		return false, port
	}
	ip, err := netip.ParseAddr(clientIP(r))
	return err == nil && !ip.Unmap().IsLoopback(), port
}

func (s *Server) firewallErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, firewall.ErrUnavailable):
		writeErr(w, http.StatusConflict, "unavailable", err.Error())
	case errors.Is(err, firewall.ErrNoPending):
		writeErr(w, http.StatusConflict, "no_pending", err.Error())
	case errors.Is(err, firewall.ErrTooMany):
		writeErr(w, http.StatusForbidden, "quota_exceeded", "the firewall already has the maximum number of entries")
	case errors.Is(err, db.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "this address is already blocked")
	case strings.HasPrefix(err.Error(), "nft:"):
		s.log.Error("firewall apply failed", "err", err)
		writeErr(w, http.StatusBadGateway, "apply_failed", "the server rejected the firewall rules; nothing was changed")
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) handleApplyFirewall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool              `json:"enabled"`
		Rules   []db.FirewallRule `json:"rules"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Rules) > firewall.MaxRules {
		badRequest(w, "too many rules (200 at most)")
		return
	}
	for i := range in.Rules {
		in.Rules[i].ID = 0
		if err := firewall.NormalizeRule(&in.Rules[i]); err != nil {
			badRequest(w, err.Error())
			return
		}
	}
	if in.Enabled {
		ip, _ := netip.ParseAddr(clientIP(r))
		for _, p := range s.firewall.SSHPorts() {
			if !firewall.CoversPort(in.Rules, firewall.TCP, p) {
				writeErr(w, http.StatusBadRequest, "lockout", "no rule allows SSH; you would lose SSH access to the server, so add a rule for its port first")
				return
			}
			// Panel users behind an SSH tunnel show up as 127.0.0.1, so
			// check the SSH sessions that are actually open.
			for _, c := range s.firewall.Connected([]int{p}) {
				if !firewall.Allows(in.Rules, firewall.TCP, p, c) {
					writeErr(w, http.StatusBadRequest, "lockout", "an SSH session is open right now from an address these rules would not allow; widen the SSH rule first")
					return
				}
			}
		}
		if direct, port := s.panelDirect(r); direct && !firewall.Allows(in.Rules, firewall.TCP, port, ip) {
			writeErr(w, http.StatusBadRequest, "lockout", "no rule lets your address reach the panel port; add one first or you would lose access to the panel")
			return
		}
	}
	p, err := s.firewall.Apply(r.Context(), in.Enabled, in.Rules)
	if err != nil {
		s.firewallErr(w, r, err)
		return
	}
	s.audit(r, "firewall.applied", "firewall", strconv.Itoa(len(in.Rules))+" rules, enabled="+strconv.FormatBool(in.Enabled))
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleConfirmFirewall(w http.ResponseWriter, r *http.Request) {
	if err := s.firewall.Confirm(r.Context()); err != nil {
		s.firewallErr(w, r, err)
		return
	}
	s.audit(r, "firewall.confirmed", "firewall", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRevertFirewall(w http.ResponseWriter, r *http.Request) {
	if err := s.firewall.Revert(r.Context()); err != nil {
		s.firewallErr(w, r, err)
		return
	}
	s.audit(r, "firewall.reverted", "firewall", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCreateFirewallBlock(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Address  string `json:"address"`
		Comment  string `json:"comment"`
		Duration int    `json:"duration_sec"` // 0 = permanent
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := firewall.NormalizeBlock(in.Address)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if ip, err := netip.ParseAddr(clientIP(r)); err == nil && p.Contains(ip.Unmap()) {
		badRequest(w, "you cannot block your own address")
		return
	}
	_, panelPort := s.panelDirect(r)
	for _, c := range s.firewall.Connected(append(s.firewall.SSHPorts(), panelPort)) {
		if p.Contains(c) {
			badRequest(w, "this address has an SSH or panel session open right now; blocking it would cut that session off")
			return
		}
	}
	if in.Duration < 0 || in.Duration > 366*24*3600 {
		badRequest(w, "the block duration must be at most one year")
		return
	}
	comment := strings.TrimSpace(in.Comment)
	if len([]rune(comment)) > firewall.MaxCommentLen || strings.ContainsAny(comment, "\r\n\t\x00") {
		badRequest(w, "the comment is too long (100 characters at most)")
		return
	}
	b := &db.FirewallBlock{Address: firewall.FormatPrefix(p), Comment: comment}
	if in.Duration > 0 {
		t := time.Now().Add(time.Duration(in.Duration) * time.Second).UTC().Truncate(time.Second)
		b.ExpiresAt = &t
	}
	if err := s.firewall.AddBlock(r.Context(), b); err != nil {
		s.firewallErr(w, r, err)
		return
	}
	s.audit(r, "firewall.blocked", b.Address, comment)
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) handleDeleteFirewallBlock(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	b, err := s.db.GetFirewallBlock(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.firewall.RemoveBlock(r.Context(), id); err != nil {
		s.firewallErr(w, r, err)
		return
	}
	s.audit(r, "firewall.unblocked", b.Address, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetFail2Ban(w http.ResponseWriter, r *http.Request) {
	st, err := s.firewall.Fail2Ban(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleFail2BanUnban(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Jail string `json:"jail"`
		IP   string `json:"ip"`
	}
	if !decode(w, r, &in) {
		return
	}
	err := s.firewall.Unban(r.Context(), in.Jail, in.IP)
	switch {
	case errors.Is(err, firewall.ErrBadAddress), errors.Is(err, firewall.ErrNoSuchJail):
		badRequest(w, err.Error())
		return
	case errors.Is(err, firewall.ErrFail2BanDown):
		writeErr(w, http.StatusConflict, "unavailable", err.Error())
		return
	case err != nil:
		s.log.Error("fail2ban unban failed", "err", err)
		writeErr(w, http.StatusBadGateway, "apply_failed", "Fail2Ban could not lift the ban")
		return
	}
	s.audit(r, "fail2ban.unbanned", in.IP, in.Jail)
	w.WriteHeader(http.StatusNoContent)
}
