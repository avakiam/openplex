package docker

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// Every account has a bridge network of its own. Its website, shell and Redis
// containers live only there; the edge and the shared database servers join
// every account network. A website therefore reaches the edge and the
// databases, but never a container of another account.

const tenantNetKind = "tenant-network"

// TenantNetwork is the network of the account ownerID.
func TenantNetwork(ownerID int64) string { return fmt.Sprintf("openplex_u%d", ownerID) }

// ensureTenantNetwork creates the account network if needed (with a /24 from
// the configured pool) and connects the edge and the shared engines to it.
func (o *Orchestrator) ensureTenantNetwork(ctx context.Context, ownerID int64) (string, error) {
	name := TenantNetwork(ownerID)
	o.netMu.Lock()
	_, err := o.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if client.IsErrNotFound(err) {
		var subnet string
		if subnet, err = o.freeTenantSubnet(ctx); err == nil {
			labels := managedLabels(tenantNetKind)
			labels["openplex.owner_id"] = fmt.Sprint(ownerID)
			_, err = o.cli.NetworkCreate(ctx, name, network.CreateOptions{
				Driver: "bridge", Labels: labels,
				IPAM: &network.IPAM{Config: []network.IPAMConfig{{Subnet: subnet}}},
			})
			if err != nil && strings.Contains(err.Error(), "already exists") {
				err = nil
			}
		}
	}
	o.netMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("account network %s: %w", name, err)
	}
	for _, m := range o.tenantMembers() {
		if err := o.joinNetwork(ctx, name, m.name, m.aliases); err != nil {
			return "", err
		}
	}
	return name, nil
}

type netMember struct {
	name    string
	aliases []string
}

// tenantMembers are the shared containers present on every account network.
func (o *Orchestrator) tenantMembers() []netMember {
	m := []netMember{{name: EdgeContainer}}
	for _, e := range Engines {
		if e.Shared {
			m = append(m, netMember{name: e.container, aliases: []string{e.Host}})
		}
	}
	return m
}

// joinNetwork connects the container called cname (when it exists) to net.
func (o *Orchestrator) joinNetwork(ctx context.Context, net, cname string, aliases []string) error {
	c, err := o.findContainer(ctx, cname)
	if err != nil || c == nil {
		return err
	}
	if c.NetworkSettings != nil {
		if _, ok := c.NetworkSettings.Networks[net]; ok {
			return nil
		}
	}
	if err := o.cli.NetworkConnect(ctx, net, c.ID, &network.EndpointSettings{Aliases: aliases}); err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("connect %s to %s: %w", cname, net, err)
	}
	return nil
}

// joinTenantNetworks connects a shared container (edge, database server) to
// every existing account network.
func (o *Orchestrator) joinTenantNetworks(ctx context.Context, cname string, aliases []string) error {
	nets, err := o.cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(
		filters.Arg("label", LabelManaged+"=true"), filters.Arg("label", LabelKind+"="+tenantNetKind))})
	if err != nil {
		return fmt.Errorf("list account networks: %w", err)
	}
	for _, n := range nets {
		if err := o.joinNetwork(ctx, n.Name, cname, aliases); err != nil {
			return err
		}
	}
	return nil
}

// freeTenantSubnet picks the first /24 of the pool that no Docker network uses.
func (o *Orchestrator) freeTenantSubnet(ctx context.Context) (string, error) {
	pool, err := netip.ParsePrefix(o.cfg.TenantNetworks)
	if err != nil || !pool.Addr().Is4() || pool.Bits() > 24 {
		return "", fmt.Errorf("invalid tenant network pool %q (an IPv4 range of /24 or bigger is expected)", o.cfg.TenantNetworks)
	}
	nets, err := o.cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list networks: %w", err)
	}
	var used []netip.Prefix
	for _, n := range nets {
		for _, c := range n.IPAM.Config {
			if p, err := netip.ParsePrefix(c.Subnet); err == nil {
				used = append(used, p.Masked())
			}
		}
	}
	return nextFreeSubnet(pool.Masked(), used)
}

func nextFreeSubnet(pool netip.Prefix, used []netip.Prefix) (string, error) {
	a := pool.Addr().As4()
	start := binary.BigEndian.Uint32(a[:])
	count := uint32(1) << (24 - pool.Bits())
	for i := uint32(0); i < count; i++ {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], start+i<<8)
		p := netip.PrefixFrom(netip.AddrFrom4(b), 24)
		free := true
		for _, u := range used {
			if u.Overlaps(p) {
				free = false
				break
			}
		}
		if free {
			return p.String(), nil
		}
	}
	return "", fmt.Errorf("the account network pool %s is full; set OPENPLEX_TENANT_NETWORKS to a bigger range", pool)
}

// pruneTenantNetworks removes account networks nothing of the account uses
// any more (only the shared containers are left on them).
func (o *Orchestrator) pruneTenantNetworks(ctx context.Context) error {
	nets, err := o.cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(
		filters.Arg("label", LabelManaged+"=true"), filters.Arg("label", LabelKind+"="+tenantNetKind))})
	if err != nil {
		return err
	}
	shared := map[string]bool{}
	for _, m := range o.tenantMembers() {
		shared[m.name] = true
	}
	for _, n := range nets {
		full, err := o.cli.NetworkInspect(ctx, n.ID, network.InspectOptions{})
		if err != nil {
			return err
		}
		used := false
		for _, c := range full.Containers {
			if !shared[c.Name] {
				used = true
				break
			}
		}
		if used {
			continue
		}
		for id := range full.Containers {
			if err := o.cli.NetworkDisconnect(ctx, n.ID, id, true); err != nil {
				return fmt.Errorf("disconnect from %s: %w", n.Name, err)
			}
		}
		if err := o.cli.NetworkRemove(ctx, n.ID); err != nil {
			return fmt.Errorf("remove %s: %w", n.Name, err)
		}
		o.log.Info("removed unused account network", "network", n.Name)
	}
	return nil
}
