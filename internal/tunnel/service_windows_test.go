//go:build windows

package tunnel

import "testing"

func TestIsCompetingTunnelService(t *testing.T) {
	tests := map[string]bool{
		ServiceName:                false,
		"NexusAWGTunnel$Old":       true,
		"WireGuardTunnel$Work":     true,
		"AmneziaWGTunnel$Personal": true,
		"WintunUserspace":          false,
		"Dhcp":                     false,
	}
	for name, want := range tests {
		if got := isCompetingTunnelService(name); got != want {
			t.Errorf("isCompetingTunnelService(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestResolveOwnerPID(t *testing.T) {
	if got := resolveOwnerPID([]int{4242}, []string{"ExWGCore.exe", "/tunnelservice", "Nexus.conf", "9999"}); got != 4242 {
		t.Fatalf("resolveOwnerPID explicit = %d, want 4242", got)
	}
	if got := resolveOwnerPID(nil, []string{"ExWGCore.exe", "/tunnelservice", "Nexus.conf", "9999"}); got != 9999 {
		t.Fatalf("resolveOwnerPID args = %d, want 9999", got)
	}
	if got := resolveOwnerPID(nil, []string{"ExWGCore.exe", "/tunnelservice", "Nexus.conf"}); got != 0 {
		t.Fatalf("resolveOwnerPID without pid = %d, want 0", got)
	}
}
