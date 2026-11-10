//go:build windows

package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"

	"nexusclient/internal/awg"
	"nexusclient/internal/debuglog"
	"nexusclient/internal/routing"
	"nexusclient/internal/tunnel"
	"nexusclient/internal/windowsroutes"
)

type Session struct {
	routes          *windowsroutes.Manager
	udpDNS          *dns.Server
	tcpDNS          *dns.Server
	dnsRouter       *routing.DNSRouter
	routingMu       sync.Mutex
	routingID       string
	routingSource   string
	routingFallback atomic.Bool

	refreshCancel context.CancelFunc
	refreshDone   chan struct{}
	closeOnce     sync.Once
	closeErr      error
}

type RouteCheck struct {
	Domain    string
	Action    routing.Action
	Rule      string
	Addresses []string
	Paths     []string
}

func (session *Session) ProxyHealth() routing.ProxyHealth {
	if session == nil || session.dnsRouter == nil {
		return routing.ProxyHealth{}
	}
	return session.dnsRouter.ProxyHealth()
}

func (session *Session) ResetTransports() {
	if session != nil && session.dnsRouter != nil {
		session.dnsRouter.ResetTransports()
	}
}

func (session *Session) CheckRoute(ctx context.Context, domain string) (RouteCheck, error) {
	if session == nil || session.dnsRouter == nil || session.routes == nil {
		return RouteCheck{}, errors.New("сначала подключите VPN")
	}
	explanation, err := session.dnsRouter.ExplainDomain(ctx, domain)
	if err != nil {
		return RouteCheck{}, err
	}
	result := RouteCheck{Domain: explanation.Domain, Action: explanation.Action, Rule: explanation.Rule}
	for _, address := range explanation.Addresses {
		index, routeErr := windowsroutes.BestRouteIndex(address)
		result.Addresses = append(result.Addresses, address.String())
		if routeErr != nil {
			result.Paths = append(result.Paths, "unavailable")
			debuglog.Error("routing.check.bestRoute", routeErr)
			continue
		}
		if index == session.routes.TunnelIndex() {
			result.Paths = append(result.Paths, "exwg")
		} else {
			result.Paths = append(result.Paths, "direct")
		}
	}
	return result, nil
}

const (
	dnsRouteRefreshInterval      = 30 * time.Minute
	routingFallbackRetryInterval = time.Minute
	routingNormalRefreshInterval = time.Hour
)

