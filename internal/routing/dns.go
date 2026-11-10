package routing

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"nexusclient/internal/debuglog"
)

type RouteApplier interface {
	Apply(context.Context, Action, []netip.Addr, time.Duration) error
}

// IPv6RouteChecker is an optional extension implemented by RouteApplier when
// it knows ahead of time whether a route action (Direct or Proxy) has a usable
// IPv6 path. When false, TypeAAAA queries can be answered locally with NODATA
// without waiting for an upstream DNS round-trip.
type IPv6RouteChecker interface {
	SupportsIPv6(Action) bool
}

// ErrDirectIPv6Unavailable tells the DNS router that Windows has no physical
// IPv6 gateway for a Direct answer. Returning that address would either make
// the connection fail or let it follow the tunnel's default route, so the DNS
// router suppresses only the unreachable IPv6 records instead.
var ErrDirectIPv6Unavailable = errors.New("IPv6-маршрут запрещён: обычный IPv6-шлюз не найден")

type DNSRouter struct {
	Profile   *Profile
	Matcher   *Matcher
	Overrides Overrides
	Routes    RouteApplier
	Client    *http.Client

	mu               sync.RWMutex
	stateMu          sync.Mutex
	clients          map[string]*http.Client
	cache            map[string]dnsCacheEntry
	pending          map[string]*dnsPending
	down             map[string]time.Time
	proxyFailures    int
	lastProxySuccess time.Time
	lastProxyFailure time.Time
}

type ProxyHealth struct {
	ConsecutiveFailures int
	LastSuccess         time.Time
	LastFailure         time.Time
}

type DomainExplanation struct {
	Domain    string
	Action    Action
	Rule      string
	Addresses []netip.Addr
}

func (router *DNSRouter) ProxyHealth() ProxyHealth {
	router.stateMu.Lock()
	defer router.stateMu.Unlock()
	return ProxyHealth{
		ConsecutiveFailures: router.proxyFailures,
		LastSuccess:         router.lastProxySuccess,
		LastFailure:         router.lastProxyFailure,
	}
}

// ExplainDomain resolves a user-requested test domain through the same DNS and
// routing path as normal traffic. It never creates background probe traffic.
func (router *DNSRouter) ExplainDomain(ctx context.Context, domain string) (DomainExplanation, error) {
	domain = normalizeDomain(domain)
	if domain == "" {
		return DomainExplanation{}, errors.New("укажите домен")
	}
	_, matcher := router.snapshot()
	if matcher == nil {
		return DomainExplanation{}, errors.New("DNS-маршрутизация не настроена")
	}
	action := matcher.Domain(domain)
	result := DomainExplanation{Domain: domain, Action: action, Rule: matcher.Trace(domain, netip.Addr{})}
	if action == ActionBlock {
		return result, nil
	}
	queryTypes := []uint16{dns.TypeA, dns.TypeAAAA}
	type queryResult struct {
		response    *dns.Msg
		blockedAddr netip.Addr
		err         error
	}
	var results [2]queryResult
	var wg sync.WaitGroup
	for idx, queryType := range queryTypes {
		wg.Add(1)
		go func(i int, qtype uint16) {
			defer wg.Done()
			request := new(dns.Msg)
			request.SetQuestion(dns.Fqdn(domain), qtype)
			response, blockedAddr, err := router.resolve(ctx, request)
			results[i] = queryResult{response: response, blockedAddr: blockedAddr, err: err}
		}(idx, queryType)
	}
	wg.Wait()

	seen := make(map[netip.Addr]bool)
	var blockedByIP netip.Addr
	for _, item := range results {
		if item.err != nil {
			return DomainExplanation{}, item.err
		}
		if item.blockedAddr.IsValid() && !blockedByIP.IsValid() {
			blockedByIP = item.blockedAddr
		}
		if item.response == nil {
			continue
		}
		for _, answer := range item.response.Answer {
			var address netip.Addr
			switch record := answer.(type) {
			case *dns.A:
				address, _ = netip.AddrFromSlice(record.A)
			case *dns.AAAA:
				address, _ = netip.AddrFromSlice(record.AAAA)
			}
			address = address.Unmap()
			if address.IsValid() && !seen[address] {
				seen[address] = true
				result.Addresses = append(result.Addresses, address)
			}
		}
	}
	if blockedByIP.IsValid() {
		if !seen[blockedByIP] {
			result.Addresses = append([]netip.Addr{blockedByIP}, result.Addresses...)
		}
		result.Action = ActionBlock
		result.Rule = matcher.Trace(domain, blockedByIP)
		return result, nil
	}
	if len(result.Addresses) > 0 {
		result.Action = matcher.Route(domain, result.Addresses[0])
		result.Rule = matcher.Trace(domain, result.Addresses[0])
	}
	return result, nil
}

