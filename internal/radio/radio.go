// Package radio reads and switches the PC's Bluetooth radio using the Windows
// Runtime Radio API (the same one behind the Settings toggle). It runs
// PowerShell because the API is WinRT and wintp has no WinRT bindings; the
// script contains only constants, never caller-supplied text.
package radio

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"wintp/internal/proc"
)

// Bluetooth states returned by Bluetooth.
const (
	StateOn   = "on"
	StateOff  = "off"
	StateNone = "none" // no Bluetooth adapter
)

const script = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asTask = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
function Await($op, $type) { $t = $asTask.MakeGenericMethod($type).Invoke($null, @($op)); $t.Wait(-1) | Out-Null; $t.Result }
[void][Windows.Devices.Radios.Radio,Windows.System.Devices,ContentType=WindowsRuntime]
[void][Windows.Devices.Radios.RadioAccessStatus,Windows.System.Devices,ContentType=WindowsRuntime]
$access = Await ([Windows.Devices.Radios.Radio]::RequestAccessAsync()) ([Windows.Devices.Radios.RadioAccessStatus])
if ($access -ne 'Allowed') { 'ERR=access ' + $access; exit 1 }
$radios = Await ([Windows.Devices.Radios.Radio]::GetRadiosAsync()) ([System.Collections.Generic.IReadOnlyList[Windows.Devices.Radios.Radio]])
$bt = $radios | Where-Object { $_.Kind -eq 'Bluetooth' } | Select-Object -First 1
if (-not $bt) { 'BT=none'; exit 0 }
%s
'BT=' + $bt.State
`

const setSnippet = `
$res = Await ($bt.SetStateAsync('%s')) ([Windows.Devices.Radios.RadioAccessStatus])
if ($res -ne 'Allowed') { 'ERR=set ' + $res; exit 1 }
`

// Bluetooth returns the radio state. If set is non-nil it first switches the
// radio on (true) or off (false).
func Bluetooth(ctx context.Context, set *bool) (string, error) {
	snippet := ""
	if set != nil {
		target := "Off"
		if *set {
			target = "On"
		}
		snippet = fmt.Sprintf(setSnippet, target)
	}
	src := fmt.Sprintf(script, snippet)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := proc.CommandContext(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodePS(src))
	out, err := cmd.CombinedOutput()
	return parse(string(out), err)
}

func parse(out string, runErr error) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "BT="); ok {
			switch strings.ToLower(v) {
			case "on":
				return StateOn, nil
			case "off", "disabled":
				return StateOff, nil
			case "none":
				return StateNone, nil
			}
			return "", fmt.Errorf("unexpected radio state %q", v)
		}
		if v, ok := strings.CutPrefix(line, "ERR="); ok {
			return "", errors.New("bluetooth: " + v)
		}
	}
	if runErr != nil {
		return "", fmt.Errorf("bluetooth: %w", runErr)
	}
	return "", errors.New("bluetooth: no result")
}

// encodePS encodes a script for powershell -EncodedCommand (UTF-16LE base64),
// which avoids all command-line quoting problems.
func encodePS(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}