func Start(ctx context.Context, configText, routingInput, routingProfileID, routingOverrides string) (started *Session, startErr error) {
	debuglog.Event("session.Start", "profileID=%s routingInputBytes=%d overrideBytes=%d", routingProfileID, len(routingInput), len(routingOverrides))
	defer func() { debuglog.Error("session.Start", startErr) }()
	config, err := awg.Parse(configText)
	if err != nil {
		return nil, err
	}
	preparedConfig, err := config.ForSplitRouting()
	if err != nil {
		return nil, err
	}
	debuglog.Event("AWG.config", "generation=%s peers=%d config validated; split configuration prepared", config.Version, len(config.Peers))

	loader := routing.NewLoader()
	cache, err := routing.NewCache(routingProfileID)
	if err != nil {
		return nil, err
	}
	// Connecting never waits for the remote routing service. Prefer the last
	// validated cache; if it is unavailable, start fail-closed as full proxy with
	// the local rules that do not require GeoSite/GeoIP data. The background
	// refresh below retries through the established tunnel and hot-applies the
	// complete bundle when it becomes reachable.
	profile, matcher, overrides, bundleUpdatedAt, fallback, err := loadRoutingOrFallback(cache, routingInput, routingOverrides)
	if err != nil {
		return nil, err
	}
	debuglog.Event("routing.compiled", "profile=%+v cacheTime=%s overrideBytes=%d fallback=%t", profile, bundleUpdatedAt, len(routingOverrides), fallback)

	if err := tunnel.StopCompetingVPNs(ctx); err != nil {
		return nil, err
	}
	if _, err := tunnel.InstallAndStart(ctx, preparedConfig); err != nil {
		return nil, err
	}
	session := &Session{routingID: routingProfileID, routingSource: routingInput}
	session.routingFallback.Store(fallback)
	fail := func(cause error) (*Session, error) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		debuglog.Error("session.start.rollback", tunnel.Stop(cleanupCtx))
		return nil, cause
	}

	for attempt := 0; attempt < 50; attempt++ {
		session.routes, err = windowsroutes.New(ctx, tunnel.ProfileName)
		if err == nil {
			break
		}
		debuglog.Error(fmt.Sprintf("routes.discover.attempt.%d", attempt+1), err)
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-time.After(40 * time.Millisecond):
		}
	}
	if session.routes == nil {
		return fail(fmt.Errorf("AWG-интерфейс не появился: %w", err))
	}

	udpConnection, err := net.ListenPacket("udp", "127.0.0.1:53")
	if err != nil {
		debuglog.Error("session.rollback.routes", session.routes.Close())
		return fail(fmt.Errorf("порт внутреннего DNS занят: %w", err))
	}
	tcpListener, err := net.Listen("tcp", "127.0.0.1:53")
	if err != nil {
		debuglog.Error("session.rollback.udp", udpConnection.Close())
		debuglog.Error("session.rollback.routes", session.routes.Close())
		return fail(fmt.Errorf("порт внутреннего DNS занят: %w", err))
	}
	session.dnsRouter = &routing.DNSRouter{Profile: profile, Matcher: matcher, Overrides: overrides, Routes: session.routes}
	session.routes.SetNetworkChangeHandler(session.dnsRouter.ResetTransports)
	session.udpDNS = &dns.Server{PacketConn: udpConnection, Handler: session.dnsRouter}
	session.tcpDNS = &dns.Server{Listener: tcpListener, Handler: session.dnsRouter}
	go func() { debuglog.Error("DNS.UDP.ActivateAndServe", session.udpDNS.ActivateAndServe()) }()
	go func() { debuglog.Error("DNS.TCP.ActivateAndServe", session.tcpDNS.ActivateAndServe()) }()
	debuglog.Event("DNS.listen", "udp=127.0.0.1:53 tcp=127.0.0.1:53")

	if err := session.applyDNSRoutes(ctx, profile, overrides); err != nil {
		debuglog.Error("session.rollback", session.Close())
		return nil, err
	}
	if err := session.routes.UseLocalDNS(ctx); err != nil {
		debuglog.Error("session.rollback", session.Close())
		return nil, err
	}
	if err := windowsroutes.FlushDNSCache(); err != nil {
		debuglog.Error("session.rollback", session.Close())
		return nil, fmt.Errorf("не удалось очистить DNS-кэш Windows: %w", err)
	}
	refreshContext, refreshCancel := context.WithCancel(context.Background())
	session.refreshCancel = refreshCancel
	session.refreshDone = make(chan struct{})
	go session.dnsRouter.Warmup(refreshContext)
	go session.refreshRouting(refreshContext, loader, cache, routingInput, routingOverrides, profile, bundleUpdatedAt, fallback)
	debuglog.Event("session.Start", "completed")
	return session, nil
}

// ReloadRouting applies an already validated cache generation to the active
// DNS/router state without restarting AWG. Non-bootstrap kernel route leases
// are pruned so updated Direct/Proxy/Block rules take effect immediately.
func (session *Session) ReloadRouting(ctx context.Context, routingOverrides string) error {
	session.routingMu.Lock()
	defer session.routingMu.Unlock()
	cache, err := routing.NewCache(session.routingID)
	if err != nil {
		return err
	}
	profile, matcher, overrides, updatedAt, fallback, err := loadRoutingOrFallback(cache, session.routingSource, routingOverrides)
	if err != nil {
		return err
	}
	if err := session.reconcileDNSRoutes(ctx, profile, overrides); err != nil {
		return err
	}
	session.dnsRouter.Update(profile, matcher, overrides)
	go session.dnsRouter.Warmup(context.Background())
	session.routingFallback.Store(fallback)
	if err := windowsroutes.FlushDNSCache(); err != nil {
		return err
	}
	debuglog.Event("routing.hotUpdate", "manual cacheTime=%s fallback=%t", updatedAt, fallback)
	return nil
}

func (session *Session) UsingRoutingFallback() bool {
	if session == nil {
		return false
	}
	return session.routingFallback.Load()
}

