package routing

import (
	"net/netip"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

func TestMatcherUsesHappPriorityAndGeoData(t *testing.T) {
	sites, err := proto.Marshal(&geodata.GeoSiteList{Entry: []*geodata.GeoSite{
		{
			Code: "CATEGORY-RU",
			Domain: []*geodata.Domain{
				{Type: geodata.Domain_Domain, Value: "bank.ru"},
			},
		},
		{
			Code: "YOUTUBE",
			Domain: []*geodata.Domain{
				{Type: geodata.Domain_Domain, Value: "youtube.com"},
			},
		},
		{
			Code: "ADS",
			Domain: []*geodata.Domain{
				{Type: geodata.Domain_Domain, Value: "ads.example"},
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ips, err := proto.Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{
		{
			Code: "PRIVATE",
			Cidr: []*geodata.CIDR{{Ip: []byte{10, 0, 0, 0}, Prefix: 8}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	profile := &Profile{
		Name:        "test",
		GlobalProxy: true,
		RouteOrder:  "block-proxy-direct",
		DirectSites: []string{"geosite:category-ru"},
		DirectIP:    []string{"geoip:private"},
		ProxySites:  []string{"geosite:youtube"},
		BlockSites:  []string{"geosite:ads"},
	}
	matcher, err := CompileMatcher(profile, sites, ips)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		domain string
		want   Action
	}{
		{"online.bank.ru", ActionDirect},
		{"www.youtube.com", ActionProxy},
		{"cdn.ads.example", ActionBlock},
		{"foreign.example", ActionProxy},
	}
	for _, test := range tests {
		if got := matcher.Domain(test.domain); got != test.want {
			t.Errorf("Domain(%q) = %s, want %s", test.domain, got, test.want)
		}
	}
	if got := matcher.IP(netip.MustParseAddr("10.20.30.40")); got != ActionDirect {
		t.Fatalf("private IP = %s, want direct", got)
	}
	if got := matcher.IP(netip.MustParseAddr("1.1.1.1")); got != ActionProxy {
		t.Fatalf("public IP = %s, want proxy", got)
	}
}

func TestPlainDomainRulesWorkWithoutGeoData(t *testing.T) {
	profile := &Profile{
		Name:        "plain",
		GlobalProxy: false,
		RouteOrder:  "block-proxy-direct",
		ProxySites:  []string{"domain:example.com", "full:only.example.net"},
		BlockSites:  []string{"regexp:^ads[0-9]+\\."},
	}
	matcher, err := CompileMatcher(profile, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := matcher.Domain("cdn.example.com"); got != ActionProxy {
		t.Fatalf("domain suffix = %s, want proxy", got)
	}
	if got := matcher.Domain("sub.only.example.net"); got != ActionDirect {
		t.Fatalf("full domain unexpectedly matched: %s", got)
	}
	if got := matcher.Domain("ads42.example.org"); got != ActionBlock {
		t.Fatalf("regexp = %s, want block", got)
	}
}

func TestMissingGeoCategoryIsAnError(t *testing.T) {
	_, err := CompileMatcher(&Profile{
		Name:        "missing",
		DirectSites: []string{"geosite:not-found"},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected missing category error")
	}
}

func TestNoHiddenRussianRulesAreAdded(t *testing.T) {
	matcher, err := CompileMatcher(&Profile{
		Name:        "russian-direct",
		GlobalProxy: true,
		RouteOrder:  "block-proxy-direct",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{
		"example.ru",
		"service.gov.ru",
		"пример.рф",
		"xn--e1afmkfd.xn--p1ai",
	} {
		if got := matcher.Domain(domain); got != ActionProxy {
			t.Errorf("Domain(%q) = %s, want profile default proxy", domain, got)
		}
	}
	if got := matcher.Domain("example.com"); got != ActionProxy {
		t.Fatalf("foreign domain = %s, want proxy", got)
	}
}

func TestRussianRulesWorkWhenProfileDeclaresThem(t *testing.T) {
	matcher, err := CompileMatcher(&Profile{
		Name:        "declared",
		GlobalProxy: true,
		DirectSites: []string{"domain:ru", "domain:gov.ru", "domain:xn--p1ai"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"example.ru", "service.gov.ru", "пример.рф"} {
		if got := matcher.Domain(domain); got != ActionDirect {
			t.Fatalf("Domain(%q) = %s, want declared direct", domain, got)
		}
	}
}

func TestExplicitBlockStillWinsInsideRussianZone(t *testing.T) {
	matcher, err := CompileMatcher(&Profile{
		Name:        "priority",
		GlobalProxy: true,
		RouteOrder:  "block-proxy-direct",
		BlockSites:  []string{"domain:blocked.рф"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := matcher.Domain("blocked.рф"); got != ActionBlock {
		t.Fatalf("blocked domain = %s, want block", got)
	}
}

func TestLocalOverridesWinOverDownloadedRules(t *testing.T) {
	matcher, err := CompileMatcherWithOverrides(&Profile{
		GlobalProxy: true,
		RouteOrder:  "block-proxy-direct",
		BlockSites:  []string{"domain:example.com"},
		DirectSites: []string{"domain:video.example"},
	}, nil, nil, "direct domain:example.com\nproxy domain:video.example")
	if err != nil {
		t.Fatal(err)
	}
	if got := matcher.Domain("www.example.com"); got != ActionDirect {
		t.Fatalf("local direct override = %s, want direct", got)
	}
	if got := matcher.Domain("cdn.video.example"); got != ActionProxy {
		t.Fatalf("local proxy override = %s, want proxy", got)
	}
}

func TestLocalOverrideParserAcceptsAddressesAndRejectsBadActions(t *testing.T) {
	overrides, err := ParseOverrides("direct 192.0.2.10\nblock full:ads.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides.DirectIP) != 1 || overrides.DirectIP[0] != "192.0.2.10/32" {
		t.Fatalf("unexpected direct IP rules: %#v", overrides.DirectIP)
	}
	if _, err := ParseOverrides("sometimes example.com"); err == nil {
		t.Fatal("expected unknown action error")
	}
}
