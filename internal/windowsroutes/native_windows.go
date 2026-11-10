//go:build windows

package windowsroutes

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"nexusclient/internal/debuglog"
)

type InterfaceInfo struct {
	Alias string
	Index int
	Up    bool
	MTU   int
	RX    uint64
	TX    uint64
}

var ErrInterfaceNotFound = errors.New("сетевой интерфейс не найден")

var (
	ipHelperDLL              = windows.NewLazySystemDLL("iphlpapi.dll")
	initializeIpForwardProc  = ipHelperDLL.NewProc("InitializeIpForwardEntry")
	createIpForwardEntryProc = ipHelperDLL.NewProc("CreateIpForwardEntry2")
	deleteIpForwardEntryProc = ipHelperDLL.NewProc("DeleteIpForwardEntry2")
	getBestRouteProc         = ipHelperDLL.NewProc("GetBestRoute2")
	dnsAPIDLL                = windows.NewLazySystemDLL("dnsapi.dll")
	dnsFlushResolverProc     = dnsAPIDLL.NewProc("DnsFlushResolverCache")
)

func FlushDNSCache() error {
	result, _, _ := dnsFlushResolverProc.Call()
	if result == 0 {
		return errors.New("Windows не очистила системный DNS-кэш")
	}
	return nil
}

func InterfaceByAlias(alias string) (InterfaceInfo, error) {
	var table *windows.MibIfTable2
	if err := windows.GetIfTable2Ex(windows.MibIfTableNormal, &table); err != nil {
		return InterfaceInfo{}, fmt.Errorf("Windows не вернула список интерфейсов: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	for _, row := range rows {
		rowAlias := windows.UTF16ToString(row.Alias[:])
		if strings.EqualFold(strings.TrimSpace(rowAlias), strings.TrimSpace(alias)) {
			mtu := int(row.Mtu)
			ipRow := windows.MibIpInterfaceRow{Family: windows.AF_INET, InterfaceIndex: row.InterfaceIndex}
			ipErr := windows.GetIpInterfaceEntry(&ipRow)
			if errors.Is(ipErr, windows.ERROR_NOT_FOUND) && row.OperStatus != windows.IfOperStatusUp {
				continue
			}
			if !errors.Is(ipErr, windows.ERROR_NOT_FOUND) {
				debuglog.Error("GetIpInterfaceEntry", ipErr)
			}
			if ipErr == nil && ipRow.NlMtu != 0 {
				mtu = int(ipRow.NlMtu)
			}
			return InterfaceInfo{
				Alias: rowAlias,
				Index: int(row.InterfaceIndex),
				Up:    row.OperStatus == windows.IfOperStatusUp,
				MTU:   mtu,
				RX:    row.InOctets,
				TX:    row.OutOctets,
			}, nil
		}
	}
	return InterfaceInfo{}, fmt.Errorf("%w: AWG-интерфейс %q", ErrInterfaceNotFound, alias)
}

func BestRouteIndex(address netip.Addr) (int, error) {
	if !address.IsValid() {
		return 0, errors.New("адрес для проверки маршрута не задан")
	}
	destination := rawAddress(address.Unmap())
	best := windows.MibIpForwardRow2{}
	source := windows.RawSockaddrInet{}
	result, _, _ := getBestRouteProc.Call(
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&destination)),
		0,
		uintptr(unsafe.Pointer(&best)),
		uintptr(unsafe.Pointer(&source)),
	)
	if result != 0 {
		return 0, syscall.Errno(result)
	}
	debuglog.Event("GetBestRoute2", "destination=%s interface=%d source=%s nextHop=%s metric=%d", address, best.InterfaceIndex, addressFromRaw(source), addressFromRaw(best.NextHop), best.Metric)
	return int(best.InterfaceIndex), nil
}

func initializeIpForwardEntry(row *windows.MibIpForwardRow2) error {
	// InitializeIpForwardEntry has a void return value; it only fills defaults.
	_, _, _ = initializeIpForwardProc.Call(uintptr(unsafe.Pointer(row)))
	return nil
}

func createIpForwardEntry(row *windows.MibIpForwardRow2) error {
	result, _, _ := createIpForwardEntryProc.Call(uintptr(unsafe.Pointer(row)))
	if result != 0 {
		return syscall.Errno(result)
	}
	return nil
}

func deleteIpForwardEntry(row *windows.MibIpForwardRow2) error {
	result, _, _ := deleteIpForwardEntryProc.Call(uintptr(unsafe.Pointer(row)))
	// ERROR_FILE_NOT_FOUND is returned when the route's interface (e.g. the
	// Nexus adapter) has already been removed together with its routes.
	if result == 0 || syscall.Errno(result) == windows.ERROR_NOT_FOUND || syscall.Errno(result) == windows.ERROR_FILE_NOT_FOUND {
		return nil
	}
	return syscall.Errno(result)
}

func rawAddress(address netip.Addr) windows.RawSockaddrInet {
	var raw windows.RawSockaddrInet
	if address.Is4() {
		value := (*windows.RawSockaddrInet4)(unsafe.Pointer(&raw))
		value.Family = windows.AF_INET
		value.Addr = address.As4()
		return raw
	}
	value := (*windows.RawSockaddrInet6)(unsafe.Pointer(&raw))
	value.Family = windows.AF_INET6
	value.Addr = address.As16()
	if zone := address.Zone(); zone != "" {
		if scope, err := strconv.ParseUint(zone, 10, 32); err == nil {
			value.Scope_id = uint32(scope)
		}
	}
	return raw
}

func addressFromRaw(raw windows.RawSockaddrInet) netip.Addr {
	switch raw.Family {
	case windows.AF_INET:
		value := (*windows.RawSockaddrInet4)(unsafe.Pointer(&raw))
		return netip.AddrFrom4(value.Addr)
	case windows.AF_INET6:
		value := (*windows.RawSockaddrInet6)(unsafe.Pointer(&raw))
		address := netip.AddrFrom16(value.Addr)
		if value.Scope_id != 0 {
			address = address.WithZone(strconv.FormatUint(uint64(value.Scope_id), 10))
		}
		return address
	default:
		return netip.Addr{}
	}
}
