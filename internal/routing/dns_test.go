package routing

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type recordedRoutes struct {
	action    Action
	addresses []netip.Addr
	ttl       time.Duration
	err       error
}

func (routes *recordedRoutes) Apply(_ context.Context, action Action, addresses []netip.Addr, ttl time.Duration) error {
	routes.action = action
	routes.addresses = append([]netip.Addr(nil), addresses...)
	routes.ttl = ttl
	return routes.err
}

func TestDNSRouterSuppressesDirectIPv6WithoutPhysicalGateway(t *testing.T) {
	server := startTestDNSServer(t)
	matcher, err := CompileMatcher(&Profile{Name: "test", GlobalProxy: false}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := &recordedRoutes{err: ErrDirectIPv6Unavailable}
	router := &DNSRouter{
		Profile: &Profile{Name: "test", RemoteDNSIP: server, DomesticDNSIP: server},
		Matcher: matcher,
		Routes:  routes,
	}
	request := new(dns.Msg)
	request.SetQuestion("direct.example.", dns.TypeAAAA)
	response, err := router.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %d, want NOERROR", response.Rcode)
	}
	if len(response.Answer) != 0 {
		t.Fatalf("answers = %v, want an empty AAAA answer", response.Answer)
	}
	if routes.action != ActionDirect || len(routes.addresses) != 1 || !routes.addresses[0].Is6() {
		t.Fatalf("unexpected route attempt: %#v", routes)
	}
}

