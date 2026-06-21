package util

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/windows/registry"
)

const autostartKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const autostartName = "wintp"

// SetAutostart adds or removes the app from Windows startup registry.
func SetAutostart(enabled bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open registry: %w", err)
	}
	defer k.Close()

	if enabled {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("get executable path: %w", err)
		}
		return k.SetStringValue(autostartName, strconv.Quote(exe))
	}
	return k.DeleteValue(autostartName)
}

// AutostartEnabled returns true if the app is registered for Windows startup.
func AutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autostartName)
	return err == nil
}
