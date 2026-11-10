//go:build windows

package windowsroutes

import (
	"errors"
	"net/netip"
	"testing"

	"golang.org/x/sys/windows"
	"nexusclient/internal/routing"
)

func TestIsManagedHostRoute(t *testing.T) {
	managedV4 := windows.MibIpForwardRow2{
		Protocol: windows.MIB_IPPROTO_NETMGMT,
		Metric:   3,
		DestinationPrefix: windows.IpAddressPrefix{
			Prefix:       rawAddress(netip.MustParseAddr("140.82.121.4")),
			PrefixLength: 32,
		},
	}
	if !isManagedHostRoute(managedV4) {
		t.Fatal("expected managed IPv4 /32 route to match")
	}

	splitDefault := managedV4
	splitDefault.DestinationPrefix.PrefixLength = 1
	if isManagedHostRoute(splitDefault) {
		t.Fatal("non-host route must not match isManagedHostRoute")
	}

	loopback := managedV4
	loopback.DestinationPrefix.Prefix = rawAddress(netip.MustParseAddr("127.0.0.1"))
	if isManagedHostRoute(loopback) {
		t.Fatal("loopback route must not match isManagedHostRoute")
	}

	otherMetric := managedV4
	otherMetric.Metric = 25
	if isManagedHostRoute(otherMetric) {
		t.Fatal("route with non-Ex WG metric must not match")
	}
}

func TestRawAddressRoundTrip(t *testing.T) {
	tests := []netip.Addr{
		netip.MustParseAddr("10.66.66.81"),
		netip.MustParseAddr("0.0.0.0"),
		netip.MustParseAddr("2001:db8::42"),
		netip.MustParseAddr("fe80::1%12"),
	}
	for _, address := range tests {
		if actual := addressFromRaw(rawAddress(address)); actual != address {
			t.Fatalf("round trip for %s returned %s", address, actual)
		}
	}
}

func TestDirectIPv6WithoutPhysicalGatewayIsReportedAsUnavailable(t *testing.T) {
	_, _, err := routeTarget(gatewayInfo{TunnelIndex: 7}, routing.ActionDirect, netip.MustParseAddr("2001:db8::42"))
	if !errors.Is(err, routing.ErrDirectIPv6Unavailable) {
		t.Fatalf("routeTarget error = %v, want ErrDirectIPv6Unavailable", err)
	}
}

func TestProxyIPv6WithoutTunnelIPv6IsReportedAsUnavailable(t *testing.T) {
	_, _, err := routeTarget(gatewayInfo{TunnelIndex: 7, TunnelIPv6Disabled: true}, routing.ActionProxy, netip.MustParseAddr("2001:db8::42"))
	if !errors.Is(err, routing.ErrDirectIPv6Unavailable) {
		t.Fatalf("routeTarget error = %v, want ErrDirectIPv6Unavailable", err)
	}
}

func TestBestRouteIndexLoopback(t *testing.T) {
	index, err := BestRouteIndex(netip.MustParseAddr("127.0.0.1"))
	if err != nil {
		t.Skipf("Windows compatibility layer does not expose GetBestRoute2: %v", err)
	}
	if index == 0 {
		t.Fatal("GetBestRoute2 returned an empty interface index")
	}
}

func TestInterfaceByAliasMarksMissingInterface(t *testing.T) {
	_, err := InterfaceByAlias("Ex WG test interface that must not exist")
	if !errors.Is(err, ErrInterfaceNotFound) {
		t.Fatalf("InterfaceByAlias error = %v, want ErrInterfaceNotFound", err)
	}
}
