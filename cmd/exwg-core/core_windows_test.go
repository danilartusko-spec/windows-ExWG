//go:build windows

package main

import (
	"errors"
	"testing"
	"time"

	"nexusclient/internal/profiles"
	"nexusclient/internal/routing"
	"nexusclient/internal/tunnel"
)

func TestRuntimeHealthFailureNeedsThreeLocalReadErrors(t *testing.T) {
	readErr := errors.New("uapi unavailable")
	if got := runtimeHealthFailure(2, time.Minute, tunnel.RuntimeStats{}, routing.ProxyHealth{}, readErr); got != nil {
		t.Fatalf("two local read errors must be tolerated: %v", got)
	}
	if got := runtimeHealthFailure(3, time.Minute, tunnel.RuntimeStats{}, routing.ProxyHealth{}, readErr); got == nil {
		t.Fatal("three local read errors must mark the session lost")
	}
}

func TestRuntimeHealthFailureIsPassiveAndHandshakeAware(t *testing.T) {
	health := routing.ProxyHealth{ConsecutiveFailures: 3}
	if got := runtimeHealthFailure(0, 20*time.Second, tunnel.RuntimeStats{}, health, nil); got != nil {
		t.Fatalf("startup grace period must be preserved: %v", got)
	}
	fresh := tunnel.RuntimeStats{LastHandshake: time.Now().Add(-time.Minute)}
	if got := runtimeHealthFailure(0, time.Minute, fresh, health, nil); got != nil {
		t.Fatalf("a fresh handshake must keep a working tunnel online: %v", got)
	}
	stale := tunnel.RuntimeStats{LastHandshake: time.Now().Add(-4 * time.Minute)}
	if got := runtimeHealthFailure(0, time.Minute, stale, health, nil); got == nil {
		t.Fatal("repeated real proxy failures plus a stale handshake must fail")
	}
}

func TestInitialRoutingSettingsRecoversRulesAfterSelectingNewProfile(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	items := []profiles.Profile{
		{ID: "new", Routing: defaultRoutingSource, UpdatedAt: time.Now()},
		{ID: "old", Routing: defaultRoutingSource, RoutingOverrides: "proxy domain:updates.cdn-apple.com", UpdatedAt: old},
	}
	settings := initialRoutingSettings(items, "new")
	if settings.Source != defaultRoutingSource || settings.Overrides != items[1].RoutingOverrides {
		t.Fatalf("settings=%#v", settings)
	}
}

func TestInitialRoutingSettingsPrefersSelectedRules(t *testing.T) {
	items := []profiles.Profile{
		{ID: "selected", Routing: "https://example.com/routing", RoutingOverrides: "direct domain:example.com", UpdatedAt: time.Now().Add(-time.Hour)},
		{ID: "newer", Routing: defaultRoutingSource, RoutingOverrides: "block domain:example.com", UpdatedAt: time.Now()},
	}
	settings := initialRoutingSettings(items, "selected")
	if settings.Source != items[0].Routing || settings.Overrides != items[0].RoutingOverrides {
		t.Fatalf("settings=%#v", settings)
	}
}

func TestCountNonEmptyLines(t *testing.T) {
	if got := countNonEmptyLines("proxy domain:a.example\n\n direct domain:b.example \n"); got != 2 {
		t.Fatalf("count=%d, want 2", got)
	}
}

func TestSeedRoutingCacheReusesNewestProfileCache(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	source := "https://routing.example/profile"
	updatedAt := time.Now().Add(-time.Hour).Round(time.Second)
	oldCache, err := routing.NewCache("old")
	if err != nil {
		t.Fatal(err)
	}
	bundle := &routing.Bundle{Profile: &routing.Profile{Name: "last known good", GlobalProxy: true}}
	if err := oldCache.Store(source, updatedAt, bundle); err != nil {
		t.Fatal(err)
	}
	app := &ExWGApp{items: []profiles.Profile{{ID: "old"}, {ID: "new"}}}
	if !app.seedRoutingCache(sharedRoutingCacheID, source) {
		t.Fatal("valid cache was not migrated into shared global cache")
	}
	sharedCache, err := routing.NewCache(sharedRoutingCacheID)
	if err != nil {
		t.Fatal(err)
	}
	loaded, loadedAt, err := sharedCache.Load(source)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profile.Name != bundle.Profile.Name || !loadedAt.Equal(updatedAt) {
		t.Fatalf("loaded=%q at %v", loaded.Profile.Name, loadedAt)
	}
}

func TestRoutingDetailsWorksWithoutProfiles(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	source := "https://routing.example/profile"
	updatedAt := time.Now().Round(time.Second)
	sharedCache, err := routing.NewCache(sharedRoutingCacheID)
	if err != nil {
		t.Fatal(err)
	}
	bundle := &routing.Bundle{Profile: &routing.Profile{Name: "RoscomVPN", GlobalProxy: true}}
	if err := sharedCache.Store(source, updatedAt, bundle); err != nil {
		t.Fatal(err)
	}
	app := &ExWGApp{routingSource: source}
	details, err := app.RoutingDetails()
	if err != nil {
		t.Fatalf("RoutingDetails returned error with 0 profiles: %v", err)
	}
	if details.ProfileName != "RoscomVPN" {
		t.Fatalf("ProfileName=%q, want RoscomVPN", details.ProfileName)
	}
}

func TestDetectSystemWake(t *testing.T) {
	base := time.Now()
	if detectSystemWake(base, base.Add(2*time.Second)) {
		t.Fatal("normal 1-2s tick interval must not trigger wake reset")
	}
	if !detectSystemWake(base, base.Add(15*time.Second)) {
		t.Fatal("time jump >5s must be detected as system wake")
	}
}
