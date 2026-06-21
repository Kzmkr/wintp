package mount

import (
	"strings"
	"testing"

	"wintp/internal/store"
)

func TestMountArgsIncludesCacheFlags(t *testing.T) {
	c := &store.Connection{
		ID:            "abc",
		Name:          "Docs",
		DriveLetter:   "X",
		RemotePath:    "/files",
		CacheMode:     store.CacheModeFull,
		CacheMaxSize:  "10G",
		CacheMaxAge:   "1h",
		DirCacheTime:  "5m",
		BufferSize:    "16M",
		ReadChunkSize: "128M",
	}

	got := strings.Join(MountArgs(c, "rclone.conf"), " ")
	for _, want := range []string{
		"mount wintp-abc:/files X:",
		"--config rclone.conf",
		"--vfs-cache-mode full",
		"--vfs-cache-max-size 10G",
		"--vfs-cache-max-age 1h",
		"--dir-cache-time 5m",
		"--buffer-size 16M",
		"--vfs-read-chunk-size 128M",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MountArgs missing %q in %q", want, got)
		}
	}
}

func TestBuildEnvForNameSanitizesRemoteName(t *testing.T) {
	// rclone expects non-alphanumeric characters in the remote name to be
	// replaced with underscores.
	env := buildEnvForName("rclone", "wintp-abc", "PASS", "secret")
	found := false
	for _, item := range env {
		if item == "RCLONE_CONFIG_WINTP_ABC_PASS=secret" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected RCLONE_CONFIG_WINTP_ABC_PASS=secret in env, got %v", env)
	}
}

func TestBuildEnvForNameOmitsEnvVarWithoutPassword(t *testing.T) {
	env := buildEnvForName("rclone", "wintp-abc", "PASS", "")
	for _, item := range env {
		if strings.HasPrefix(item, "RCLONE_CONFIG_") {
			t.Fatalf("unexpected password env var without password: %q", item)
		}
	}
}

func TestReadinessFromLogLine(t *testing.T) {
	err, ok := readinessFromLogLine(`{"level":"info","msg":"Filesystem is serving"}`)
	if !ok || err != nil {
		t.Fatalf("ready line = (%v, %v), want ready nil", err, ok)
	}

	err, ok = readinessFromLogLine(`{"level":"error","msg":"bad credentials"}`)
	if !ok || err == nil {
		t.Fatalf("error line = (%v, %v), want error", err, ok)
	}

	err, ok = readinessFromLogLine(`plain failed line`)
	if !ok || err == nil {
		t.Fatalf("plain failed line = (%v, %v), want error", err, ok)
	}
}