type dnsEndpoint struct {
	URL       string
	Bootstrap string
}

type dnsCacheEntry struct {
	wire                         []byte
	created, expires, staleUntil time.Time
}

type dnsPending struct {
	done chan struct{}
	wire []byte
	err  error
}

func (router *DNSRouter) Update(profile *Profile, matcher *Matcher, overrideValues ...Overrides) {
	overrides := router.Overrides
	if len(overrideValues) > 0 {
		overrides = overrideValues[0]
	}
	router.mu.Lock()
	router.Profile = profile
	router.Matcher = matcher
	router.Overrides = overrides
	router.mu.Unlock()
	router.stateMu.Lock()
	clear(router.cache)
	clear(router.pending)
	clear(router.down)
	router.proxyFailures = 0
	router.stateMu.Unlock()
}

// Warmup pre-establishes upstream DNS transports (such as DoH HTTP/2 sessions)
// for Direct and Proxy routes and sends an initial packet through the tunnel so
// AmneziaWG initiates its handshake immediately upon connect.
func (router *DNSRouter) Warmup(ctx context.Context) {
	profile, _, overrides := router.snapshotWithOverrides()
	if profile == nil {
		return
	}
	router.ensureState()
	var wg sync.WaitGroup
	for _, action := range []Action{ActionDirect, ActionProxy} {
		endpoints, err := dnsEndpoints(profile, overrides, action)
		if err != nil || len(endpoints) == 0 {
			continue
		}
		endpoint := endpoints[0]
		wg.Add(1)
		go func(act Action, ep dnsEndpoint) {
			defer wg.Done()
			warmCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			probe := new(dns.Msg)
			probe.SetQuestion(".", dns.TypeNS)
			started := time.Now()
			if _, proto, err := router.exchangeAt(warmCtx, probe, act, ep); err == nil {
				debuglog.Event("DNS.warmup", "route=%s resolver=%s protocol=%s elapsed=%s", act, ep.URL, proto, time.Since(started))
			}
		}(action, endpoint)
	}
	wg.Wait()
}

// ResetTransports closes idle HTTP/2 DoH connections and clears resolver
// cooldowns and consecutive failure counters after a network transition or
// system wake-up.
func (router *DNSRouter) ResetTransports() {
	router.stateMu.Lock()
	for _, client := range router.clients {
		client.CloseIdleConnections()
	}
	clear(router.clients)
	clear(router.down)
	router.proxyFailures = 0
	router.stateMu.Unlock()
}

func (router *DNSRouter) snapshot() (*Profile, *Matcher) {
	router.mu.RLock()
	defer router.mu.RUnlock()
	return router.Profile, router.Matcher
}

func (router *DNSRouter) snapshotWithOverrides() (*Profile, *Matcher, Overrides) {
	router.mu.RLock()
	defer router.mu.RUnlock()
	return router.Profile, router.Matcher, router.Overrides
}

func (router *DNSRouter) ServeDNS(writer dns.ResponseWriter, request *dns.Msg) {
	debuglog.Event("DNS.request", "remote=%s\n%s", writer.RemoteAddr(), request.String())
	response, err := router.Resolve(context.Background(), request)
	if err != nil {
		debuglog.Error("DNS.Resolve", err)
		response = new(dns.Msg)
		response.SetRcode(request, dns.RcodeServerFailure)
	}
	debuglog.Event("DNS.response", "%s", response.String())
	debuglog.Error("DNS.WriteMsg", writer.WriteMsg(response))
}

