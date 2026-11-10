//go:build windows

package windowsroutes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/amnezia-vpn/amneziawg-windows/v3/tunnel/winipcfg"
	"golang.org/x/sys/windows"

	"nexusclient/internal/debuglog"
	"nexusclient/internal/routing"
)

type gatewayInfo struct {
	TunnelIndex        int
	TunnelIPv6Disabled bool
	IPv4Index          int
	IPv4Gateway        netip.Addr
	IPv6Index          int
	IPv6Gateway        netip.Addr
}

type lease struct {
	action         routing.Action
	interfaceIndex int
	expires        time.Time
}

type Manager struct {
	gateways gatewayInfo
	alias    string

	mu              sync.Mutex
	leases          map[netip.Addr]lease
	stop            chan struct{}
	done            chan struct{}
	changes         chan struct{}
	callback        *winipcfg.InterfaceChangeCallback
	onNetworkChange func()
}

func New(ctx context.Context, tunnelAlias string) (*Manager, error) {
	gateways, err := discoverGateways(ctx, tunnelAlias)
	if err != nil {
		return nil, err
	}
	manager := &Manager{
		gateways: gateways,
		alias:    tunnelAlias,
		leases:   make(map[netip.Addr]lease),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		changes:  make(chan struct{}, 1),
	}
	debuglog.Event("routes.gateways", "%+v", gateways)
	callback, err := winipcfg.RegisterInterfaceChangeCallback(func(_ winipcfg.MibNotificationType, iface *winipcfg.MibIPInterfaceRow) {
		if iface != nil && iface.InterfaceIndex == uint32(gateways.TunnelIndex) {
			return
		}
		select {
		case manager.changes <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return nil, fmt.Errorf("NotifyIpInterfaceChange: %w", err)
	}
	manager.callback = callback
	go manager.expireLoop()
	return manager, nil
}

func (manager *Manager) SetNetworkChangeHandler(handler func()) {
	manager.mu.Lock()
	manager.onNetworkChange = handler
	manager.mu.Unlock()
}

// leaseGraceAfterRuleChange bounds how long a host route created under the
// previous rules can survive a hot reload. Apply() replaces it immediately
// when a fresh DNS answer maps the address to a different action.
const leaseGraceAfterRuleChange = 45 * time.Second

// PruneLeases is called after routing rules are hot-reloaded. Bootstrap DNS
// routes that keep their action are preserved; every other lease gets a short
// expiry instead of being deleted at once, so established connections are not
// cut mid-stream while the Windows DNS cache (already flushed) re-resolves and
// re-applies routes under the new rules.
func (manager *Manager) PruneLeases(_ context.Context, keep map[routing.Action][]netip.Addr) error {
	preserved := make(map[netip.Addr]routing.Action)
	for action, addresses := range keep {
		for _, addr := range addresses {
			unmapped := addr.Unmap()
			if unmapped.IsValid() {
				preserved[unmapped] = action
			}
		}
	}
	deadline := time.Now().Add(leaseGraceAfterRuleChange)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for address, current := range manager.leases {
		if keepAction, ok := preserved[address]; ok && keepAction == current.action {
			continue
		}
		if current.expires.After(deadline) {
			current.expires = deadline
			manager.leases[address] = current
		}
	}
	return nil
}

func (manager *Manager) Apply(ctx context.Context, action routing.Action, addresses []netip.Addr, ttl time.Duration) error {
	if action == routing.ActionBlock {
		return errors.New("block-маршруты должны применяться через DNS/WFP, а не таблицу маршрутов")
	}
	if action != routing.ActionDirect && action != routing.ActionProxy {
		return fmt.Errorf("неизвестное действие %q", action)
	}
	if ttl < 30*time.Second {
		ttl = 30 * time.Second
	}
	if ttl > time.Hour {
		ttl = time.Hour
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, source := range addresses {
		address := source.Unmap()
		if !address.IsValid() {
			continue
		}
		interfaceIndex, nextHop, err := manager.target(action, address)
		if err != nil {
			return err
		}
		if current, ok := manager.leases[address]; ok {
			if current.action == action && current.interfaceIndex == interfaceIndex {
				current.expires = time.Now().Add(ttl)
				manager.leases[address] = current
				continue
			}
			// Install the replacement first. A failed Direct/Proxy transition
			// must keep the last explicit route rather than expose the system
			// default between remove and add.
			if err := addRoute(ctx, address, interfaceIndex, nextHop); err != nil {
				return err
			}
			if err := removeManagedRoutesExcept(ctx, address, interfaceIndex); err != nil {
				debuglog.Error("routes.replace.removeOld", err)
			}
		} else if err := addRoute(ctx, address, interfaceIndex, nextHop); err != nil {
			return err
		}
		manager.leases[address] = lease{
			action:         action,
			interfaceIndex: interfaceIndex,
			expires:        time.Now().Add(ttl),
		}
	}
	return nil
}

func (manager *Manager) Close() error {
	select {
	case <-manager.stop:
		return nil
	default:
		close(manager.stop)
	}
	if manager.callback != nil {
		debuglog.Error("NotifyIpInterfaceChange.Unregister", manager.callback.Unregister())
		manager.callback = nil
	}
	<-manager.done

	manager.mu.Lock()
	defer manager.mu.Unlock()
	clear(manager.leases)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := CleanupManagedRoutes(ctx)
	return err
}

func (manager *Manager) TunnelIndex() int {
	return manager.gateways.TunnelIndex
}

func (manager *Manager) UseLocalDNS(ctx context.Context) error {
	if luid, err := winipcfg.LUIDFromIndex(uint32(manager.gateways.TunnelIndex)); err == nil {
		_ = luid.FlushDNS(windows.AF_INET6)
	}
	err := netsh(ctx, "interface", "ipv4", "set", "dnsservers",
		"name="+strconv.Itoa(manager.gateways.TunnelIndex),
		"source=static", "address=127.0.0.1", "register=primary", "validate=no")
	if err != nil {
		return fmt.Errorf("не удалось включить внутренний DNS: %w", err)
	}
	return nil
}

func (manager *Manager) RestoreDNS(ctx context.Context) error {
	if _, err := InterfaceByAlias(manager.alias); err != nil {
		if errors.Is(err, ErrInterfaceNotFound) {
			debuglog.Event("DNS.restore", "interface %s already removed; no DNS state remains", manager.alias)
			return nil
		}
		return fmt.Errorf("не удалось проверить AWG-интерфейс перед восстановлением DNS: %w", err)
	}
	if luid, err := winipcfg.LUIDFromIndex(uint32(manager.gateways.TunnelIndex)); err == nil {
		_ = luid.FlushDNS(windows.AF_INET6)
	}
	err := netsh(ctx, "interface", "ipv4", "set", "dnsservers",
		"name="+strconv.Itoa(manager.gateways.TunnelIndex), "source=dhcp")
	if err != nil {
		return fmt.Errorf("не удалось восстановить DNS: %w", err)
	}
	return nil
}

func (manager *Manager) SupportsIPv6(action routing.Action) bool {
	manager.mu.Lock()
	gateways := manager.gateways
	manager.mu.Unlock()
	if action == routing.ActionProxy {
		return !gateways.TunnelIPv6Disabled
	}
	return gateways.IPv6Index != 0 && gateways.IPv6Gateway.IsValid()
}

func (manager *Manager) target(action routing.Action, address netip.Addr) (int, netip.Addr, error) {
	return routeTarget(manager.gateways, action, address)
}

func routeTarget(gateways gatewayInfo, action routing.Action, address netip.Addr) (int, netip.Addr, error) {
	if action == routing.ActionProxy {
		if address.Is4() {
			return gateways.TunnelIndex, netip.IPv4Unspecified(), nil
		}
		if gateways.TunnelIPv6Disabled {
			return 0, netip.Addr{}, routing.ErrDirectIPv6Unavailable
		}
		return gateways.TunnelIndex, netip.IPv6Unspecified(), nil
	}
	if address.Is4() {
		if gateways.IPv4Index == 0 || !gateways.IPv4Gateway.IsValid() {
			return 0, netip.Addr{}, errors.New("не найден обычный IPv4-шлюз")
		}
		return gateways.IPv4Index, gateways.IPv4Gateway, nil
	}
	if gateways.IPv6Index == 0 || !gateways.IPv6Gateway.IsValid() {
		return 0, netip.Addr{}, routing.ErrDirectIPv6Unavailable
	}
	return gateways.IPv6Index, gateways.IPv6Gateway, nil
}

func (manager *Manager) expireLoop() {
	defer close(manager.done)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-manager.stop:
			return
		case <-manager.changes:
			timer := time.NewTimer(700 * time.Millisecond)
			select {
			case <-manager.stop:
				timer.Stop()
				return
			case <-timer.C:
			}
			manager.refreshGateways()
		case now := <-ticker.C:
			manager.expire(now)
		}
	}
}

func (manager *Manager) refreshGateways() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	updated, err := discoverGateways(ctx, manager.alias)
	if err != nil {
		debuglog.Error("routes.networkChange.discover", err)
		return
	}
	manager.mu.Lock()
	previous := manager.gateways
	if previous == updated {
		manager.mu.Unlock()
		return
	}
	debuglog.Event("routes.networkChange", "previous=%+v updated=%+v leases=%d", previous, updated, len(manager.leases))
	for address, item := range manager.leases {
		if item.action != routing.ActionDirect {
			continue
		}
		newIndex, newGateway, targetErr := routeTarget(updated, item.action, address)
		if targetErr != nil {
			debuglog.Error("routes.networkChange.target", targetErr)
			continue
		}
		if newIndex == item.interfaceIndex {
			continue
		}
		if err := addRoute(ctx, address, newIndex, newGateway); err != nil {
			debuglog.Error("routes.networkChange.add", err)
			continue
		}
		debuglog.Error("routes.networkChange.removeOld", removeManagedRoutesExcept(ctx, address, newIndex))
		item.interfaceIndex = newIndex
		manager.leases[address] = item
	}
	manager.gateways = updated
	handler := manager.onNetworkChange
	manager.mu.Unlock()
	if handler != nil {
		handler()
	}
	debuglog.Event("routes.networkChange", "Direct leases rebound; official AWG default-route monitor handles the outer UDP bind")
}

