//go:build windows

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/amnezia-vpn/amneziawg-windows/v3/ringlogger"
	awgtunnel "github.com/amnezia-vpn/amneziawg-windows/v3/tunnel"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"nexusclient/internal/debuglog"
	"nexusclient/internal/windowsroutes"
)

const (
	ProfileName = "Nexus"
	ServiceName = "NexusAWGTunnel$" + ProfileName
)

func RunService(configPath string, ownerPID ...int) error {
	wintunHandle, wintunPath, err := prepareWintunDLL()
	if err != nil {
		debuglog.Error("Wintun.prepare", err)
		return err
	}
	defer windows.FreeLibrary(wintunHandle)
	debuglog.Event("Wintun.prepare", "preloaded=%s", wintunPath)
	// Initialize before awgtunnel.Run: its second InitGlobalLogger call is a
	// no-op. Keep the upstream ring log AND the persistent unabridged TXT log.
	if err := ringlogger.InitGlobalLogger("TUN"); err != nil {
		debuglog.Error("AWG.InitGlobalLogger", err)
		return err
	}
	if journal := debuglog.Default(); journal != nil {
		log.SetOutput(io.MultiWriter(ringlogger.Global, journal))
	}

	serviceDone := make(chan struct{})
	defer func() {
		close(serviceDone)
		cleanupPostServiceExit()
	}()
	if pid := resolveOwnerPID(ownerPID, os.Args); pid > 0 {
		startOwnerWatchdog(pid, serviceDone)
	}
	return awgtunnel.Run(configPath)
}

func resolveOwnerPID(explicit []int, args []string) int {
	if len(explicit) > 0 && explicit[0] > 0 {
		return explicit[0]
	}
	if len(args) >= 4 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(args[3])); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

func startOwnerWatchdog(pid int, serviceDone <-chan struct{}) {
	ownerHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		debuglog.Error("AWG.watchdog.openOwner", err)
		return
	}
	ownerExited := make(chan struct{})
	go func() {
		defer close(ownerExited)
		_, _ = windows.WaitForSingleObject(ownerHandle, windows.INFINITE)
	}()
	go func() {
		defer windows.CloseHandle(ownerHandle)
		select {
		case <-serviceDone:
			return
		case <-ownerExited:
		}
		select {
		case <-serviceDone:
			return
		case <-time.After(1500 * time.Millisecond):
		}
		debuglog.Event("AWG.watchdog", "owner pid=%d terminated while tunnel was active; stopping service", pid)
		stopAndDeleteSelfService()
		select {
		case <-serviceDone:
			return
		case <-time.After(4 * time.Second):
			cleanupPostServiceExit()
			os.Exit(0)
		}
	}()
}

func stopAndDeleteSelfService() {
	manager, err := mgr.Connect()
	if err != nil {
		debuglog.Error("AWG.watchdog.scmConnect", err)
		return
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(ServiceName)
	if err != nil {
		if !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			debuglog.Error("AWG.watchdog.openService", err)
		}
		return
	}
	defer service.Close()
	_, _ = service.Control(svc.Stop)
	if err := service.Delete(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		debuglog.Error("AWG.watchdog.deleteService", err)
	}
}

func deleteSelfService() {
	manager, err := mgr.Connect()
	if err != nil {
		return
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(ServiceName)
	if err != nil {
		return
	}
	defer service.Close()
	_ = service.Delete()
}

func cleanupPostServiceExit() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if removed, err := windowsroutes.CleanupManagedRoutes(ctx); err != nil {
		debuglog.Error("AWG.cleanup.routes", err)
	} else if removed > 0 {
		debuglog.Event("AWG.cleanup.routes", "removed=%d", removed)
	}
	_ = windowsroutes.FlushDNSCache()
	deleteSelfService()
}

