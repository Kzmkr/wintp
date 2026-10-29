//go:build e2e

package thumbs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"wintp/internal/mount"
)

// go test -tags e2e -run E2E ./internal/thumbs  (needs WinFsp, ../../rclone.exe, E2E_MEDIA)
func TestE2EPrewarmSurvivesRemount(t *testing.T) {
	media := os.Getenv("E2E_MEDIA")
	bin, _ := filepath.Abs("../../rclone.exe")
	id := "e2e-" + time.Now().Format("150405")
	mountIt := func() *exec.Cmd {
		args := append([]string{"mount", media, "X:", "--vfs-cache-mode", "off", "--no-console"},
			mount.VolumeIdentityArgs(id)...)
		cmd := exec.Command(bin, args...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 50; i++ {
			if _, err := os.Stat(`X:\`); err == nil {
				return cmd
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("mount did not appear")
		return nil
	}
	unmount := func(cmd *exec.Cmd) { _ = cmd.Process.Kill(); _ = cmd.Wait(); time.Sleep(time.Second) }

	cmd := mountIt()
	p := New()
	p.delay = 0
	start := time.Now()
	p.Start(`X:\`)
	for {
		st := p.Status()
		if st.Scanned && !st.Running {
			t.Logf("first pass: %+v in %v (size %d)", st, time.Since(start).Round(time.Millisecond), p.size)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	p.Stop()
	unmount(cmd)

	cmd = mountIt()
	defer unmount(cmd)
	files := scan(t.Context(), `X:\`)
	missing := 0
	withCOM(func() {
		for _, f := range files {
			if !shellHas(f, p.size) {
				missing++
				t.Logf("not cached after remount: %s", f)
			}
		}
	})
	t.Logf("after remount: %d of %d cached", len(files)-missing, len(files))
	if missing > 0 {
		t.Fail()
	}
}