func (session *Session) Close() error {
	session.closeOnce.Do(func() {
		debuglog.Event("session.Close", "begin: DNS, routes and AWG cleanup")
		if session.refreshCancel != nil {
			session.refreshCancel()
		}
		if session.refreshDone != nil {
			select {
			case <-session.refreshDone:
			case <-time.After(time.Second):
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if session.udpDNS != nil {
			session.closeErr = errors.Join(session.closeErr, session.udpDNS.ShutdownContext(ctx))
		}
		if session.tcpDNS != nil {
			session.closeErr = errors.Join(session.closeErr, session.tcpDNS.ShutdownContext(ctx))
		}
		if session.routes != nil {
			session.closeErr = errors.Join(session.closeErr, session.routes.Close())
		}
		session.closeErr = errors.Join(session.closeErr, tunnel.Stop(ctx))
		if session.routes != nil {
			session.closeErr = errors.Join(session.closeErr, session.routes.RestoreDNS(ctx))
		}
		session.closeErr = errors.Join(session.closeErr, windowsroutes.FlushDNSCache())
		debuglog.Error("session.Close", session.closeErr)
		debuglog.Event("session.Close", "completed; error=%v", session.closeErr)
	})
	return session.closeErr
}

func (session *Session) applyDNSRoutes(ctx context.Context, profile *routing.Profile, overrides routing.Overrides) error {
	addresses, err := routing.DNSBootstrapAddresses(profile, overrides)
	if err != nil {
		return err
	}
	for _, action := range []routing.Action{routing.ActionDirect, routing.ActionProxy} {
		if len(addresses[action]) == 0 {
			continue
		}
		if err := session.routes.Apply(ctx, action, addresses[action], time.Hour); err != nil {
			return err
		}
	}
	return nil
}

func (session *Session) reconcileDNSRoutes(ctx context.Context, profile *routing.Profile, overrides routing.Overrides) error {
	addresses, err := routing.DNSBootstrapAddresses(profile, overrides)
	if err != nil {
		return err
	}
	if session.routes != nil {
		debuglog.Error("routing.pruneLeases", session.routes.PruneLeases(ctx, addresses))
	}
	for _, action := range []routing.Action{routing.ActionDirect, routing.ActionProxy} {
		if len(addresses[action]) == 0 {
			continue
		}
		if err := session.routes.Apply(ctx, action, addresses[action], time.Hour); err != nil {
			return err
		}
	}
	return nil
}

func (session *Session) refreshRouting(ctx context.Context, loader *routing.Loader, cache *routing.Cache, routingInput, routingOverrides string, profile *routing.Profile, updatedAt time.Time, fallback bool) {
	defer close(session.refreshDone)
	dnsTicker := time.NewTicker(dnsRouteRefreshInterval)
	firstRefresh := routingNormalRefreshInterval
	if fallback {
		firstRefresh = 3 * time.Second
	}
	routingTimer := time.NewTimer(firstRefresh)
	defer dnsTicker.Stop()
	defer routingTimer.Stop()
	currentProfile := profile
	currentOverrides, _ := routing.ParseOverrides(routingOverrides)
	for {
		select {
		case <-ctx.Done():
			return
		case <-dnsTicker.C:
			operationContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			debuglog.Error("routing.refreshDNSRoutes", session.applyDNSRoutes(operationContext, currentProfile, currentOverrides))
			cancel()
		case <-routingTimer.C:
			operationContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
			session.routingMu.Lock()
			bundle, bundleUpdatedAt, err := cache.LoadOrRefresh(operationContext, loader, routingInput, routing.CacheMaxAge)
			var updatedProfile *routing.Profile
			var updatedMatcher *routing.Matcher
			var updatedOverrides routing.Overrides
			if err == nil && bundleUpdatedAt.After(updatedAt) {
				updatedProfile, updatedMatcher, updatedOverrides, err = compileRouting(bundle, routingOverrides)
			}
			if err == nil && updatedProfile != nil {
				err = session.reconcileDNSRoutes(operationContext, updatedProfile, updatedOverrides)
			}
			if err == nil && updatedProfile != nil {
				session.dnsRouter.Update(updatedProfile, updatedMatcher, updatedOverrides)
				go session.dnsRouter.Warmup(ctx)
				session.routingFallback.Store(false)
				fallback = false
				currentProfile = updatedProfile
				currentOverrides = updatedOverrides
				updatedAt = bundleUpdatedAt
				debuglog.Error("routing.FlushDNSCache", windowsroutes.FlushDNSCache())
				debuglog.Event("routing.hotUpdate", "applied cacheTime=%s", updatedAt)
			}
			debuglog.Error("routing.hotUpdate", err)
			session.routingMu.Unlock()
			cancel()
			nextRefresh := routingNormalRefreshInterval
			if fallback {
				nextRefresh = routingFallbackRetryInterval
			}
			routingTimer.Reset(nextRefresh)
		}
	}
}

func loadRoutingOrFallback(cache *routing.Cache, source, routingOverrides string) (*routing.Profile, *routing.Matcher, routing.Overrides, time.Time, bool, error) {
	bundle, updatedAt, unavailableErr := cache.Load(source)
	if unavailableErr == nil {
		profile, matcher, overrides, err := compileRouting(bundle, routingOverrides)
		if err == nil {
			return profile, matcher, overrides, updatedAt, false, nil
		}
		unavailableErr = err
	}

	profile, matcher, overrides, skippedGeo, err := compileFallbackRouting(routingOverrides)
	if err != nil {
		return nil, nil, routing.Overrides{}, time.Time{}, false, errors.Join(
			fmt.Errorf("полные правила маршрутизации недоступны: %w", unavailableErr),
			err,
		)
	}
	debuglog.Error("routing.fallback.reason", unavailableErr)
	debuglog.Event("routing.fallback", "full proxy with local rules; skippedGeoRules=%d", skippedGeo)
	return profile, matcher, overrides, time.Time{}, true, nil
}

func compileFallbackRouting(input string) (*routing.Profile, *routing.Matcher, routing.Overrides, int, error) {
	if _, err := routing.ParseOverrides(input); err != nil {
		return nil, nil, routing.Overrides{}, 0, fmt.Errorf("локальные правила: %w", err)
	}
	var kept []string
	skippedGeo := 0
	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			value := strings.ToLower(strings.TrimSpace(strings.Join(parts[1:], " ")))
			if strings.HasPrefix(value, "geosite:") || strings.HasPrefix(value, "geoip:") {
				skippedGeo++
				continue
			}
		}
		kept = append(kept, line)
	}
	filtered := strings.Join(kept, "\n")
	profile := &routing.Profile{
		Name: "Local fallback", GlobalProxy: routing.BoolValue(true),
		RouteOrder: "block-proxy-direct", DomainStrategy: "IPIfNonMatch",
		RemoteDNSType: "doh", RemoteDNSDomain: "dns.google", RemoteDNSIP: "8.8.8.8",
		DomesticDNSType: "doh", DomesticDNSDomain: "cloudflare-dns.com", DomesticDNSIP: "1.1.1.1",
		DNSHosts: make(map[string]string),
	}
	if err := profile.Validate(); err != nil {
		return nil, nil, routing.Overrides{}, skippedGeo, err
	}
	matcher, err := routing.CompileMatcherWithOverrides(profile, nil, nil, filtered)
	if err != nil {
		return nil, nil, routing.Overrides{}, skippedGeo, err
	}
	overrides, err := routing.ParseOverrides(filtered)
	if err != nil {
		return nil, nil, routing.Overrides{}, skippedGeo, err
	}
	return profile, matcher, overrides, skippedGeo, nil
}