func (manager *Manager) expire(now time.Time) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for address, item := range manager.leases {
		if now.Before(item.expires) {
			continue
		}
		if removeManagedRoutesExcept(ctx, address, 0) == nil {
			delete(manager.leases, address)
		}
	}
}

func discoverGateways(ctx context.Context, tunnelAlias string) (gatewayInfo, error) {
	if err := ctx.Err(); err != nil {
		return gatewayInfo{}, err
	}
	tunnel, err := InterfaceByAlias(tunnelAlias)
	if err != nil {
		return gatewayInfo{}, err
	}
	result := gatewayInfo{
		TunnelIndex:        tunnel.Index,
		TunnelIPv6Disabled: !interfaceHasUsableIPv6(tunnel.Index),
	}
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		return gatewayInfo{}, fmt.Errorf("Windows не вернула таблицу маршрутов: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	v4Metric, v6Metric := ^uint64(0), ^uint64(0)
	for _, row := range table.Rows() {
		if row.InterfaceIndex == uint32(tunnel.Index) || row.DestinationPrefix.PrefixLength != 0 {
			continue
		}
		nextHop := addressFromRaw(row.NextHop)
		if !nextHop.IsValid() || nextHop.IsUnspecified() {
			continue
		}
		interfaceMetric, usable := usableInterfaceMetric(row.InterfaceIndex, nextHop)
		if !usable {
			continue
		}
		effectiveMetric := uint64(row.Metric) + uint64(interfaceMetric)
		if nextHop.Is4() && effectiveMetric < v4Metric {
			v4Metric = effectiveMetric
			result.IPv4Index = int(row.InterfaceIndex)
			result.IPv4Gateway = nextHop
		}
		if nextHop.Is6() && effectiveMetric < v6Metric {
			v6Metric = effectiveMetric
			result.IPv6Index = int(row.InterfaceIndex)
			result.IPv6Gateway = nextHop
		}
	}
	if result.IPv4Index == 0 {
		return gatewayInfo{}, errors.New("обычный IPv4-шлюз не найден")
	}
	return result, nil
}

func usableInterfaceMetric(index uint32, nextHop netip.Addr) (uint32, bool) {
	physical := windows.MibIfRow2{InterfaceIndex: index}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &physical); err != nil || physical.OperStatus != windows.IfOperStatusUp {
		return 0, false
	}
	family := uint16(windows.AF_INET6)
	if nextHop.Is4() {
		family = windows.AF_INET
	}
	ipInterface := windows.MibIpInterfaceRow{Family: family, InterfaceIndex: index}
	if err := windows.GetIpInterfaceEntry(&ipInterface); err != nil || ipInterface.Connected == 0 || ipInterface.DisableDefaultRoutes != 0 {
		return 0, false
	}
	return ipInterface.Metric, true
}

