package singleinstance

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestAcquireRejectsSecondInstance(t *testing.T) {
	name := "wintp-test-" + uuid.NewString()

	first, err := Acquire(name)
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	defer first.Release()

	second, err := Acquire(name)
	if !errors.Is(err, ErrAlreadyRunning) {
		if second != nil {
			_ = second.Release()
		}
		t.Fatalf("second Acquire() error = %v, want %v", err, ErrAlreadyRunning)
	}
	if second != nil {
		t.Fatalf("second Acquire() lock = %#v, want nil", second)
	}
}