var (
	compiledRoutingMu sync.Mutex
	compiledRouting   struct {
		bundle    *routing.Bundle
		overrides string
		profile   *routing.Profile
		matcher   *routing.Matcher
		parsed    routing.Overrides
	}
)

func compileRouting(bundle *routing.Bundle, routingOverrides string) (*routing.Profile, *routing.Matcher, routing.Overrides, error) {
	if bundle == nil || bundle.Profile == nil {
		return nil, nil, routing.Overrides{}, errors.New("routing-пакет пуст")
	}
	profile := bundle.Profile
	if bool(profile.FakeDNS) {
		return nil, nil, routing.Overrides{}, errors.New("FakeDNS требует пакетный userspace-router; эта Windows-сборка не включает его и не будет имитировать поддержку")
	}
	compiledRoutingMu.Lock()
	if compiledRouting.bundle == bundle && compiledRouting.overrides == routingOverrides && compiledRouting.profile != nil && compiledRouting.matcher != nil {
		cachedProfile := compiledRouting.profile
		cachedMatcher := compiledRouting.matcher
		cachedOverrides := compiledRouting.parsed
		compiledRoutingMu.Unlock()
		return cachedProfile, cachedMatcher, cachedOverrides, nil
	}
	compiledRoutingMu.Unlock()
	matcher, err := routing.CompileMatcherWithOverrides(profile, bundle.GeoSite, bundle.GeoIP, routingOverrides)
	if err != nil {
		return nil, nil, routing.Overrides{}, err
	}
	overrides, err := routing.ParseOverrides(routingOverrides)
	if err != nil {
		return nil, nil, routing.Overrides{}, err
	}
	compiledRoutingMu.Lock()
	compiledRouting.bundle = bundle
	compiledRouting.overrides = routingOverrides
	compiledRouting.profile = profile
	compiledRouting.matcher = matcher
	compiledRouting.parsed = overrides
	compiledRoutingMu.Unlock()
	return profile, matcher, overrides, nil
}
