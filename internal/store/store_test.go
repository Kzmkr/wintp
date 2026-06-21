package store

import "testing"

func TestRemoteName(t *testing.T) {
	got := RemoteName("abc")
	if got != "wintp-abc" {
		t.Fatalf("RemoteName() = %q", got)
	}
}

func TestRcloneTypeFor(t *testing.T) {
	tests := map[string]string{
		"ftp":  "ftp",
		"ftps": "ftp",
		"sftp": "sftp",
	}
	for in, want := range tests {
		if got := RcloneTypeFor(in); got != want {
			t.Fatalf("RcloneTypeFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultPort(t *testing.T) {
	tests := map[string]int{
		"ftp":    21,
		"ftps":   21,
		"sftp":   22,
		"smb":    445,
		"webdav": 443,
	}
	for provider, want := range tests {
		if got := DefaultPort(provider); got != want {
			t.Fatalf("DefaultPort(%q) = %d, want %d", provider, got, want)
		}
	}
}

func TestMigrateV1FTPS(t *testing.T) {
	c := &Connection{
		LegacyProtocol: "ftps",
		LegacyHost:     "example.com",
		LegacyPort:     990,
		LegacyUsername: "demo",
	}

	migrateV1(c)

	if c.Provider != "ftp" {
		t.Fatalf("Provider = %q", c.Provider)
	}
	if c.Config["tls"] != "true" {
		t.Fatalf("tls config missing")
	}
	if c.Config[ConfigKeyUIType] != "ftps" {
		t.Fatalf("ui type = %q", c.Config[ConfigKeyUIType])
	}
	if c.Config[ConfigKeyPassEnv] != "PASS" {
		t.Fatalf("pass env = %q", c.Config[ConfigKeyPassEnv])
	}
	if c.LegacyProtocol != "" || c.LegacyHost != "" || c.LegacyPort != 0 || c.LegacyUsername != "" {
		t.Fatalf("legacy fields were not cleared: %+v", c)
	}
}
