//go:build windows

package tunnel

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"nexusclient/internal/debuglog"
)

const (
	rasMaxEntryName        = 256
	rasMaxDeviceType       = 16
	rasMaxDeviceName       = 128
	rasMaxPath             = 260
	rasErrorBufferTooSmall = 603
	rasErrorNoInterface    = 632
)

type rasConnection struct {
	Size          uint32
	Handle        windows.Handle
	EntryName     [rasMaxEntryName + 1]uint16
	DeviceType    [rasMaxDeviceType + 1]uint16
	DeviceName    [rasMaxDeviceName + 1]uint16
	Phonebook     [rasMaxPath]uint16
	SubEntry      uint32
	EntryGUID     windows.GUID
	Flags         uint32
	LUID          uint64
	CorrelationID windows.GUID
}

var (
	rasAPI                  = windows.NewLazySystemDLL("rasapi32.dll")
	procRasEnumConnectionsW = rasAPI.NewProc("RasEnumConnectionsW")
	procRasHangUpW          = rasAPI.NewProc("RasHangUpW")
)

// disconnectRASVPNs hangs up only RAS connections whose device type is VPN.
// It deliberately leaves PPPoE, dial-up, and other RAS transports untouched.
func disconnectRASVPNs(ctx context.Context) error {
	connections, err := enumerateRASConnections()
	if err != nil {
		return err
	}
	var handles []windows.Handle
	for _, connection := range connections {
		if !strings.EqualFold(windows.UTF16ToString(connection.DeviceType[:]), "vpn") {
			continue
		}
		name := windows.UTF16ToString(connection.EntryName[:])
		result, _, _ := procRasHangUpW.Call(uintptr(connection.Handle))
		if result != 0 {
			return fmt.Errorf("RasHangUpW(%s): %w", name, syscall.Errno(result))
		}
		handles = append(handles, connection.Handle)
		debuglog.Event("VPN.RAS.disconnect", "entry=%s device=%s", name, windows.UTF16ToString(connection.DeviceName[:]))
	}
	if len(handles) == 0 {
		debuglog.Event("VPN.RAS.disconnect", "no active Windows VPN connections")
		return nil
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		active, enumErr := enumerateRASConnections()
		if enumErr != nil {
			return enumErr
		}
		remaining := 0
		for _, connection := range active {
			for _, handle := range handles {
				if connection.Handle == handle {
					remaining++
				}
			}
		}
		if remaining == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func enumerateRASConnections() ([]rasConnection, error) {
	itemSize := uint32(unsafe.Sizeof(rasConnection{}))
	connections := make([]rasConnection, 1)
	connections[0].Size = itemSize
	bufferSize := itemSize
	var count uint32
	result, _, _ := procRasEnumConnectionsW.Call(
		uintptr(unsafe.Pointer(&connections[0])),
		uintptr(unsafe.Pointer(&bufferSize)),
		uintptr(unsafe.Pointer(&count)),
	)
	// Windows returns ERROR_NOINTERFACE when the RAS subsystem has no usable
	// interface. For our purpose that is equivalent to an empty connection list,
	// not a reason to block the unrelated AWG tunnel from starting.
	if result == rasErrorNoInterface && count == 0 {
		debuglog.Event("VPN.RAS.enumerate", "RAS interface is unavailable; no Windows VPN connection to stop")
		return nil, nil
	}
	if result == rasErrorBufferTooSmall {
		capacity := int((bufferSize + itemSize - 1) / itemSize)
		connections = make([]rasConnection, capacity)
		connections[0].Size = itemSize
		result, _, _ = procRasEnumConnectionsW.Call(
			uintptr(unsafe.Pointer(&connections[0])),
			uintptr(unsafe.Pointer(&bufferSize)),
			uintptr(unsafe.Pointer(&count)),
		)
		if result == rasErrorNoInterface && count == 0 {
			debuglog.Event("VPN.RAS.enumerate", "RAS interface became unavailable; no Windows VPN connection to stop")
			return nil, nil
		}
	}
	if result != 0 {
		return nil, fmt.Errorf("RasEnumConnectionsW: %w", syscall.Errno(result))
	}
	if count == 0 {
		return nil, nil
	}
	if int(count) > len(connections) {
		return nil, fmt.Errorf("RasEnumConnectionsW returned %d connections for a %d-item buffer", count, len(connections))
	}
	return connections[:count], nil
}
