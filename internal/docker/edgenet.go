package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// The edge network is dual-stack so that Docker (27 and later) publishes the
// edge's ports on IPv6 with ip6tables DNAT, which keeps the visitor's address.
// On an IPv4-only network its userland proxy forwards every IPv6 visit from
// the network gateway instead, hiding the visitor from the access logs, PHP,
// Fail2Ban and extension restrictions.

// edgeV6Subnet is the edge network's IPv6 range when the engine has no IPv6
// address pool of its own (Docker before 27).
const edgeV6Subnet = "fd6f:706c:7865:6467::/64"

// edgeNetworkAttempts are the ways to create the edge network, best first:
// IPv6 from the engine's pool, IPv6 from edgeV6Subnet, and IPv4 only (an
// engine or kernel without IPv6). v4 keeps an existing IPv4 range (the webmail
// trusts the edge by it); empty lets the engine pick one.
func edgeNetworkAttempts(v4 []network.IPAMConfig, ipv6Only bool) []network.CreateOptions {
	opts := func(v6 bool, extra ...network.IPAMConfig) network.CreateOptions {
		o := network.CreateOptions{Driver: "bridge", Labels: managedLabels("network")}
		if v6 {
			o.EnableIPv6 = &v6
		}
		if cfg := append(append([]network.IPAMConfig(nil), v4...), extra...); len(cfg) > 0 {
			o.IPAM = &network.IPAM{Config: cfg}
		}
		return o
	}
	out := []network.CreateOptions{opts(true), opts(true, network.IPAMConfig{Subnet: edgeV6Subnet})}
	if !ipv6Only {
		out = append(out, opts(false))
	}
	return out
}

// ipv4Ranges returns the IPv4 entries of a network's address configuration.
func ipv4Ranges(cfg []network.IPAMConfig) []network.IPAMConfig {
	var out []network.IPAMConfig
	for _, c := range cfg {
		if c.Subnet != "" && !strings.Contains(c.Subnet, ":") {
			out = append(out, network.IPAMConfig{Subnet: c.Subnet, IPRange: c.IPRange, Gateway: c.Gateway})
		}
	}
	return out
}

// createEdgeNetwork creates name with the first attempt the engine accepts.
func (o *Orchestrator) createEdgeNetwork(ctx context.Context, name string, v4 []network.IPAMConfig, ipv6Only bool) error {
	var errs []error
	for _, opts := range edgeNetworkAttempts(v4, ipv6Only) {
		_, err := o.cli.NetworkCreate(ctx, name, opts)
		if err == nil || strings.Contains(err.Error(), "already exists") {
			if len(errs) > 0 {
				o.log.Warn("edge network created after a fallback", "network", name, "ipv6", opts.EnableIPv6 != nil, "err", errors.Join(errs...))
			}
			return nil
		}
		errs = append(errs, err)
	}
	return fmt.Errorf("create network %s: %w", name, errors.Join(errs...))
}

// ensureEdgeNetwork creates the edge network, or rebuilds one made without
// IPv6 by an older OpenPlex (once the engine shows it can create IPv6
// networks). The edge container is removed in a rebuild: EnsureEdge creates it
// again right after. Other members are reconnected with their aliases.
func (o *Orchestrator) ensureEdgeNetwork(ctx context.Context) error {
	o.netMu.Lock()
	defer o.netMu.Unlock()
	n, err := o.cli.NetworkInspect(ctx, EdgeNetwork, network.InspectOptions{})
	if client.IsErrNotFound(err) {
		return o.createEdgeNetwork(ctx, EdgeNetwork, nil, false)
	}
	if err != nil {
		return fmt.Errorf("inspect network %s: %w", EdgeNetwork, err)
	}
	if n.EnableIPv6 || o.edgeNoIPv6 {
		return nil
	}
	probe := EdgeNetwork + "_v6probe"
	_ = o.cli.NetworkRemove(ctx, probe)
	if err := o.createEdgeNetwork(ctx, probe, nil, true); err != nil {
		o.edgeNoIPv6 = true // don't retry until the next start
		o.log.Warn("edge network stays IPv4-only: the engine can't create IPv6 networks; IPv6 visitors will show the gateway's address", "err", err)
		return nil
	}
	if err := o.cli.NetworkRemove(ctx, probe); err != nil {
		return fmt.Errorf("remove network %s: %w", probe, err)
	}
	return o.rebuildEdgeNetwork(ctx, ipv4Ranges(n.IPAM.Config))
}

type edgeMember struct {
	id, name string
	aliases  []string
	running  bool
}

func (o *Orchestrator) rebuildEdgeNetwork(ctx context.Context, v4 []network.IPAMConfig) error {
	o.log.Info("rebuilding the edge network with IPv6", "network", EdgeNetwork)
	list, err := o.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("network", EdgeNetwork))})
	if err != nil {
		return fmt.Errorf("list %s members: %w", EdgeNetwork, err)
	}
	var members []edgeMember
	for _, s := range list {
		c, err := o.cli.ContainerInspect(ctx, s.ID)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", s.ID, err)
		}
		name := strings.TrimPrefix(c.Name, "/")
		if name == EdgeContainer {
			if err := o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				return fmt.Errorf("remove edge: %w", err)
			}
			continue
		}
		m := edgeMember{id: c.ID, name: name, running: c.State != nil && c.State.Running}
		if c.NetworkSettings != nil {
			if ep := c.NetworkSettings.Networks[EdgeNetwork]; ep != nil {
				m.aliases = ep.Aliases
			}
		}
		if err := o.cli.NetworkDisconnect(ctx, EdgeNetwork, c.ID, true); err != nil {
			return fmt.Errorf("disconnect %s from %s: %w", name, EdgeNetwork, err)
		}
		members = append(members, m)
	}
	if err := o.cli.NetworkRemove(ctx, EdgeNetwork); err != nil && !client.IsErrNotFound(err) {
		return fmt.Errorf("remove network %s: %w", EdgeNetwork, err)
	}
	err = o.createEdgeNetwork(ctx, EdgeNetwork, v4, false)
	if err != nil && len(v4) > 0 {
		// The old IPv4 range could not be reused: any range beats none.
		err = o.createEdgeNetwork(ctx, EdgeNetwork, nil, false)
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, m := range members {
		if err := o.cli.NetworkConnect(ctx, EdgeNetwork, m.id, &network.EndpointSettings{Aliases: m.aliases}); err != nil {
			errs = append(errs, fmt.Errorf("reconnect %s to %s: %w", m.name, EdgeNetwork, err))
			continue
		}
		if m.running {
			// A restart gives it back the default route it had through the edge network.
			if err := o.cli.ContainerRestart(ctx, m.id, container.StopOptions{}); err != nil {
				errs = append(errs, fmt.Errorf("restart %s: %w", m.name, err))
			}
		}
	}
	return errors.Join(errs...)
}