func (router *DNSRouter) Resolve(ctx context.Context, request *dns.Msg) (*dns.Msg, error) {
	response, _, err := router.resolve(ctx, request)
	return response, err
}

func (router *DNSRouter) resolve(ctx context.Context, request *dns.Msg) (*dns.Msg, netip.Addr, error) {
	profile, matcher, overrides := router.snapshotWithOverrides()
	if profile == nil || matcher == nil {
		return nil, netip.Addr{}, errors.New("DNS-маршрутизация не настроена")
	}
	if len(request.Question) == 0 {
		return nil, netip.Addr{}, errors.New("DNS-запрос не содержит вопрос")
	}

	domain := normalizeDomain(request.Question[0].Name)
	action := matcher.Domain(domain)
	debuglog.Event("DNS.classify", "domain=%s action=%s", domain, action)
	if action == ActionBlock {
		response := new(dns.Msg)
		response.SetRcode(request, dns.RcodeNameError)
		response.Authoritative = true
		return response, netip.Addr{}, nil
	}
	if request.Question[0].Qtype == dns.TypeAAAA {
		if checker, ok := router.Routes.(IPv6RouteChecker); ok && !checker.SupportsIPv6(action) && (!checker.SupportsIPv6(ActionDirect) && !checker.SupportsIPv6(ActionProxy)) {
			response := new(dns.Msg)
			response.SetReply(request)
			return response, netip.Addr{}, nil
		}
	}

	// A static DnsHosts answer replaces only the upstream lookup. The address
	// must still receive a Direct/Proxy route below; otherwise it silently
	// follows the tunnel's split-default route regardless of the rules.
	var response *dns.Msg
	var err error
	if fixed, ok := profile.DNSHosts[domain]; ok {
		response, err = fixedDNSResponse(request, domain, fixed)
		debuglog.Event("DNS.hosts", "domain=%s address=%s action=%s", domain, fixed, action)
	} else {
		response, err = router.exchangeCached(ctx, profile, overrides, request, action)
	}
	if err != nil {
		return nil, netip.Addr{}, err
	}
	addresses, ttl := answerAddresses(response)
	if len(addresses) > 0 && router.Routes != nil {
		grouped := make(map[Action][]netip.Addr)
		for _, address := range addresses {
			resolvedAction := matcher.Route(domain, address)
			if resolvedAction == ActionBlock {
				response.SetRcode(request, dns.RcodeNameError)
				response.Authoritative = true
				response.Answer = nil
				return response, address.Unmap(), nil
			}
			grouped[resolvedAction] = append(grouped[resolvedAction], address)
		}
		for resolvedAction, routedAddresses := range grouped {
			var ipv4, ipv6 []netip.Addr
			for _, address := range routedAddresses {
				if address.Is4() {
					ipv4 = append(ipv4, address)
				} else {
					ipv6 = append(ipv6, address)
				}
			}
			if len(ipv4) > 0 {
				debuglog.Event("DNS.routes", "domain=%s action=%s addresses=%v ttl=%s", domain, resolvedAction, ipv4, ttl)
				if err := router.Routes.Apply(ctx, resolvedAction, ipv4, ttl); err != nil {
					return nil, netip.Addr{}, fmt.Errorf("не удалось применить маршрут для %s: %w", domain, err)
				}
			}
			if len(ipv6) > 0 {
				debuglog.Event("DNS.routes", "domain=%s action=%s addresses=%v ttl=%s", domain, resolvedAction, ipv6, ttl)
				if err := router.Routes.Apply(ctx, resolvedAction, ipv6, ttl); err != nil {
					if errors.Is(err, ErrDirectIPv6Unavailable) {
						stripIPv6Records(response)
						router.updateCachedMessage(request, action, response)
						debuglog.Event("DNS.routes.skipIPv6", "domain=%s action=%s addresses=%v reason=%v", domain, resolvedAction, ipv6, err)
						continue
					}
					return nil, netip.Addr{}, fmt.Errorf("не удалось применить маршрут для %s: %w", domain, err)
				}
			}
		}
	}
	return response, netip.Addr{}, nil
}

