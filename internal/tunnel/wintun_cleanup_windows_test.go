//go:build windows

package tunnel

import (
	"fmt"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// Regression: switching profiles reported
// WintunOpenAdapter("Nexus"): Element not found (code=1168) as a disconnect
// failure, although the AWG service had already removed its adapter.
func TestWintunAdapterMissingTreatsNotFoundAsRemoved(t *testing.T) {
	for _, code := range []syscall.Errno{
		windows.ERROR_NOT_FOUND,        // 1168
		windows.ERROR_FILE_NOT_FOUND,   // 2
		windows.ERROR_PATH_NOT_FOUND,   // 3
		windows.ERROR_OBJECT_NOT_FOUND, // 4312
	} {
		if !wintunAdapterMissing(fmt.Errorf("WintunOpenAdapter: %w", code)) {
			t.Fatalf("code %d must mean the adapter is already gone", uint32(code))
		}
	}
	if wintunAdapterMissing(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("access denied must still be reported as a real cleanup failure")
	}
}
