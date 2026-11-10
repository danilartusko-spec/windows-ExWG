//go:build windows

package diagnostics

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"nexusclient/internal/debuglog"
	"nexusclient/internal/windowsroutes"
)

func SystemSnapshot() string {
	var out strings.Builder
	version := windows.RtlGetVersion()
	fmt.Fprintf(&out, "Nexus=%s time=%s OS=%s arch=%s Go=%s\nWindows version=%+v\nelevated=%v uptime=%s\n", debuglog.Version, time.Now().Format(time.RFC3339Nano), runtime.GOOS, runtime.GOARCH, runtime.Version(), version, windows.GetCurrentProcessToken().IsElevated(), windows.DurationSinceBoot())
	if info, ok := debug.ReadBuildInfo(); ok {
		fmt.Fprintf(&out, "module=%s version=%s\n", info.Main.Path, info.Main.Version)
		for _, dep := range info.Deps {
			fmt.Fprintf(&out, "dependency=%s version=%s\n", dep.Path, dep.Version)
		}
	}
	out.WriteString(windowsroutes.DiagnosticSnapshot())
	return out.String()
}
