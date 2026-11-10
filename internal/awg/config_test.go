package awg

import (
	"strings"
	"testing"
)

const testKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
const testPeerKey = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="

func configWith(extra string) string {
	return `[Interface]
PrivateKey = ` + testKey + `
Address = 10.10.0.2/32
Jc = 6
Jmin = 10
Jmax = 80
S1 = 76
S2 = 47
S3 = 33
S4 = 12
H1 = 1
H2 = 2
H3 = 3
H4 = 4
` + extra + `

[Peer]
PublicKey = ` + testPeerKey + `
PresharedKey = ` + testKey + `
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = vpn.example.com:51820
PersistentKeepalive = 25
`
}

func TestDetectsSupportedGenerations(t *testing.T) {
	tests := []struct {
		name    string
		extra   string
		version Version
	}{
		{name: "2.1", version: Version21},
		{name: "3.0", extra: "HeaderProtectionKey = " + testKey, version: Version30},
		{name: "3.1", extra: "HeaderProtectionKey = " + testKey + "\nRandomTrailers = on\nDisableCookies = on", version: Version31},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := Parse(configWith(test.extra))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Version != test.version {
				t.Fatalf("got %s, want %s", cfg.Version, test.version)
			}
		})
	}
}

func TestRejectsCommandHooks(t *testing.T) {
	_, err := Parse(strings.Replace(configWith(""), "Address = 10.10.0.2/32", "Address = 10.10.0.2/32\nPostUp = calc.exe", 1))
	if err == nil || !strings.Contains(err.Error(), "PostUp") {
		t.Fatalf("expected PostUp rejection, got %v", err)
	}
}

func TestHeaderProtectionRequiresPadding(t *testing.T) {
	_, err := Parse(strings.Replace(configWith("HeaderProtectionKey = "+testKey), "S4 = 12", "S4 = 8", 1))
	if err == nil || !strings.Contains(err.Error(), "S4") {
		t.Fatalf("expected S4 validation error, got %v", err)
	}
}

func TestUnknownFieldRejectedByOfficialParser(t *testing.T) {
	_, err := Parse(configWith("FutureAwgField = enabled"))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "futureawgfield") {
		t.Fatalf("expected official parser rejection, got %v", err)
	}
}

func TestSplitRoutingUsesHalfDefaultRoutesAndPreventsIPv6Leak(t *testing.T) {
	cfg, err := Parse(configWith(""))
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := cfg.ForSplitRouting()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten, "AllowedIPs = 0.0.0.0/1, 128.0.0.0/1") {
		t.Fatalf("IPv4 split routes missing:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "::/1, 8000::/1") {
		t.Fatal("IPv6 must be captured fail-closed even when the peer has no IPv6 address")
	}
}

func TestSplitRoutingIncludesIPv6WithTunnelAddress(t *testing.T) {
	raw := strings.Replace(configWith(""), "Address = 10.10.0.2/32", "Address = 10.10.0.2/32, fd00::2/128", 1)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := cfg.ForSplitRouting()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten, "::/1, 8000::/1") {
		t.Fatalf("IPv6 split routes missing:\n%s", rewritten)
	}
}

func TestSplitRoutingStripsInterfaceDNS(t *testing.T) {
	cfg, err := Parse(configWith("DNS = 1.1.1.1, 2606:4700:4700::1111"))
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := cfg.ForSplitRouting()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(rewritten), "dns =") {
		t.Fatalf("expected runtime split config to omit [Interface] DNS, got:\n%s", rewritten)
	}
	if !strings.Contains(cfg.Raw, "2606:4700:4700::1111") {
		t.Fatal("original Config.Raw must remain untouched")
	}
}
