//go:build windows

package session

import (
	"net/netip"
	"testing"
	"time"

	"nexusclient/internal/routing"
)

func TestLoadRoutingOrFallbackStartsWithoutCache(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	cache, err := routing.NewCache("missing-cache")
	if err != nil {
		t.Fatal(err)
	}
	profile, matcher, _, updatedAt, fallback, err := loadRoutingOrFallback(cache, "https://routing.example/profile", "direct apple.com")
	if err != nil {
		t.Fatal(err)
	}
	if !fallback || !updatedAt.Equal(time.Time{}) || !bool(profile.GlobalProxy) {
		t.Fatalf("fallback=%t updatedAt=%v globalProxy=%t", fallback, updatedAt, profile.GlobalProxy)
	}
	if got := matcher.Domain("apple.com"); got != routing.ActionDirect {
		t.Fatalf("apple.com=%s, want direct", got)
	}
}

func TestCompileFallbackRoutingKeepsLocalRulesAndDefaultsToProxy(t *testing.T) {
	input := "direct apple.com\nblock 203.0.113.7\ndns-proxy https://dns.google/dns-query 8.8.8.8"
	profile, matcher, overrides, skipped, err := compileFallbackRouting(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bool(profile.GlobalProxy) || skipped != 0 {
		t.Fatalf("globalProxy=%t skipped=%d", profile.GlobalProxy, skipped)
	}
	if got := matcher.Domain("apple.com"); got != routing.ActionDirect {
		t.Fatalf("apple.com=%s, want direct", got)
	}
	if got := matcher.IP(netip.MustParseAddr("203.0.113.7")); got != routing.ActionBlock {
		t.Fatalf("203.0.113.7=%s, want block", got)
	}
	if got := matcher.Domain("example.net"); got != routing.ActionProxy {
		t.Fatalf("default=%s, want proxy", got)
	}
	if got := overrides.DNS["dns-proxy"]; len(got) != 1 || got[0] != "https://dns.google/dns-query 8.8.8.8" {
		t.Fatalf("dns-proxy=%v", got)
	}
}

func TestCompileFallbackRoutingSkipsOnlyGeoRules(t *testing.T) {
	input := "proxy geosite:apple\ndirect geoip:private\nblock ads.example"
	_, matcher, _, skipped, err := compileFallbackRouting(input)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 2 {
		t.Fatalf("skipped=%d, want 2", skipped)
	}
	if got := matcher.Domain("ads.example"); got != routing.ActionBlock {
		t.Fatalf("ads.example=%s, want block", got)
	}
}

func TestCompileFallbackRoutingRejectsInvalidLocalRule(t *testing.T) {
	if _, _, _, _, err := compileFallbackRouting("teleport example.com"); err == nil {
		t.Fatal("invalid local action was accepted")
	}
}