func InstallAndStart(ctx context.Context, configText string) (path string, startErr error) {
	debuglog.Event("SCM.install", "service=%s", ServiceName)
	defer func() { debuglog.Error("SCM.install", startErr) }()
	configPath, err := saveLockedConfig(ctx, configText)
	if err != nil {
		return "", err
	}
	// The service only needs the file while starting. Once UAPI is available the
	// parsed configuration lives in the service process, so do not retain a
	// plaintext copy of the private key on disk.
	defer func() {
		removeErr := os.Remove(configPath)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			debuglog.Error("AWG.config.remove", removeErr)
		} else {
			debuglog.Event("AWG.config.remove", "temporary runtime configuration removed")
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", err
	}

	manager, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("нужны права администратора для управления VPN-службой: %w", err)
	}
	defer manager.Disconnect()
	if err := removeExistingService(ctx, manager); err != nil {
		return "", err
	}
	if _, adapterErr := cleanupOrphanedWintunAdapter(ProfileName); adapterErr != nil {
		debuglog.Error("SCM.install.adapterPreclean", adapterErr)
	}

	serviceArgs := []string{"/tunnelservice", configPath, strconv.Itoa(os.Getpid())}
	if debuglog.Enabled() {
		serviceArgs = append(serviceArgs, "--debuglog")
	}
	service, err := manager.CreateService(ServiceName, executable, mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartManual,
		ErrorControl: mgr.ErrorNormal,
		Dependencies: []string{"Nsi", "TcpIp"},
		DisplayName:  "Nexus AmneziaWG Tunnel",
		SidType:      windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}, serviceArgs...)
	if err != nil {
		return "", fmt.Errorf("не удалось создать VPN-службу: %w", err)
	}
	defer service.Close()
	if err := service.Start(); err != nil {
		_ = service.Delete()
		return "", fmt.Errorf("не удалось запустить VPN-службу: %w", err)
	}
	debuglog.Event("SCM.Start", "request accepted; waiting for Running and UAPI")
	if err := waitForState(ctx, service, svc.Running); err != nil {
		return "", err
	}
	if err := WaitReady(ctx); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = removeExistingService(cleanupCtx, manager)
		return "", err
	}
	return configPath, nil
}

func Stop(ctx context.Context) error {
	var joined error
	manager, err := mgr.Connect()
	if err != nil {
		joined = errors.Join(joined, err)
	} else {
		_, removeErr := removeExistingServiceStatus(ctx, manager)
		joined = errors.Join(joined, removeErr)
		_ = manager.Disconnect()
	}
	// The AWG service removes its own adapter on stop. Forced removal here is
	// only a safety net, so a failure is logged (with the original Win32 code)
	// and retried by InstallAndStart/CleanupOrphaned instead of reporting the
	// whole disconnect as failed.
	if _, adapterErr := cleanupOrphanedWintunAdapter(ProfileName); adapterErr != nil {
		debuglog.Error("tunnel.Stop.adapter", adapterErr)
	}
	_, routeErr := windowsroutes.CleanupManagedRoutes(ctx)
	joined = errors.Join(joined, routeErr)
	return joined
}

// CleanupOrphaned removes any leftover tunnel service, orphaned Wintun adapter,
// or managed /32 and /128 host routes from a previous aborted session or
// Windows Fast Startup reboot.
func CleanupOrphaned(ctx context.Context) error {
	var joined error
	hadService := false
	if manager, err := mgr.Connect(); err == nil {
		var removeErr error
		hadService, removeErr = removeExistingServiceStatus(ctx, manager)
		joined = errors.Join(joined, removeErr)
		_ = manager.Disconnect()
	}
	hadAdapter, adapterErr := cleanupOrphanedWintunAdapter(ProfileName)
	joined = errors.Join(joined, adapterErr)
	removedRoutes, routeErr := windowsroutes.CleanupManagedRoutes(ctx)
	joined = errors.Join(joined, routeErr)
	if hadService || hadAdapter || removedRoutes > 0 {
		_ = windowsroutes.FlushDNSCache()
		debuglog.Event("tunnel.cleanupOrphaned", "service=%t adapter=%t routes=%d", hadService, hadAdapter, removedRoutes)
	}
	return joined
}

// StopCompetingVPNs disconnects Windows RAS sessions and stops known
// WireGuard/AmneziaWG tunnel services before Ex WG creates its own adapter.
// It deliberately does not touch arbitrary network adapters or unrelated
// services, because those can be physical, Hyper-V, or remote-access links.
func StopCompetingVPNs(ctx context.Context) error {
	if err := disconnectRASVPNs(ctx); err != nil {
		return fmt.Errorf("не удалось отключить Windows VPN: %w", err)
	}

	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("не удалось открыть диспетчер служб для остановки других VPN: %w", err)
	}
	defer manager.Disconnect()
	names, err := manager.ListServices()
	if err != nil {
		return fmt.Errorf("не удалось получить список VPN-служб: %w", err)
	}
	var stopErr error
	for _, name := range names {
		if !isCompetingTunnelService(name) {
			continue
		}
		service, openErr := manager.OpenService(name)
		if openErr != nil {
			stopErr = errors.Join(stopErr, fmt.Errorf("%s: %w", name, openErr))
			continue
		}
		status, queryErr := service.Query()
		if queryErr == nil && status.State != svc.Stopped {
			_, controlErr := service.Control(svc.Stop)
			if controlErr == nil {
				controlErr = waitForServiceState(ctx, service, name, svc.Stopped)
			}
			if controlErr != nil {
				stopErr = errors.Join(stopErr, fmt.Errorf("%s: %w", name, controlErr))
			} else {
				debuglog.Event("VPN.service.stop", "service=%s stopped", name)
			}
		} else if queryErr != nil {
			stopErr = errors.Join(stopErr, fmt.Errorf("%s: %w", name, queryErr))
		}
		service.Close()
	}
	if stopErr != nil {
		return fmt.Errorf("не удалось остановить другую VPN-службу: %w", stopErr)
	}
	return nil
}

