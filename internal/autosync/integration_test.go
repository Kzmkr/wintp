//go:build rcloneit

package autosync

import (
	"os"
	"path/filepath"
	"testing"

	"wintp/internal/store"
)

// Runs real rclone against a "local" remote: go test -tags rcloneit
func TestRealRclone(t *testing.T) {
	bin, _ := filepath.Abs("../../rclone.exe")
	dir := t.TempDir()
	conf := filepath.Join(dir, "rclone.conf")
	os.WriteFile(conf, []byte("[wintp-c1]\ntype = local\n\n"), 0600)
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	os.MkdirAll(local, 0755)
	os.MkdirAll(remote, 0755)
	os.WriteFile(filepath.Join(local, "a.txt"), []byte("A"), 0644)

	job := &store.SyncJob{ID: "j1", ConnectionID: "c1", LocalPath: local, RemotePath: filepath.ToSlash(remote),
		Direction: store.SyncUpload, Enabled: true, IntervalMinutes: 5}
	fs := &fakeStore{jobs: map[string]*store.SyncJob{"j1": job}, conns: map[string]*store.Connection{"c1": {ID: "c1", Config: map[string]string{}}}}
	m := New(bin, conf, fs)
	defer m.Stop()

	check := func(stage string) {
		t.Helper()
		if st := m.Status("j1"); st.State != StateOK {
			t.Fatalf("%s: %+v", stage, st)
		}
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	// Upload (copy).
	m.runJob("j1")
	check("upload")
	if !exists(filepath.Join(remote, "a.txt")) {
		t.Fatal("upload did not copy a.txt")
	}

	// Mirror deletes at destination.
	os.WriteFile(filepath.Join(remote, "extra.txt"), []byte("x"), 0644)
	fs.jobs["j1"].Mirror = true
	m.runJob("j1")
	check("mirror")
	if exists(filepath.Join(remote, "extra.txt")) {
		t.Fatal("mirror did not delete extra.txt")
	}

	// Two-way: first run resyncs, then changes on both sides propagate.
	fs.jobs["j1"].Direction = store.SyncTwoWay
	fs.jobs["j1"].Mirror = false
	os.WriteFile(filepath.Join(remote, "b.txt"), []byte("B"), 0644)
	m.runJob("j1")
	check("bisync resync")
	if !fs.jobs["j1"].BisyncReady || !exists(filepath.Join(local, "b.txt")) {
		t.Fatal("resync did not merge b.txt")
	}
	os.WriteFile(filepath.Join(local, "c.txt"), []byte("C"), 0644)
	os.Remove(filepath.Join(remote, "a.txt"))
	m.runJob("j1")
	check("bisync incremental")
	if !exists(filepath.Join(remote, "c.txt")) || exists(filepath.Join(local, "a.txt")) {
		t.Fatal("incremental bisync did not propagate")
	}
}
