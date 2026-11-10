//go:build windows

package windowsroutes

import (
	"fmt"
	"net"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"nexusclient/internal/debuglog"
)

// DiagnosticSnapshot is read-only; it does not repair or install any route.
func DiagnosticSnapshot() string {
	var out strings.Builder
	interfaces, err := net.Interfaces()
	if err != nil {
		out.WriteString("net.Interfaces: " + debuglog.ErrorDetail(err))
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		fmt.Fprintf(&out, "interface index=%d name=%q mtu=%d flags=%s hardware=%s addresses=%v\n", iface.Index, iface.Name, iface.MTU, iface.Flags, iface.HardwareAddr, addresses)
		if err != nil {
			out.WriteString(debuglog.ErrorDetail(err))
		}
	}
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		out.WriteString("GetIpForwardTable2: " + debuglog.ErrorDetail(err))
		return out.String()
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	out.WriteString("--- IPv4 / IPv6 route table ---\n")
	for _, row := range table.Rows() {
		fmt.Fprintf(&out, "destination=%s/%d nextHop=%s interfaceIndex=%d LUID=%d metric=%d protocol=%d origin=%d validLifetime=%d preferredLifetime=%d\n", addressFromRaw(row.DestinationPrefix.Prefix), row.DestinationPrefix.PrefixLength, addressFromRaw(row.NextHop), row.InterfaceIndex, row.InterfaceLuid, row.Metric, row.Protocol, row.Origin, row.ValidLifetime, row.PreferredLifetime)
	}
	return out.String()
}
