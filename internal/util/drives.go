package util

import (
	"golang.org/x/sys/windows"
)

// FreeDriveLetters returns drive letters (e.g. "D", "E") not currently in use.
func FreeDriveLetters() []string {
	mask, _ := windows.GetLogicalDrives()
	var free []string
	for i := 0; i < 26; i++ {
		letter := string(rune('A' + i))
		// Skip A and B (floppy convention), C (usually system).
		if letter == "A" || letter == "B" || letter == "C" {
			continue
		}
		if mask&(1<<uint(i)) == 0 {
			free = append(free, letter)
		}
	}
	return free
}

// IsDriveInUse reports whether the given drive letter (e.g. "D") is occupied.
func IsDriveInUse(letter string) bool {
	if len(letter) == 0 {
		return false
	}
	mask, _ := windows.GetLogicalDrives()
	idx := int(letter[0] - 'A')
	if idx < 0 || idx > 25 {
		return false
	}
	return mask&(1<<uint(idx)) != 0
}