func (router *DNSRouter) updateCachedMessage(request *dns.Msg, action Action, response *dns.Msg) {
	if request == nil || response == nil {
		return
	}
	raw, err := request.Pack()
	if err != nil || len(raw) < 12 {
		return
	}
	wire, err := response.Pack()
	if err != nil {
		return
	}
	key := string(action) + string(raw[2:])
	router.stateMu.Lock()
	if item, ok := router.cache[key]; ok {
		item.wire = append([]byte(nil), wire...)
		router.cache[key] = item
	}
	router.stateMu.Unlock()
}

func stripIPv6Records(message *dns.Msg) {
	message.Answer = recordsWithoutAAAA(message.Answer)
	message.Ns = recordsWithoutAAAA(message.Ns)
	message.Extra = recordsWithoutAAAA(message.Extra)
}

func recordsWithoutAAAA(records []dns.RR) []dns.RR {
	filtered := records[:0]
	for _, record := range records {
		if record.Header().Rrtype != dns.TypeAAAA {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func (router *DNSRouter) ensureState() {
	router.stateMu.Lock()
	defer router.stateMu.Unlock()
	if router.clients == nil {
		router.clients = make(map[string]*http.Client)
		router.cache = make(map[string]dnsCacheEntry)
		router.pending = make(map[string]*dnsPending)
		router.down = make(map[string]time.Time)
	}
}

func (router *DNSRouter) exchangeCached(ctx context.Context, profile *Profile, overrides Overrides, message *dns.Msg, action Action) (*dns.Msg, error) {
	router.ensureState()
	raw, err := message.Pack()
	if err != nil {
		return nil, err
	}
	if len(raw) < 12 {
		return nil, errors.New("DNS query too short")
	}
	key := string(action) + string(raw[2:])
	now := time.Now()
	router.stateMu.Lock()
	if item, ok := router.cache[key]; ok {
		if now.Before(item.expires) {
			router.stateMu.Unlock()
			result := new(dns.Msg)
			if err := result.Unpack(cachedDNSReply(item, message.Id, now)); err != nil {
				return nil, err
			}
			debuglog.Event("DNS.cache", "action=%s question=%v hit=true", action, message.Question)
			return result, nil
		}
		if now.Before(item.staleUntil) {
			if router.pending[key] == nil {
				queryCopy := message.Copy()
				go router.revalidateCached(queryCopy)
			}
			router.stateMu.Unlock()
			result := new(dns.Msg)
			if err := result.Unpack(cachedDNSReply(item, message.Id, now)); err != nil {
				return nil, err
			}
			debuglog.Event("DNS.cache", "action=%s question=%v hit=stale-revalidate", action, message.Question)
			return result, nil
		}
	}
	if pending := router.pending[key]; pending != nil {
		router.stateMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending.done:
		}
		if pending.err != nil {
			return nil, pending.err
		}
		wire := append([]byte(nil), pending.wire...)
		binary.BigEndian.PutUint16(wire, message.Id)
		result := new(dns.Msg)
		return result, result.Unpack(wire)
	}
	pending := &dnsPending{done: make(chan struct{})}
	router.pending[key] = pending
	router.stateMu.Unlock()

	result, err := router.exchangeFallbacks(ctx, profile, overrides, message, action)
	var wire []byte
	if err == nil {
		wire, err = result.Pack()
	}
	router.stateMu.Lock()
	pending.wire, pending.err = wire, err
	if err == nil {
		if ttl := cacheTTL(result); ttl > 0 {
			if len(router.cache) >= 512 {
				for existingKey, item := range router.cache {
					if now.After(item.expires) {
						delete(router.cache, existingKey)
					}
				}
				if len(router.cache) >= 512 {
					for existingKey := range router.cache {
						delete(router.cache, existingKey)
						break
					}
				}
			}
			createdAt := time.Now()
			router.cache[key] = dnsCacheEntry{
				wire:       append([]byte(nil), wire...),
				created:    createdAt,
				expires:    createdAt.Add(ttl),
				staleUntil: createdAt.Add(ttl + 5*time.Minute),
			}
		}
	}
	delete(router.pending, key)
	close(pending.done)
	router.stateMu.Unlock()
	return result, err
}

func (router *DNSRouter) revalidateCached(message *dns.Msg) {
	if message == nil || len(message.Question) == 0 {
		return
	}
	profile, matcher, overrides := router.snapshotWithOverrides()
	if profile == nil || matcher == nil {
		return
	}
	domain := normalizeDomain(message.Question[0].Name)
	action := matcher.Domain(domain)
	if action == ActionBlock {
		return
	}
	raw, err := message.Pack()
	if err != nil || len(raw) < 12 {
		return
	}
	key := string(action) + string(raw[2:])
	router.stateMu.Lock()
	if router.pending[key] != nil {
		router.stateMu.Unlock()
		return
	}
	pending := &dnsPending{done: make(chan struct{})}
	router.pending[key] = pending
	router.stateMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := router.exchangeFallbacks(ctx, profile, overrides, message, action)
	var wire []byte
	if err == nil {
		wire, err = result.Pack()
	}
	router.stateMu.Lock()
	pending.wire, pending.err = wire, err
	if err == nil {
		if ttl := cacheTTL(result); ttl > 0 {
			createdAt := time.Now()
			router.cache[key] = dnsCacheEntry{
				wire:       append([]byte(nil), wire...),
				created:    createdAt,
				expires:    createdAt.Add(ttl),
				staleUntil: createdAt.Add(ttl + 5*time.Minute),
			}
		}
	}
	delete(router.pending, key)
	close(pending.done)
	router.stateMu.Unlock()

	if err == nil && router.Routes != nil {
		addresses, ttl := answerAddresses(result)
		grouped := make(map[Action][]netip.Addr)
		for _, address := range addresses {
			resolvedAction := matcher.Route(domain, address)
			if resolvedAction != ActionBlock {
				grouped[resolvedAction] = append(grouped[resolvedAction], address)
			}
		}
		for resolvedAction, routedAddresses := range grouped {
			_ = router.Routes.Apply(ctx, resolvedAction, routedAddresses, ttl)
		}
	}
}

func (router *DNSRouter) exchangeFallbacks(ctx context.Context, profile *Profile, overrides Overrides, message *dns.Msg, action Action) (*dns.Msg, error) {
	if action == ActionBlock {
		return nil, errors.New("DNS route blocked")
	}
	endpoints, err := dnsEndpoints(profile, overrides, action)
	if err != nil {
		return nil, err
	}
	var failures []error
	for index, endpoint := range endpoints {
		key := string(action) + " " + endpoint.URL
		router.stateMu.Lock()
		down := time.Now().Before(router.down[key])
		router.stateMu.Unlock()
		if down && index < len(endpoints)-1 {
			debuglog.Event("DNS.cooldown", "endpoint=%s", endpoint.URL)
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		started := time.Now()
		response, protocol, err := router.exchangeAt(attempt, message, action, endpoint)
		cancel()
		if err == nil && (response.Rcode == dns.RcodeServerFailure || response.Rcode == dns.RcodeRefused) {
			err = fmt.Errorf("DNS rcode=%d (%s)", response.Rcode, dns.RcodeToString[response.Rcode])
		}
		if err == nil {
			router.stateMu.Lock()
			delete(router.down, key)
			if action == ActionProxy {
				router.proxyFailures = 0
				router.lastProxySuccess = time.Now()
			}
			router.stateMu.Unlock()
			debuglog.Event("DNS.answer", "question=%v route=%s resolver=%s fallback=%t protocol=%s elapsed=%s", message.Question, action, endpoint.URL, index > 0, protocol, time.Since(started))
			return response, nil
		}
		wrapped := fmt.Errorf("question=%v route=%s resolver=%s bootstrap=%s attempt=%d: %w", message.Question, action, endpoint.URL, endpoint.Bootstrap, index+1, err)
		debuglog.Error("DNS.upstream", wrapped)
		failures = append(failures, wrapped)
		router.stateMu.Lock()
		router.down[key] = time.Now().Add(30 * time.Second)
		router.stateMu.Unlock()
		if ctx.Err() != nil {
			break
		}
	}
	if action == ActionProxy {
		router.stateMu.Lock()
		router.proxyFailures++
		router.lastProxyFailure = time.Now()
		router.stateMu.Unlock()
	}
	return nil, errors.Join(failures...)
}

func dnsEndpoints(profile *Profile, overrides Overrides, action Action) ([]dnsEndpoint, error) {
	dnsType, dnsDomain, dnsIP, legacy := profile.RemoteDNSType, profile.RemoteDNSDomain, profile.RemoteDNSIP, profile.RemoteDNS
	if action == ActionDirect {
		dnsType, dnsDomain, dnsIP, legacy = profile.DomesticDNSType, profile.DomesticDNSDomain, profile.DomesticDNSIP, profile.DomesticDNS
	}
	kind := strings.ToLower(strings.TrimSpace(dnsType))
	server := strings.TrimSpace(dnsDomain)
	if server == "" {
		server = strings.TrimSpace(legacy)
	}
	if server == "" {
		server = strings.TrimSpace(dnsIP)
	}
	if !strings.Contains(server, "://") {
		switch kind {
		case "doh":
			server = "https://" + server + "/dns-query"
		case "dot":
			server = "tls://" + server
		case "tcp":
			server = "tcp://" + server
		default:
			server = "udp://" + server
		}
	}
	bootstrap := strings.TrimSpace(dnsIP)
	if parsed, err := url.Parse(server); err == nil {
		if _, err := netip.ParseAddr(parsed.Hostname()); err == nil {
			bootstrap = ""
		}
	}
	primary := strings.TrimSpace(server + " " + bootstrap)
	key := "dns-" + string(action)
	if local := overrides.DNS[key]; len(local) > 0 {
		primary = local[len(local)-1]
	}
	values := append([]string{primary}, overrides.DNS[key+"-fallback"]...)
	if len(values) > 4 {
		return nil, errors.New("DNS: максимум три резервных сервера на маршрут")
	}
	var result []dnsEndpoint
	for _, value := range values {
		endpoint, err := parseDNSEndpoint(value)
		if err != nil {
			return nil, err
		}
		result = append(result, endpoint)
	}
	return result, nil
}

func parseDNSEndpoint(value string) (dnsEndpoint, error) {
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > 2 {
		return dnsEndpoint{}, errors.New("DNS: ожидается URL и необязательный bootstrap IP")
	}
	endpoint := dnsEndpoint{URL: parts[0]}
	if len(parts) == 2 {
		endpoint.Bootstrap = parts[1]
	}
	if !strings.Contains(endpoint.URL, "://") {
		endpoint.URL = "udp://" + endpoint.URL
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return dnsEndpoint{}, errors.New("DNS: неверный адрес сервера")
	}
	switch parsed.Scheme {
	case "https", "tls", "udp", "tcp":
	default:
		return dnsEndpoint{}, fmt.Errorf("DNS: протокол %q не поддерживается", parsed.Scheme)
	}
	if parsed.Scheme == "https" && parsed.Path == "" {
		parsed.Path = "/dns-query"
		endpoint.URL = parsed.String()
	}
	if endpoint.Bootstrap == "" {
		if ip, err := netip.ParseAddr(parsed.Hostname()); err == nil {
			endpoint.Bootstrap = ip.String()
		} else if known := defaultBootstrapIP(parsed.Hostname()); known != "" {
			endpoint.Bootstrap = known
		} else {
			lookupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			addrs, lookupErr := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", parsed.Hostname())
			cancel()
			if lookupErr == nil {
				for _, addr := range addrs {
					addr = addr.Unmap()
					if addr.Is4() {
						endpoint.Bootstrap = addr.String()
						break
					}
					if endpoint.Bootstrap == "" && addr.IsValid() {
						endpoint.Bootstrap = addr.String()
					}
				}
			}
		}
	}
	if _, err := netip.ParseAddr(endpoint.Bootstrap); err != nil {
		return dnsEndpoint{}, fmt.Errorf("DNS %s: нужен bootstrap IP", parsed.Hostname())
	}
	return endpoint, nil
}

func defaultBootstrapIP(host string) string {
	switch strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), ".")) {
	case "dns.google", "dns.google.com":
		return "8.8.8.8"
	case "cloudflare-dns.com", "one.one.one.one", "1dot1dot1dot1.cloudflare-dns.com":
		return "1.1.1.1"
	case "dns.quad9.net", "dns9.quad9.net":
		return "9.9.9.9"
	case "common.dot.dns.yandex.net", "dns.yandex.ru", "dns.yandex.com":
		return "77.88.8.8"
	case "dns.adguard-dns.com", "dns.adguard.com":
		return "94.140.14.14"
	case "doh.opendns.com":
		return "208.67.222.222"
	default:
		return ""
	}
}

