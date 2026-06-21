package singleinstance

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

var ErrAlreadyRunning = errors.New("another wintp instance is already running")

type Lock struct {
	handle windows.Handle
}

func Acquire(name string) (*Lock, error) {
	ptr, err := windows.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return nil, fmt.Errorf("mutex name: %w", err)
	}

	handle, err := windows.CreateMutex(nil, false, ptr)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("create mutex: %w", err)
	}

	if errors.Is(windows.GetLastError(), windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(handle)
		return nil, ErrAlreadyRunning
	}

	return &Lock{handle: handle}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	handle := l.handle
	l.handle = 0
	return windows.CloseHandle(handle)
}