func TestDNSRouterBlocksWithoutCallingUpstream(t *testing.T) {
	matcher, err := CompileMatcher(&Profile{
		Name:       "test",
		BlockSites: []string{"domain:ads.example"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := &DNSRouter{Profile: &Profile{Name: "test"}, Matcher: matcher}
	request := new(dns.Msg)
	request.SetQuestion("cdn.ads.example.", dns.TypeA)
	response, err := router.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Rcode != dns.RcodeNameError {
		t.Fatalf("rcode = %d, want NXDOMAIN", response.Rcode)
	}
}

func TestDNSRouterCanAtomicallyReplaceRules(t *testing.T) {
	firstProfile := &Profile{Name: "first"}
	firstMatcher, err := CompileMatcher(&Profile{Name: "first", GlobalProxy: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := &DNSRouter{Profile: firstProfile, Matcher: firstMatcher}

	secondProfile := &Profile{Name: "second"}
	secondMatcher, err := CompileMatcher(&Profile{Name: "second", BlockSites: []string{"domain:example.com"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	router.Update(secondProfile, secondMatcher)

	profile, matcher := router.snapshot()
	if profile.Name != "second" || matcher.Domain("example.com") != ActionBlock {
		t.Fatal("routing profile and matcher were not replaced together")
	}
}

func TestDNSRouterAppliesRouteBeforeReturningAnswer(t *testing.T) {
	server := startTestDNSServer(t)
	matcher, err := CompileMatcher(&Profile{
		Name:        "test",
		GlobalProxy: false,
		ProxySites:  []string{"domain:youtube.com"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := new(recordedRoutes)
	router := &DNSRouter{
		Profile: &Profile{Name: "test", RemoteDNSIP: server, DomesticDNSIP: server},
		Matcher: matcher,
		Routes:  routes,
	}
	request := new(dns.Msg)
	request.SetQuestion("www.youtube.com.", dns.TypeA)
	response, err := router.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(response.Answer))
	}
	if routes.action != ActionProxy || len(routes.addresses) != 1 || routes.addresses[0].String() != "203.0.113.10" {
		t.Fatalf("unexpected route: %#v", routes)
	}
	if routes.ttl != 120*time.Second {
		t.Fatalf("ttl = %s, want 2m", routes.ttl)
	}
	if health := router.ProxyHealth(); health.ConsecutiveFailures != 0 || health.LastSuccess.IsZero() {
		t.Fatalf("unexpected proxy health after success: %+v", health)
	}
}

func TestDNSRouterTracksConsecutiveProxyFailures(t *testing.T) {
	matcher, err := CompileMatcher(&Profile{Name: "test", GlobalProxy: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := &DNSRouter{
		Profile: &Profile{Name: "test", RemoteDNSIP: "127.0.0.1:1"},
		Matcher: matcher,
	}
	request := new(dns.Msg)
	request.SetQuestion("unavailable.example.", dns.TypeA)
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, resolveErr := router.Resolve(ctx, request)
		cancel()
		if resolveErr == nil {
			t.Fatal("expected the unavailable proxy resolver to fail")
		}
	}
	health := router.ProxyHealth()
	if health.ConsecutiveFailures != 3 || health.LastFailure.IsZero() {
		t.Fatalf("unexpected proxy health after failures: %+v", health)
	}
}

func TestDNSRouterExplainsEffectiveDomainRoute(t *testing.T) {
	server := startTestDNSServer(t)
	matcher, err := CompileMatcher(&Profile{
		Name:       "test",
		ProxySites: []string{"domain:downloads.example"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := new(recordedRoutes)
	router := &DNSRouter{
		Profile: &Profile{Name: "test", RemoteDNSIP: server, DomesticDNSIP: server},
		Matcher: matcher,
		Routes:  routes,
	}
	explanation, err := router.ExplainDomain(context.Background(), "Downloads.Example.")
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Domain != "downloads.example" || explanation.Action != ActionProxy {
		t.Fatalf("unexpected explanation: %+v", explanation)
	}
	if len(explanation.Addresses) != 2 || routes.action != ActionProxy {
		t.Fatalf("addresses=%v route=%+v", explanation.Addresses, routes)
	}
}

func TestDNSRouterExplainsIPBlockedDomain(t *testing.T) {
	server := startTestDNSServer(t)
	matcher, err := CompileMatcher(&Profile{
		Name:    "test",
		BlockIP: []string{"203.0.113.0/24"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := new(recordedRoutes)
	router := &DNSRouter{
		Profile: &Profile{Name: "test", RemoteDNSIP: server, DomesticDNSIP: server},
		Matcher: matcher,
		Routes:  routes,
	}
	explanation, err := router.ExplainDomain(context.Background(), "ip-blocked.example.")
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Action != ActionBlock {
		t.Fatalf("expected ActionBlock for IP-blocked domain, got %+v", explanation)
	}
	if len(explanation.Addresses) == 0 || explanation.Addresses[0].String() != "203.0.113.10" {
		t.Fatalf("expected blocked IP in explanation addresses, got %+v", explanation.Addresses)
	}
}

func TestDNSRouterStaticHostStillReceivesDirectRoute(t *testing.T) {
	profile := &Profile{
		Name:        "test",
		GlobalProxy: true,
		DirectSites: []string{"domain:ru"},
		DNSHosts:    map[string]string{"lknpd.nalog.ru": "213.24.64.181"},
	}
	matcher, err := CompileMatcher(profile, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := new(recordedRoutes)
	router := &DNSRouter{Profile: profile, Matcher: matcher, Routes: routes}
	request := new(dns.Msg)
	request.SetQuestion("lknpd.nalog.ru.", dns.TypeA)
	response, err := router.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Answer) != 1 {
		t.Fatalf("answers = %v, want the static DnsHosts address", response.Answer)
	}
	if routes.action != ActionDirect || len(routes.addresses) != 1 || routes.addresses[0].String() != "213.24.64.181" {
		t.Fatalf("static host must get a Direct route, got %#v", routes)
	}
}

type noIPv6Routes struct {
	recordedRoutes
}

func (*noIPv6Routes) SupportsIPv6(Action) bool {
	return false
}

func TestDNSRouterShortCircuitsAAAAWhenIPv6Unavailable(t *testing.T) {
	matcher, err := CompileMatcher(&Profile{Name: "test", GlobalProxy: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := &DNSRouter{
		Profile: &Profile{Name: "test", RemoteDNSIP: "127.0.0.1:1", DomesticDNSIP: "127.0.0.1:1"},
		Matcher: matcher,
		Routes:  new(noIPv6Routes),
	}
	request := new(dns.Msg)
	request.SetQuestion("proxy.example.", dns.TypeAAAA)
	response, err := router.Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("expected local NODATA without upstream lookup, got error: %v", err)
	}
	if response.Rcode != dns.RcodeSuccess || len(response.Answer) != 0 {
		t.Fatalf("unexpected AAAA response: %+v", response)
	}
}

func TestDNSRouterSuppressesProxyIPv6WithoutTunnelIPv6(t *testing.T) {
	server := startTestDNSServer(t)
	matcher, err := CompileMatcher(&Profile{Name: "test", GlobalProxy: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := &recordedRoutes{err: ErrDirectIPv6Unavailable}
	router := &DNSRouter{
		Profile: &Profile{Name: "test", RemoteDNSIP: server, DomesticDNSIP: server},
		Matcher: matcher,
		Routes:  routes,
	}
	request := new(dns.Msg)
	request.SetQuestion("proxy.example.", dns.TypeAAAA)
	response, err := router.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Rcode != dns.RcodeSuccess || len(response.Answer) != 0 {
		t.Fatalf("expected empty AAAA answer for IPv4-only tunnel, got %v", response.Answer)
	}
}

func TestDNSRouterResetTransportsClearsFailures(t *testing.T) {
	router := &DNSRouter{proxyFailures: 4}
	router.ensureState()
	router.down["proxy https://dns.google/dns-query"] = time.Now().Add(time.Minute)
	router.ResetTransports()
	if health := router.ProxyHealth(); health.ConsecutiveFailures != 0 {
		t.Fatalf("expected consecutive failures reset, got %+v", health)
	}
}

func startTestDNSServer(t *testing.T) string {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{
		PacketConn: packet,
		Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, request *dns.Msg) {
			response := new(dns.Msg)
			response.SetReply(request)
			if request.Question[0].Qtype == dns.TypeAAAA {
				response.Answer = append(response.Answer, &dns.AAAA{
					Hdr:  dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 120},
					AAAA: net.ParseIP("2001:db8::10"),
				})
			} else {
				response.Answer = append(response.Answer, &dns.A{
					Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120},
					A:   net.ParseIP("203.0.113.10").To4(),
				})
			}
			_ = writer.WriteMsg(response)
		}),
	}
	go func() {
		_ = server.ActivateAndServe()
	}()
	t.Cleanup(func() {
		_ = server.Shutdown()
	})
	return packet.LocalAddr().String()
}