// DNSBootstrapAddresses returns every physical destination that must receive
// an explicit route before the local DNS proxy starts, including user-defined
// fallbacks. This prevents a Proxy resolver from escaping through Direct.
func DNSBootstrapAddresses(profile *Profile, overrides Overrides) (map[Action][]netip.Addr, error) {
	result := make(map[Action][]netip.Addr)
	for _, action := range []Action{ActionDirect, ActionProxy} {
		endpoints, err := dnsEndpoints(profile, overrides, action)
		if err != nil {
			return nil, err
		}
		seen := make(map[netip.Addr]bool)
		for _, endpoint := range endpoints {
			address, err := netip.ParseAddr(endpoint.Bootstrap)
			if err != nil {
				return nil, err
			}
			address = address.Unmap()
			if !seen[address] {
				seen[address] = true
				result[action] = append(result[action], address)
			}
		}
	}
	return result, nil
}

func (router *DNSRouter) exchangeAt(ctx context.Context, message *dns.Msg, action Action, endpoint dnsEndpoint) (*dns.Msg, string, error) {
	parsed, _ := url.Parse(endpoint.URL)
	port := parsed.Port()
	if port == "" {
		switch parsed.Scheme {
		case "https":
			port = "443"
		case "tls":
			port = "853"
		default:
			port = "53"
		}
	}
	address := net.JoinHostPort(endpoint.Bootstrap, port)
	if parsed.Scheme == "https" {
		return router.exchangeDoH(ctx, message, action, endpoint, address)
	}
	network := "udp"
	if parsed.Scheme == "tcp" || parsed.Scheme == "tls" {
		network = "tcp"
	}
	client := &dns.Client{Net: network, Timeout: 5 * time.Second}
	if parsed.Scheme == "tls" {
		client.Net = "tcp-tls"
		client.TLSConfig = &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12}
	}
	response, rtt, err := client.ExchangeContext(ctx, message.Copy(), address)
	if err != nil {
		return nil, network, err
	}
	protocol := parsed.Scheme + " rtt=" + rtt.String()
	if response.Truncated && parsed.Scheme == "udp" {
		copyEndpoint := endpoint
		copyEndpoint.URL = "tcp://" + parsed.Host
		return router.exchangeAt(ctx, message, action, copyEndpoint)
	}
	return response, protocol, nil
}

