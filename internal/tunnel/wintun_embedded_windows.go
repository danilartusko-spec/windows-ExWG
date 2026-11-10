//go:build windows

package tunnel

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/amnezia-vpn/amneziawg-windows/v3/tunnel/winipcfg"
	"golang.org/x/sys/windows"
	"nexusclient/internal/debuglog"
	"nexusclient/internal/windowsroutes"
)

// Keep Wintun inside the executable as well as in the portable distribution.
// A user may move NexusClient.exe out of the extracted folder, while the VPN
// service is launched by SCM with an unrelated working directory. Preloading
// this private, versioned copy by absolute path makes both cases deterministic.
//
//go:embed assets/wintun.dll
var embeddedWintunDLL []byte

func prepareWintunDLL() (windows.Handle, string, error) {
	if len(embeddedWintunDLL) == 0 {
		return 0, "", fmt.Errorf("embedded wintun.dll is empty")
	}
	sum := sha256.Sum256(embeddedWintunDLL)
	programData := strings.TrimSpace(os.Getenv("ProgramData"))
	if programData == "" {
		programData = `C:\ProgramData`
	}
	runtimeDir := filepath.Join(programData, "NexusClient", "Runtime", fmt.Sprintf("%x", sum[:8]))
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return 0, "", fmt.Errorf("create private Wintun directory: %w", err)
	}
	path := filepath.Join(runtimeDir, "wintun.dll")
	valid := false
	if current, err := os.ReadFile(path); err == nil {
		valid = sha256.Sum256(current) == sum
	}
	if !valid {
		temporary, err := os.CreateTemp(runtimeDir, "wintun-*.tmp")
		if err != nil {
			return 0, "", fmt.Errorf("create temporary Wintun DLL: %w", err)
		}
		temporaryPath := temporary.Name()
		removeTemporary := true
		defer func() {
			if removeTemporary {
				_ = os.Remove(temporaryPath)
			}
		}()
		if _, err = temporary.Write(embeddedWintunDLL); err == nil {
			err = temporary.Sync()
		}
		if closeErr := temporary.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return 0, "", fmt.Errorf("write private Wintun DLL: %w", err)
		}
		_ = os.Remove(path)
		if err := os.Rename(temporaryPath, path); err != nil {
			return 0, "", fmt.Errorf("install private Wintun DLL: %w", err)
		}
		removeTemporary = false
	}
	handle, err := windows.LoadLibrary(path)
	if err != nil {
		return 0, path, fmt.Errorf("preload Wintun DLL %q: %w", path, err)
	}
	return handle, path, nil
}

// wintunAdapterMissing reports Win32 codes that WintunOpenAdapter returns when
// the device node is already gone. The NDIS interface row can outlive the PnP
// device for a short time after the AWG service stops, so InterfaceByAlias may
// still see "Nexus" while Wintun returns ERROR_NOT_FOUND (1168).
func wintunAdapterMissing(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND)
}

// waitAdapterGone gives the stopped AWG service time to remove its own adapter
// before Ex WG forces a removal. Returns true once the alias disappears.
func waitAdapterGone(adapterName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := windowsroutes.InterfaceByAlias(adapterName); errors.Is(err, windowsroutes.ErrInterfaceNotFound) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func cleanupOrphanedWintunAdapter(adapterName string) (bool, error) {
	if waitAdapterGone(adapterName, 250*time.Millisecond) {
		return false, nil
	}
	handle, _, err := prepareWintunDLL()
	if err != nil {
		return false, err
	}
	defer windows.FreeLibrary(handle)

	openProc, err := windows.GetProcAddress(handle, "WintunOpenAdapter")
	if err != nil {
		return false, fmt.Errorf("WintunOpenAdapter proc: %w", err)
	}
	closeProc, err := windows.GetProcAddress(handle, "WintunCloseAdapter")
	if err != nil {
		return false, fmt.Errorf("WintunCloseAdapter proc: %w", err)
	}
	luidProc, _ := windows.GetProcAddress(handle, "WintunGetAdapterLUID")

	name16, err := windows.UTF16PtrFromString(adapterName)
	if err != nil {
		return false, err
	}
	adapterHandle, _, openErr := syscall.SyscallN(openProc, uintptr(unsafe.Pointer(name16)))
	if adapterHandle == 0 {
		if wintunAdapterMissing(openErr) {
			debuglog.Event("Wintun.cleanup", "adapter=%s interface row still present but Wintun device is gone: %v", adapterName, openErr)
			return false, nil
		}
		return false, fmt.Errorf("WintunOpenAdapter(%q): %w", adapterName, openErr)
	}
	if luidProc != 0 {
		var rawLUID uint64
		_, _, _ = syscall.SyscallN(luidProc, adapterHandle, uintptr(unsafe.Pointer(&rawLUID)))
		if rawLUID != 0 {
			luid := winipcfg.LUID(rawLUID)
			_ = luid.FlushRoutes(windows.AF_INET)
			_ = luid.FlushIPAddresses(windows.AF_INET)
			_ = luid.FlushDNS(windows.AF_INET)
			_ = luid.FlushRoutes(windows.AF_INET6)
			_ = luid.FlushIPAddresses(windows.AF_INET6)
			_ = luid.FlushDNS(windows.AF_INET6)
		}
	}
	// WintunCloseAdapter returns void; the adapter is released either way.
	_, _, _ = syscall.SyscallN(closeProc, adapterHandle)
	debuglog.Event("Wintun.cleanup", "removed orphaned adapter=%s", adapterName)
	return true, nil
}
