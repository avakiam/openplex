package docker

import (
	"net/netip"
	"testing"
)

func TestNextFreeSubnet(t *testing.T) {
	pool := netip.MustParsePrefix("10.213.0.0/22")
	tests := []struct {
		used []string
		want string
	}{
		{nil, "10.213.0.0/24"},
		{[]string{"10.213.0.0/24"}, "10.213.1.0/24"},
		{[]string{"10.213.0.0/24", "10.213.1.128/25"}, "10.213.2.0/24"},
		{[]string{"10.0.0.0/8"}, ""},
		{[]string{"172.17.0.0/16"}, "10.213.0.0/24"},
	}
	for _, tt := range tests {
		var used []netip.Prefix
		for _, u := range tt.used {
			used = append(used, netip.MustParsePrefix(u))
		}
		got, err := nextFreeSubnet(pool, used)
		if tt.want == "" {
			if err == nil {
				t.Errorf("used %v: want pool full, got %s", tt.used, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("used %v: got %q, %v; want %s", tt.used, got, err, tt.want)
		}
	}
}