func (router *DNSRouter) exchangeDoH(ctx context.Context, message *dns.Msg, action Action, endpoint dnsEndpoint, address string) (*dns.Msg, string, error) {
	parsed, _ := url.Parse(endpoint.URL)
	payload, err := message.Pack()
	if err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", "application/dns-message")
	request.Header.Set("Accept", "application/dns-message")
	key := string(action) + " " + endpoint.URL + " " + address
	router.stateMu.Lock()
	client := router.clients[key]
	if client == nil {
		transport := &http.Transport{
			TLSClientConfig:       &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          8,
			MaxIdleConnsPerHost:   4,
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
			},
		}
		client = &http.Client{Transport: transport, Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("DNS redirect refused") }}
		router.clients[key] = client
	}
	router.stateMu.Unlock()
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	debuglog.Event("DNS.transport", "resolver=%s route=%s protocol=%s status=%s", parsed.Redacted(), action, response.Proto, response.Status)
	if response.StatusCode != http.StatusOK {
		return nil, response.Proto, fmt.Errorf("DoH HTTP status=%s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil {
		return nil, response.Proto, err
	}
	result := new(dns.Msg)
	if err := result.Unpack(data); err != nil {
		return nil, response.Proto, fmt.Errorf("неверный ответ DoH: %w", err)
	}
	return result, response.Proto, nil
}

