//go:build windows

package debuglog

import (
	"strings"
	"testing"
)

func TestOriginalNonUTF8CommandOutputNotDiscarded(t *testing.T) {
	data := []byte{0x8e, 0xe8, 0xa8, 0xa1, 0xaa, 0xa0}
	output := DecodeWindowsOutput(data)
	if !strings.Contains(output, "stdout.raw.hex=8ee8a8a1aaa0") {
		t.Fatalf("original bytes lost: %s", output)
	}
	if got := DecodeWindowsOutput([]byte("исходная ошибка")); got != "исходная ошибка" {
		t.Fatal(got)
	}
}
