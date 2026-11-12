package firewall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"openplex.dev/openplex/internal/db"
)

// ConfirmWindow is how long applied rule changes wait for confirmation.
const ConfirmWindow = 60 * time.Second

// Errors.
var (
	ErrUnavailable = errors.New("the firewall cannot be managed on this server")
	ErrNoPending   = errors.New("there are no changes waiting for confirmation")
	ErrTooMany     = errors.New("too many entries")
)

// Unavailable reasons (fixed strings; the dashboard translates them).
const (
	ReasonDisabled    = "Firewall management is turned off in the panel configuration (OPENPLEX_FIREWALL)."
	ReasonUnsupported = "Firewall management needs a Linux server."
	ReasonNoNFT       = "The nft command is missing. Install the nftables package on the server."
)

// Runner executes a host command with optional stdin and returns its
// combined output.
type Runner func(ctx context.Context, stdin string, name string, args ...string) (string, error)

func execRunner(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// Pending is an applied configuration waiting for confirmation.
type Pending struct {
	Enabled   bool              `json:"enabled"`
	Rules     []db.FirewallRule `json:"rules"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// Status is the firewall as the dashboard shows it.
type Status struct {
	Available   bool               `json:"available"`
	Unavailable string             `json:"unavailable,omitempty"`
	Enabled     bool               `json:"enabled"`
	Rules       []db.FirewallRule  `json:"rules"`
	Pending     *Pending           `json:"pending"`
	Blocks      []db.FirewallBlock `json:"blocks"`
	SSHPorts    []int              `json:"ssh_ports"`
	UFWActive   bool               `json:"ufw_active"`
	LastError   string             `json:"last_error"`
	AppliedAt   *time.Time         `json:"applied_at"`
}

// Manager owns the openplex nftables table.
type Manager struct {
	db     *db.DB
	log    *slog.Logger
	root   string // host filesystem root ("/"; a temp dir in tests)
	run    Runner
	nft    string // path of nft; "" when unavailable
	reason string
	window time.Duration

	mu        sync.Mutex
	pending   *Pending
	gen       int
	timer     *time.Timer
	lastErr   string
	appliedAt *time.Time
	services  []int
	restrict  []Restricted
}

// SetServicePorts declares the TCP ports OpenPlex serves on the host itself
// (the SFTP server); they stay open while the firewall is on. Call it before Run.
func (m *Manager) SetServicePorts(ports ...int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.services = ports
}

// SetRestrictedPorts declares the TCP ports OpenPlex serves only to some
// addresses (remote database access) and reloads the firewall.
func (m *Manager) SetRestrictedPorts(ctx context.Context, r []Restricted) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restrict = r
	if !m.Available() {
		return nil
	}
	return m.reload(ctx)
}

// New creates a Manager. enabled=false (OPENPLEX_FIREWALL=off) leaves the
// host firewall alone entirely.
func New(store *db.DB, log *slog.Logger, enabled bool) *Manager {
	m := &Manager{db: store, log: log, root: "/", run: execRunner, window: ConfirmWindow}
	switch {
	case !enabled:
		m.reason = ReasonDisabled
	case runtime.GOOS != "linux":
		m.reason = ReasonUnsupported
	default:
		if p, err := exec.LookPath("nft"); err == nil {
			m.nft = p
		} else {
			m.reason = ReasonNoNFT
		}
	}
	return m
}

// SSHPorts returns the host's SSH ports.
func (m *Manager) SSHPorts() []int { return SSHPorts(m.root) }

// Connected returns the addresses with a connection open to ports right now.
func (m *Manager) Connected(ports []int) []netip.Addr { return ConnectedClients(m.root, ports) }

// Available reports whether the host firewall can be managed.
func (m *Manager) Available() bool { return m.nft != "" }

// Run seeds and applies the configuration, then expires blocks and repairs
// the table (e.g. after `systemctl restart nftables` flushed it) until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	if !m.Available() {
		m.log.Info("host firewall not managed", "reason", m.reason)
		if p, err := exec.LookPath("nft"); err == nil && m.reason == ReasonDisabled && runtime.GOOS == "linux" {
			// Turned off after having been on: drop our table rather than
			// leave stale rules nobody manages.
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if _, err := m.run(cctx, "", p, "delete", "table", "inet", Table); err == nil {
				m.log.Info("firewall: removed the openplex nftables table")
			}
			cancel()
		}
		return
	}
	if err := m.init(ctx); err != nil {
		m.log.Error("firewall: initial apply failed", "err", err)
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for i := 1; ; i++ {
		select {
		case <-ctx.Done():
			m.shutdown()
			return
		case <-t.C:
			n, err := m.db.DeleteExpiredFirewallBlocks(ctx, time.Now())
			if err != nil {
				m.log.Error("firewall: expire blocks", "err", err)
			}
			if n > 0 || (i%10 == 0 && !m.tablePresent(ctx)) {
				m.mu.Lock()
				if err := m.reload(ctx); err != nil {
					m.log.Error("firewall: reapply", "err", err)
				}
				m.mu.Unlock()
			}
		}
	}
}

func (m *Manager) init(ctx context.Context) error {
	cfg, err := m.db.GetFirewall(ctx)
	if err != nil {
		return err
	}
	if !cfg.Seeded {
		on := !UFWActive(m.root) // with ufw in charge, start with ours off
		rules := DefaultRules(m.root)
		if err := m.db.SaveFirewall(ctx, on, rules); err != nil {
			return err
		}
		m.log.Info("firewall: initial configuration created", "enabled", on, "rules", len(rules))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reload(ctx)
}

// shutdown drops unconfirmed changes so the kernel matches what the next
// start will apply.
func (m *Manager) shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		return
	}
	m.cancelPending()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.reload(ctx); err != nil {
		m.log.Error("firewall: restore confirmed rules at shutdown", "err", err)
	}
}

// tablePresent reports whether the table exists, or is not expected to.
func (m *Manager) tablePresent(ctx context.Context) bool {
	rs, err := m.ruleset(ctx)
	if err != nil || (!rs.Enabled && len(rs.Blocks) == 0) {
		return true
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = m.run(cctx, "", m.nft, "list", "table", "inet", Table)
	return err == nil
}

// ruleset assembles what the kernel should have: pending rules if any,
// otherwise the confirmed ones, plus the blocks. Callers hold m.mu.
func (m *Manager) ruleset(ctx context.Context) (Ruleset, error) {
	blocks, err := m.db.ListFirewallBlocks(ctx)
	if err != nil {
		return Ruleset{}, err
	}
	if m.pending != nil {
		return Ruleset{Enabled: m.pending.Enabled, Rules: m.pending.Rules, Blocks: blocks, Services: m.services, Restricted: m.restrict}, nil
	}
	cfg, err := m.db.GetFirewall(ctx)
	if err != nil {
		return Ruleset{}, err
	}
	return Ruleset{Enabled: cfg.Enabled, Rules: cfg.Rules, Blocks: blocks, Services: m.services, Restricted: m.restrict}, nil
}

// reload renders and applies the current ruleset. Callers hold m.mu.
func (m *Manager) reload(ctx context.Context) error {
	rs, err := m.ruleset(ctx)
	if err != nil {
		return err
	}
	return m.apply(ctx, rs)
}

func (m *Manager) apply(ctx context.Context, rs Ruleset) error {
	script, err := Render(rs, time.Now())
	if err != nil {
		m.lastErr = err.Error()
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if out, err := m.run(cctx, script, m.nft, "-f", "-"); err != nil {
		m.lastErr = strings.TrimSpace(out)
		if m.lastErr == "" {
			m.lastErr = err.Error()
		}
		return fmt.Errorf("nft: %w: %s", err, m.lastErr)
	}
	t := time.Now().UTC()
	m.lastErr, m.appliedAt = "", &t
	return nil
}

func (m *Manager) cancelPending() {
	m.pending = nil
	m.gen++
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
}

// Status returns the current state.
func (m *Manager) Status(ctx context.Context) (*Status, error) {
	cfg, err := m.db.GetFirewall(ctx)
	if err != nil {
		return nil, err
	}
	blocks, err := m.db.ListFirewallBlocks(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := &Status{
		Available: m.Available(), Unavailable: m.reason, Enabled: cfg.Enabled, Rules: cfg.Rules, Blocks: blocks,
		SSHPorts: SSHPorts(m.root), UFWActive: UFWActive(m.root), LastError: m.lastErr, AppliedAt: m.appliedAt,
	}
	if m.pending != nil {
		p := *m.pending
		st.Pending = &p
	}
	return st, nil
}

// Apply puts a new configuration into effect at once; it is rolled back
// unless Confirm is called within the confirmation window. Rules must be
// normalised (NormalizeRule) by the caller.
func (m *Manager) Apply(ctx context.Context, enabled bool, rules []db.FirewallRule) (*Pending, error) {
	if !m.Available() {
		return nil, ErrUnavailable
	}
	if len(rules) > MaxRules {
		return nil, ErrTooMany
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelPending()
	m.pending = &Pending{Enabled: enabled, Rules: slices.Clone(rules), ExpiresAt: time.Now().Add(m.window).UTC()}
	if err := m.reload(ctx); err != nil {
		m.cancelPending()
		if rerr := m.reload(ctx); rerr != nil {
			m.log.Error("firewall: restore after failed apply", "err", rerr)
		}
		return nil, err
	}
	gen := m.gen
	m.timer = time.AfterFunc(m.window, func() { m.expire(gen) })
	p := *m.pending
	return &p, nil
}

// expire rolls back unconfirmed changes.
func (m *Manager) expire(gen int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gen != gen || m.pending == nil {
		return
	}
	m.cancelPending()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.reload(ctx); err != nil {
		m.log.Error("firewall: rollback failed", "err", err)
	}
	m.log.Warn("firewall: changes were not confirmed in time; previous rules restored")
	_ = m.db.Audit(ctx, db.AuditEntry{Action: "firewall.rolled_back", Target: "firewall", Detail: "changes were not confirmed in time"})
}

// Confirm makes the pending configuration permanent.
func (m *Manager) Confirm(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		return ErrNoPending
	}
	if err := m.db.SaveFirewall(ctx, m.pending.Enabled, m.pending.Rules); err != nil {
		return err
	}
	m.cancelPending()
	return nil
}

// Revert drops the pending configuration now.
func (m *Manager) Revert(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		return ErrNoPending
	}
	m.cancelPending()
	return m.reload(ctx)
}

// AddBlock blocks an address (normalised by NormalizeBlock) at once.
func (m *Manager) AddBlock(ctx context.Context, b *db.FirewallBlock) error {
	if !m.Available() {
		return ErrUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, err := m.db.ListFirewallBlocks(ctx)
	if err != nil {
		return err
	}
	if len(existing) >= MaxBlocks {
		return ErrTooMany
	}
	if err := m.db.CreateFirewallBlock(ctx, b); err != nil {
		return err
	}
	if err := m.reload(ctx); err != nil {
		_ = m.db.DeleteFirewallBlock(context.WithoutCancel(ctx), b.ID)
		_ = m.reload(context.WithoutCancel(ctx))
		return err
	}
	return nil
}

// RemoveBlock unblocks an address.
func (m *Manager) RemoveBlock(ctx context.Context, id int64) error {
	if !m.Available() {
		return ErrUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.db.DeleteFirewallBlock(ctx, id); err != nil {
		return err
	}
	return m.reload(ctx)
}