func cacheTTL(message *dns.Msg) time.Duration {
	if message == nil || message.Truncated || (message.Rcode != dns.RcodeSuccess && message.Rcode != dns.RcodeNameError) {
		return 0
	}
	ttl := uint32(300)
	found := false
	for _, record := range append(append([]dns.RR{}, message.Answer...), message.Ns...) {
		found = true
		if record.Header().Ttl < ttl {
			ttl = record.Header().Ttl
		}
		if soa, ok := record.(*dns.SOA); ok && soa.Minttl < ttl {
			ttl = soa.Minttl
		}
	}
	if !found {
		return 0
	}
	return time.Duration(ttl) * time.Second
}

func cachedDNSReply(item dnsCacheEntry, id uint16, now time.Time) []byte {
	result := append([]byte(nil), item.wire...)
	var message dns.Msg
	if message.Unpack(result) != nil {
		return result
	}
	message.Id = id
	age := uint32(now.Sub(item.created) / time.Second)
	for _, records := range [][]dns.RR{message.Answer, message.Ns, message.Extra} {
		for _, record := range records {
			if record.Header().Rrtype == dns.TypeOPT {
				continue
			}
			if record.Header().Ttl > age {
				record.Header().Ttl -= age
			} else {
				record.Header().Ttl = 15
			}
		}
	}
	result, _ = message.Pack()
	return result
}