func isCompetingTunnelService(name string) bool {
	if strings.EqualFold(name, ServiceName) {
		return false
	}
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "wireguardtunnel$") ||
		strings.HasPrefix(lower, "amneziawgtunnel$") ||
		strings.HasPrefix(lower, "nexusawgtunnel$") ||
		strings.Contains(lower, "openvpn")
}

func waitForServiceState(ctx context.Context, service *mgr.Service, name string, desired svc.State) error {
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := service.Query()
		if err != nil {
			return err
		}
		if status.State == desired {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("ожидание %s: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func removeExistingService(ctx context.Context, manager *mgr.Mgr) error {
	_, err := removeExistingServiceStatus(ctx, manager)
	return err
}

func removeExistingServiceStatus(ctx context.Context, manager *mgr.Mgr) (bool, error) {
	service, err := manager.OpenService(ServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer service.Close()
	status, queryErr := service.Query()
	debuglog.Error("SCM.Query.beforeStop", queryErr)
	if queryErr == nil && status.State != svc.Stopped {
		stopStatus, stopErr := service.Control(svc.Stop)
		debuglog.Event("SCM.Control.Stop", "status=%+v", stopStatus)
		debuglog.Error("SCM.Control.Stop", stopErr)
		debuglog.Error("SCM.waitStopped", waitForState(ctx, service, svc.Stopped))
	}
	if err := service.Delete(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return true, fmt.Errorf("не удалось удалить старую VPN-службу: %w", err)
	}
	debuglog.Event("SCM.Delete", "service=%s delete requested", ServiceName)
	return true, nil
}

func waitForState(ctx context.Context, service *mgr.Service, desired svc.State) error {
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	var previous svc.Status
	for {
		status, err := service.Query()
		if err != nil {
			return err
		}
		if status != previous {
			debuglog.Event("SCM.Query", "service=%s desired=%d status=%+v", ServiceName, desired, status)
			previous = status
		}
		if status.State == desired {
			return nil
		}
		if desired == svc.Running && status.State == svc.Stopped {
			return fmt.Errorf("SCM: service stopped during startup; Win32ExitCode=%d (0x%08X); ServiceSpecificExitCode=%d; status=%+v", status.Win32ExitCode, status.Win32ExitCode, status.ServiceSpecificExitCode, status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func saveLockedConfig(ctx context.Context, configText string) (string, error) {
	programData := os.Getenv("ProgramData")
	if strings.TrimSpace(programData) == "" {
		programData = `C:\ProgramData`
	}
	directory := filepath.Join(programData, "NexusClient", "Configurations")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	if err := lockConfigurationDirectory(ctx, directory); err != nil {
		return "", err
	}
	path := filepath.Join(directory, ProfileName+".conf")
	temporary := path + ".new"
	if err := os.WriteFile(temporary, []byte(configText), 0600); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return "", err
	}

	command := exec.CommandContext(ctx, "icacls.exe", path,
		"/inheritance:r",
		"/grant:r", "*S-1-5-18:(F)",
		"/grant:r", "*S-1-5-32-544:(F)",
	)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := command.CombinedOutput(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("icacls configuration ACL: %w\n%s", err, debuglog.DecodeWindowsOutput(output))
	}
	return path, nil
}

var (
	lockedConfigDirMu sync.Mutex
	lockedConfigDir   string
)

func lockConfigurationDirectory(ctx context.Context, directory string) error {
	lockedConfigDirMu.Lock()
	if lockedConfigDir == directory {
		lockedConfigDirMu.Unlock()
		return nil
	}
	lockedConfigDirMu.Unlock()
	command := exec.CommandContext(ctx, "icacls.exe", directory,
		"/inheritance:r",
		"/grant:r", "*S-1-5-18:(OI)(CI)(F)",
		"/grant:r", "*S-1-5-32-544:(OI)(CI)(F)",
	)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("icacls configuration directory ACL: %w\n%s", err, debuglog.DecodeWindowsOutput(output))
	}
	lockedConfigDirMu.Lock()
	lockedConfigDir = directory
	lockedConfigDirMu.Unlock()
	return nil
}
