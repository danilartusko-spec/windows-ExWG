//go:build windows

package updater

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// UpdatesDirectory returns %LOCALAPPDATA%\NexusClient\Updates.
func UpdatesDirectory() (string, error) {
	localAppData, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil || strings.TrimSpace(localAppData) == "" {
		localAppData = strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	}
	if localAppData == "" {
		if err != nil {
			return "", fmt.Errorf("определение папки LocalAppData: %w", err)
		}
		return "", errors.New("Windows не сообщила папку LocalAppData")
	}
	return filepath.Join(localAppData, "NexusClient", "Updates"), nil
}

// LaunchInstaller starts the Inno Setup installer detached in silent mode.
func LaunchInstaller(installerPath string) error {
	cleaned := strings.TrimSpace(installerPath)
	if cleaned == "" {
		return errors.New("путь к установщику пуст")
	}
	cmd := exec.Command(
		cleaned,
		"/VERYSILENT",
		"/SUPPRESSMSGBOXES",
		"/NORESTART",
		"/CLOSEAPPLICATIONS",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск установщика обновления: %w", err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}