func answerAddresses(message *dns.Msg) ([]netip.Addr, time.Duration) {
	addresses := make([]netip.Addr, 0)
	minimumTTL := uint32(3600)
	for _, answer := range message.Answer {
		var address netip.Addr
		switch record := answer.(type) {
		case *dns.A:
			if converted, ok := netip.AddrFromSlice(record.A); ok {
				address = converted.Unmap()
			}
		case *dns.AAAA:
			if converted, ok := netip.AddrFromSlice(record.AAAA); ok {
				address = converted
			}
		}
		if address.IsValid() {
			addresses = append(addresses, address)
			if answer.Header().Ttl < minimumTTL {
				minimumTTL = answer.Header().Ttl
			}
		}
	}
	if minimumTTL < 30 {
		minimumTTL = 30
	}
	return addresses, time.Duration(minimumTTL) * time.Second
}

func fixedDNSResponse(request *dns.Msg, domain, addressText string) (*dns.Msg, error) {
	address, err := netip.ParseAddr(addressText)
	if err != nil {
		return nil, err
	}
	response := new(dns.Msg)
	response.SetReply(request)
	name := dns.Fqdn(domain)
	if address.Is4() && request.Question[0].Qtype == dns.TypeA {
		response.Answer = append(response.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.IP(address.AsSlice()),
		})
	}
	if address.Is6() && request.Question[0].Qtype == dns.TypeAAAA {
		response.Answer = append(response.Answer, &dns.AAAA{
			Hdr:  dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 300},
			AAAA: net.IP(address.AsSlice()),
		})
	}
	return response, nil
}
