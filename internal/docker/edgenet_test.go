package docker

import (
	"reflect"
	"testing"

	"github.com/docker/docker/api/types/network"
)

func TestEdgeNetworkAttempts(t *testing.T) {
	old := []network.IPAMConfig{{Subnet: "172.18.0.0/16", Gateway: "172.18.0.1"}}
	type want struct {
		ipv6 bool
		ipam []network.IPAMConfig // nil: the engine picks
	}
	tests := []struct {
		name     string
		v4       []network.IPAMConfig
		ipv6Only bool
		want     []want
	}{
		{"fresh", nil, false, []want{
			{true, nil},
			{true, []network.IPAMConfig{{Subnet: edgeV6Subnet}}},
			{false, nil},
		}},
		{"keeps the IPv4 range", old, false, []want{
			{true, old},
			{true, append(append([]network.IPAMConfig(nil), old...), network.IPAMConfig{Subnet: edgeV6Subnet})},
			{false, old},
		}},
		{"IPv6 only (probe)", nil, true, []want{
			{true, nil},
			{true, []network.IPAMConfig{{Subnet: edgeV6Subnet}}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := edgeNetworkAttempts(tt.v4, tt.ipv6Only)
			if len(got) != len(tt.want) {
				t.Fatalf("%d attempts, want %d", len(got), len(tt.want))
			}
			for i, g := range got {
				w := tt.want[i]
				if g.Driver != "bridge" || g.Internal || g.Labels[LabelManaged] != "true" {
					t.Errorf("attempt %d: %+v", i, g)
				}
				if v6 := g.EnableIPv6 != nil && *g.EnableIPv6; v6 != w.ipv6 {
					t.Errorf("attempt %d: ipv6 = %v, want %v", i, v6, w.ipv6)
				}
				var ipam []network.IPAMConfig
				if g.IPAM != nil {
					ipam = g.IPAM.Config
				}
				if !reflect.DeepEqual(ipam, w.ipam) {
					t.Errorf("attempt %d: ipam = %+v, want %+v", i, ipam, w.ipam)
				}
			}
		})
	}
}

func TestIPv4Ranges(t *testing.T) {
	in := []network.IPAMConfig{
		{Subnet: "172.18.0.0/16", Gateway: "172.18.0.1", AuxAddress: map[string]string{"x": "172.18.0.9"}},
		{Subnet: "fd39:ff54:8444::/64", Gateway: "fd39:ff54:8444::1"},
		{Subnet: ""},
	}
	want := []network.IPAMConfig{{Subnet: "172.18.0.0/16", Gateway: "172.18.0.1"}}
	if got := ipv4Ranges(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("ipv4Ranges = %+v, want %+v", got, want)
	}
	if got := ipv4Ranges(nil); got != nil {
		t.Fatalf("ipv4Ranges(nil) = %+v", got)
	}
}
