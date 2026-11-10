//go:build windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"nexusclient/internal/debuglog"
	"nexusclient/internal/tunnel"
)

const (
	coreInstanceMutexName = `Local\ExWG.Client.Windows.CoreInstance`
	coreShutdownEventName = `Local\ExWG.Client.Windows.CoreShutdown`
	windowClassName       = "FLUTTER_RUNNER_WIN32_WINDOW"
	windowTitle           = "Ex WG"
	wmAppQuitMessage      = 0x8000 + 3 // WM_APP + 3
)

type rpcRequest struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     int64  `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func main() {
	service := len(os.Args) >= 3 && len(os.Args) <= 5 && strings.EqualFold(os.Args[1], "/tunnelservice")
	log.SetOutput(io.Discard)
	log.SetFlags(0)
	if service && slices.Contains(os.Args[3:], "--debuglog") {
		if journal, err := debuglog.Init(true); err == nil {
			defer journal.Close()
			log.SetOutput(journal)
		}
	}
	defer func() {
		if journal := debuglog.Default(); journal != nil {
			_ = journal.Close()
		}
	}()
	debuglog.Event("process.start", "shell=flutter-core core=%s service=%v", debuglog.Version, service)
	if service {
		if err := tunnel.RunService(os.Args[2]); err != nil {
			debuglog.Error("AWG.RunService", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && strings.EqualFold(os.Args[1], "--cleanup") {
		if err := runStandaloneCleanup(); err != nil {
			debuglog.Error("wg.core.cleanup", err)
			fmt.Fprintln(os.Stderr, debuglog.ErrorDetail(err))
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 2 || os.Args[1] != "--ipc-stdio" {
		fmt.Fprintln(os.Stderr, "ExWGCore is started by ExWG.exe")
		os.Exit(2)
	}
	// Wait (bounded, below the UI's 12 s start timeout) for a previous core
	// that is still removing its tunnel, then hold the mutex until exit.
	coreMutex, mutexErr := acquireCoreInstanceMutex(10 * time.Second)
	debuglog.Error("wg.core.mutex", mutexErr)
	if coreMutex != 0 {
		defer windows.CloseHandle(coreMutex)
	}
	app, err := NewExWGApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, debuglog.ErrorDetail(err))
		os.Exit(1)
	}
	app.startup()
	defer app.shutdown()
	watchShutdownEvent(app)
	if err := serveRPC(os.Stdin, os.Stdout, app); err != nil && !errors.Is(err, io.EOF) {
		debuglog.Error("flutter.rpc", err)
		fmt.Fprintln(os.Stderr, debuglog.ErrorDetail(err))
	}
}

// acquireCoreInstanceMutex returns an owned mutex handle that is kept for the
// whole process lifetime. Win32 mutex ownership belongs to an OS thread while
// goroutines migrate between threads, so the mutex is never released
// explicitly: Windows abandons it when the process exits, and waiters treat
// WAIT_ABANDONED as "core finished".
func acquireCoreInstanceMutex(timeout time.Duration) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(coreInstanceMutexName)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if handle == 0 {
		return 0, err
	}
	waitResult, waitErr := windows.WaitForSingleObject(handle, uint32(timeout.Milliseconds()))
	if waitResult != windows.WAIT_OBJECT_0 && waitResult != windows.WAIT_ABANDONED {
		_ = windows.CloseHandle(handle)
		if waitErr != nil {
			return 0, waitErr
		}
		return 0, fmt.Errorf("WaitForSingleObject(%s)=0x%X", coreInstanceMutexName, waitResult)
	}
	return handle, nil
}

// watchShutdownEvent lets ExWG.exe (on exit or Windows shutdown) and the
// installer request a synchronous teardown. Without it the UI would wait for
// the core while the core waits for stdin EOF from the still-running UI.
func watchShutdownEvent(app *ExWGApp) {
	name, err := windows.UTF16PtrFromString(coreShutdownEventName)
	if err != nil {
		return
	}
	event, err := windows.CreateEvent(nil, 1, 0, name)
	if event == 0 {
		debuglog.Error("wg.core.shutdownEvent.create", err)
		return
	}
	_ = windows.ResetEvent(event)
	go func() {
		if result, _ := windows.WaitForSingleObject(event, windows.INFINITE); result != windows.WAIT_OBJECT_0 {
			return
		}
		debuglog.Event("wg.core.shutdownEvent", "shutdown requested by ExWG.exe or installer")
		app.shutdown()
		if journal := debuglog.Default(); journal != nil {
			_ = journal.Sync()
		}
		os.Exit(0)
	}()
}

func signalCoreShutdown() {
	name, err := windows.UTF16PtrFromString(coreShutdownEventName)
	if err != nil {
		return
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil || event == 0 {
		return
	}
	_ = windows.SetEvent(event)
	_ = windows.CloseHandle(event)
}

func postQuitToFlutterWindow() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	findWindowW := user32.NewProc("FindWindowW")
	postMessageW := user32.NewProc("PostMessageW")
	classPtr, _ := windows.UTF16PtrFromString(windowClassName)
	titlePtr, _ := windows.UTF16PtrFromString(windowTitle)
	if hwnd, _, _ := findWindowW.Call(uintptr(unsafe.Pointer(classPtr)), uintptr(unsafe.Pointer(titlePtr))); hwnd != 0 {
		_, _, _ = postMessageW.Call(hwnd, wmAppQuitMessage, 0, 0)
	}
}

func runStandaloneCleanup() error {
	postQuitToFlutterWindow()
	signalCoreShutdown()
	if coreMutex, err := acquireCoreInstanceMutex(15 * time.Second); coreMutex != 0 {
		defer windows.CloseHandle(coreMutex)
	} else {
		debuglog.Error("wg.core.cleanup.waitCore", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return tunnel.CleanupOrphaned(ctx)
}

func serveRPC(input io.Reader, output io.Writer, app *ExWGApp) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var request rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if encodeErr := encoder.Encode(rpcResponse{Error: err.Error()}); encodeErr != nil {
				return encodeErr
			}
			continue
		}
		result, stop, err := dispatchRPC(app, request.Method, request.Params)
		response := rpcResponse{ID: request.ID, Result: result}
		if err != nil {
			response.Result = nil
			response.Error = debuglog.ErrorDetail(err)
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
	return scanner.Err()
}

func dispatchRPC(app *ExWGApp, method string, raw json.RawMessage) (any, bool, error) {
	decode := func(target any) error {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		return json.Unmarshal(raw, target)
	}
	switch method {
	case "state":
		return app.State(), false, nil
	case "selectProfile":
		var params struct {
			ID string `json:"id"`
		}
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		state, err := app.SelectProfile(params.ID)
		return state, false, err
	case "getProfile":
		var params struct {
			ID string `json:"id"`
		}
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		profile, err := app.GetProfile(params.ID)
		return profile, false, err
	case "saveProfile":
		var params ProfileEditor
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		state, err := app.SaveProfile(params)
		return state, false, err
	case "deleteProfile":
		var params struct {
			ID string `json:"id"`
		}
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		state, err := app.DeleteProfile(params.ID)
		return state, false, err
	case "importProfiles":
		var params struct {
			Paths []string `json:"paths"`
		}
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		state, err := app.ImportProfiles(params.Paths)
		return state, false, err
	case "toggleConnection":
		state, err := app.ToggleConnection()
		return state, false, err
	case "routingDetails":
		details, err := app.RoutingDetails()
		return details, false, err
	case "checkRoute":
		var params struct {
			Domain string `json:"domain"`
		}
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		result, err := app.CheckRoute(params.Domain)
		return result, false, err
	case "saveRouting":
		var params struct {
			Source    string `json:"source"`
			Overrides string `json:"overrides"`
		}
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		details, err := app.SaveRouting(params.Source, params.Overrides)
		return details, false, err
	case "refreshRouting":
		var params struct {
			Reset bool `json:"reset"`
		}
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		details, err := app.RefreshRouting(params.Reset)
		return details, false, err
	case "diagnosticsPreview":
		return app.DiagnosticsPreview(), false, nil
	case "setDiagnosticsEnabled":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		enabled, err := app.SetDiagnosticsEnabled(params.Enabled)
		return enabled, false, err
	case "runDiagnostics":
		text, err := app.RunDiagnostics()
		return text, false, err
	case "fullDiagnostics":
		return app.FullDiagnostics(), false, nil
	case "saveDiagnostics":
		var params struct {
			Path string `json:"path"`
		}
		if err := decode(&params); err != nil {
			return nil, false, err
		}
		path, err := app.SaveDiagnostics(params.Path)
		return path, false, err
	case "checkForUpdates":
		info, err := app.CheckForUpdates()
		return info, false, err
	case "installUpdate":
		ok, err := app.InstallUpdate()
		return ok, false, err
	case "shutdown":
		app.shutdown()
		return true, true, nil
	default:
		return nil, false, fmt.Errorf("unknown RPC method %q", method)
	}
}
