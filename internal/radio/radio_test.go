package radio

import (
	"context"
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		out     string
		want    string
		wantErr bool
	}{
		{"BT=On\r\n", StateOn, false},
		{"junk\r\nBT=Off\r\n", StateOff, false},
		{"BT=Disabled\n", StateOff, false},
		{"BT=none\n", StateNone, false},
		{"ERR=set DeniedByUser\n", "", true},
		{"BT=weird\n", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := parse(c.out, nil)
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("parse(%q) = %q, %v", c.out, got, err)
		}
	}
	if _, err := parse("", errors.New("exit 1")); err == nil {
		t.Error("expected error when process failed with no output")
	}
}

// Integration check against the real radio. Setting the radio to the state it
// is already in is a no-op, so this is safe to run on a dev machine.
func TestBluetoothRealRadio(t *testing.T) {
	state, err := Bluetooth(context.Background(), nil)
	if err != nil {
		t.Skipf("radio API unavailable: %v", err)
	}
	t.Logf("bluetooth state: %s", state)
	if state == StateNone {
		t.Skip("no bluetooth adapter")
	}
	same := state == StateOn
	got, err := Bluetooth(context.Background(), &same)
	if err != nil || got != state {
		t.Fatalf("no-op set: got %q, %v; want %q", got, err, state)
	}
}
