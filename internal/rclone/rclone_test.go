package rclone

import (
	"reflect"
	"testing"

	"wintp/internal/store"
)

func TestRemoteConfigArgsSkipsInternalKeysAndAddsKnownHosts(t *testing.T) {
	c := &store.Connection{
		Provider: "sftp",
		Config: map[string]string{
			"host":                 "example.com",
			store.ConfigKeyPassEnv: "PASS",
		},
	}

	got := RemoteConfigArgs(c, "known_hosts")
	want := []string{"host", "example.com", "known_hosts_file", "known_hosts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RemoteConfigArgs() = %#v, want %#v", got, want)
	}
}

func TestParseAuthorizeTokenMarkers(t *testing.T) {
	got, err := ParseAuthorizeToken("noise\nPaste the following into your remote machine --->\n{\"token\":\"x\"}\n<---End paste")
	if err != nil {
		t.Fatalf("ParseAuthorizeToken() error = %v", err)
	}
	if got != `{"token":"x"}` {
		t.Fatalf("token = %q", got)
	}
}

func TestParseAuthorizeTokenJSONLine(t *testing.T) {
	got, err := ParseAuthorizeToken("notice\n{\"access_token\":\"x\"}\n")
	if err != nil {
		t.Fatalf("ParseAuthorizeToken() error = %v", err)
	}
	if got != `{"access_token":"x"}` {
		t.Fatalf("token = %q", got)
	}
}


