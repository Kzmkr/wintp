package util

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"wintp/internal/proc"

	"golang.org/x/sys/windows/registry"
)

var rcloneVersionRE = regexp.MustCompile(`v?\d+\.\d+\.\d+`)

// FindRclone returns the path to the rclone binary.
// It first looks for rclone.exe next to the current executable,
// then falls back to PATH.
func FindRclone() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		bundled := filepath.Join(filepath.Dir(exe), "rclone.exe")
		if _, err := os.Stat(bundled); err == nil {
			return bundled, nil
		}
	}
	path, err := proc.LookPath("rclone")
	if err != nil {
		return "", fmt.Errorf("rclone not found: install rclone and ensure it is on PATH, or place rclone.exe next to wintp.exe")
	}
	return path, nil
}

// RcloneVersion returns the rclone version string.
func RcloneVersion(rcloneBin string) string {
	out, err := proc.Command(rcloneBin, "version", "--check=false").Output()
	if err != nil {
		return "unknown"
	}
	if m := rcloneVersionRE.FindString(string(out)); m != "" {
		return m
	}
	return "unknown"
}

// CheckWinFsp returns an error if WinFsp does not appear to be installed.
func CheckWinFsp() error {
	// WinFsp installs to a known registry path.
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\WOW6432Node\WinFsp`, registry.QUERY_VALUE)
	if err == nil {
		k.Close()
		return nil
	}
	k2, err2 := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\WinFsp`, registry.QUERY_VALUE)
	if err2 == nil {
		k2.Close()
		return nil
	}
	return fmt.Errorf("WinFsp does not appear to be installed. rclone mount requires WinFsp. Download from https://winfsp.dev")
}