func interfaceHasUsableIPv6(index int) bool {
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return false
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, raw := range addrs {
		ipNet, ok := raw.(*net.IPNet)
		if !ok || ipNet == nil {
			continue
		}
		if addr, ok := netip.AddrFromSlice(ipNet.IP); ok {
			addr = addr.Unmap()
			if addr.Is6() && !addr.IsLinkLocalUnicast() && !addr.IsLoopback() && !addr.IsMulticast() && !addr.IsUnspecified() {
				return true
			}
		}
	}
	return false
}

func addRoute(ctx context.Context, address netip.Addr, interfaceIndex int, nextHop netip.Addr) (routeErr error) {
	debuglog.Event("routes.add", "address=%s interface=%d nextHop=%s metric=3", address, interfaceIndex, nextHop)
	defer func() { debuglog.Error("routes.add", routeErr) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	row := windows.MibIpForwardRow2{}
	if err := initializeIpForwardEntry(&row); err != nil {
		return fmt.Errorf("не удалось подготовить маршрут %s: %w", address, err)
	}
	row.InterfaceIndex = uint32(interfaceIndex)
	row.DestinationPrefix.Prefix = rawAddress(address)
	if address.Is4() {
		row.DestinationPrefix.PrefixLength = 32
	} else {
		row.DestinationPrefix.PrefixLength = 128
	}
	row.NextHop = rawAddress(nextHop)
	row.Metric = 3
	row.Protocol = windows.MIB_IPPROTO_NETMGMT
	if err := createIpForwardEntry(&row); err != nil {
		if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
			debuglog.Error("routes.removeBeforeAdd", removeRoute(ctx, address, interfaceIndex))
			if retryErr := createIpForwardEntry(&row); retryErr == nil {
				return nil
			} else {
				err = retryErr
			}
		}
		return fmt.Errorf("не удалось добавить маршрут %s: %w", address, err)
	}
	return nil
}

