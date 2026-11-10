//go:build windows

package debuglog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

// Preserve exact bytes as hex whenever stdout is not UTF-8. OEM decoding is
// only a readable companion, never a reason to discard the original output.
func DecodeWindowsOutput(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	wide := make([]uint16, len(data))
	count, err := windows.MultiByteToWideChar(1 /* CP_OEMCP */, 0, &data[0], int32(len(data)), &wide[0], int32(len(wide)))
	if err != nil {
		return fmt.Sprintf("decode(CP_OEMCP): %s\nstdout.raw.hex=%x", ErrorDetail(err), data)
	}
	return fmt.Sprintf("stdout.decoded(CP_OEMCP)=%s\nstdout.raw.hex=%x", windows.UTF16ToString(wide[:count]), data)
}

func Directory(service bool) (string, error) {
	folder := windows.FOLDERID_LocalAppData
	if service {
		folder = windows.FOLDERID_ProgramData
	}
	base, err := windows.KnownFolderPath(folder, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", err
	}
	role := "UI"
	if service {
		role = "Tunnel"
	}
	return filepath.Join(base, "NexusClient", "Logs", role), nil
}

func Init(service bool) (*Journal, error) {
	dir, err := Directory(service)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if service {
		// ProgramData is shared. Raw tunnel logs are local to SYSTEM/admins.
		sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
		if err != nil {
			return nil, err
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			return nil, err
		}
		if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			return nil, err
		}
	}
	j, err := Open(dir, FileBytes, RetainedFiles)
	if err == nil {
		SetDefault(j)
	}
	return j, err
}

func SetEnabled(enable bool, service bool) error {
	if !enable {
		if j := Default(); j != nil {
			SetDefault(nil)
			_ = j.Close()
		}
		return nil
	}
	if Default() != nil {
		return nil
	}
	if _, err := Init(service); err != nil {
		return err
	}
	Event("wg.diagnostics", "Режим диагностики включён")
	return nil
}

func Combined(tailBytes int) string {
	var out strings.Builder
	out.WriteString("Ex WG — ORIGINAL LOCAL DEBUG LOG\nContains server/network addresses. Review before sharing.\nRetention: 8 × 2 MiB per role (UI and Tunnel). Oldest files rotate automatically.\n")
	for _, service := range []bool{false, true} {
		dir, err := Directory(service)
		fmt.Fprintf(&out, "\n=== directory=%s ===\n", dir)
		if err != nil {
			out.WriteString(ErrorDetail(err))
			continue
		}
		data, err := Snapshot(dir, tailBytes)
		if err != nil {
			out.WriteString(ErrorDetail(err))
		} else {
			out.WriteString(data)
		}
	}
	if j := Default(); j != nil && j.LastError() != nil {
		out.WriteString("\nLOG WRITE FAILURE:\n" + ErrorDetail(j.LastError()))
	}
	return out.String()
}
