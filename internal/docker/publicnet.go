package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// PublicNetwork holds the containers that publish ports to the internet
// themselves (the DNS and mail servers). It is dual-stack, like the edge
// network, so Docker publishes their ports on IPv6 with ip6tables DNAT: the
// servers then see the client's real address, and IPv6 traffic takes the
// same forwarding path as IPv4 instead of Docker's userland proxy on the
// host, where a host firewall such as ufw blocked it.
const PublicNetwork = "openplex_public"

// publicV6Subnet is its IPv6 range when the engine has no IPv6 pool.
const publicV6Subnet = "fd6f:706c:7865:7075::/64"

// ensurePublicNetwork creates the public network when it is missing.
func (o *Orchestrator) ensurePublicNetwork(ctx context.Context) error {
	o.netMu.Lock()
	defer o.netMu.Unlock()
	_, err := o.cli.NetworkInspect(ctx, PublicNetwork, network.InspectOptions{})
	if err == nil {
		return nil
	}
	if !client.IsErrNotFound(err) {
		return fmt.Errorf("inspect network %s: %w", PublicNetwork, err)
	}
	return o.createDualStackNetwork(ctx, PublicNetwork, publicV6Subnet, nil, false)
}

// publicEndpoint puts a container on the public network.
func publicEndpoint() *network.NetworkingConfig {
	return &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{PublicNetwork: {}}}
}