func removeRoute(ctx context.Context, address netip.Addr, interfaceIndex int) (routeErr error) {
	defer func() { debuglog.Error("routes.remove", routeErr) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	family := uint16(windows.AF_INET6)
	prefixLength := uint8(128)
	if address.Is4() {
		family = windows.AF_INET
		prefixLength = 32
	}
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(family, &table); err != nil {
		return fmt.Errorf("не удалось прочитать маршруты: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	var joined error
	for _, source := range table.Rows() {
		row := source
		if row.InterfaceIndex != uint32(interfaceIndex) || row.DestinationPrefix.PrefixLength != prefixLength || addressFromRaw(row.DestinationPrefix.Prefix) != address {
			continue
		}
		if row.Protocol != windows.MIB_IPPROTO_NETMGMT || row.Metric != 3 {
			continue
		}
		debuglog.Event("routes.remove", "address=%s interface=%d metric=%d", address, interfaceIndex, row.Metric)
		joined = errors.Join(joined, deleteIpForwardEntry(&row))
	}
	return joined
}

func removeManagedRoutesExcept(ctx context.Context, address netip.Addr, keepInterfaceIndex int) (routeErr error) {
	defer func() { debuglog.Error("routes.removeManaged", routeErr) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	family := uint16(windows.AF_INET6)
	prefixLength := uint8(128)
	if address.Is4() {
		family = windows.AF_INET
		prefixLength = 32
	}
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(family, &table); err != nil {
		return fmt.Errorf("не удалось прочитать маршруты: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	var joined error
	for _, source := range table.Rows() {
		row := source
		if keepInterfaceIndex != 0 && row.InterfaceIndex == uint32(keepInterfaceIndex) {
			continue
		}
		if row.DestinationPrefix.PrefixLength != prefixLength || addressFromRaw(row.DestinationPrefix.Prefix) != address {
			continue
		}
		if row.Protocol != windows.MIB_IPPROTO_NETMGMT || row.Metric != 3 {
			continue
		}
		debuglog.Event("routes.removeManaged", "address=%s interface=%d metric=%d", address, row.InterfaceIndex, row.Metric)
		joined = errors.Join(joined, deleteIpForwardEntry(&row))
	}
	return joined
}

// CleanupManagedRoutes removes any lingering Ex WG /32 and /128 host routes
// (MIB_IPPROTO_NETMGMT, Metric=3) left behind if an earlier process was
// forcefully terminated.
func CleanupManagedRoutes(ctx context.Context) (removed int, routeErr error) {
	defer func() { debuglog.Error("routes.cleanupManaged", routeErr) }()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		return 0, fmt.Errorf("не удалось прочитать маршруты: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	var joined error
	for _, source := range table.Rows() {
		row := source
		if !isManagedHostRoute(row) {
			continue
		}
		address := addressFromRaw(row.DestinationPrefix.Prefix)
		debuglog.Event("routes.cleanupManaged", "address=%s interface=%d metric=%d", address, row.InterfaceIndex, row.Metric)
		if err := deleteIpForwardEntry(&row); err != nil {
			joined = errors.Join(joined, err)
			continue
		}
		removed++
	}
	return removed, joined
}

func isManagedHostRoute(row windows.MibIpForwardRow2) bool {
	if row.Protocol != windows.MIB_IPPROTO_NETMGMT || row.Metric != 3 {
		return false
	}
	address := addressFromRaw(row.DestinationPrefix.Prefix)
	if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() || address.IsLinkLocalUnicast() {
		return false
	}
	if address.Is4() {
		return row.DestinationPrefix.PrefixLength == 32
	}
	return row.DestinationPrefix.PrefixLength == 128
}

func netsh(ctx context.Context, arguments ...string) error {
	command := exec.CommandContext(ctx, "netsh.exe", arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := command.CombinedOutput()
	detail := debuglog.DecodeWindowsOutput(output)
	debuglog.Event("command.netsh", "args=%q exit=%v\n%s", arguments, err, detail)
	if err != nil {
		return fmt.Errorf("netsh args=%q: %w\n%s", arguments, err, detail)
	}
	return nil
}
