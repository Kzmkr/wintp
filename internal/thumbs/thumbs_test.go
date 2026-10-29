package thumbs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func touch(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestScanFindsMediaNewestFirstAndSkipsAppData(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	touch(t, filepath.Join(root, "DCIM", "Camera", "old.jpg"), now.Add(-48*time.Hour))
	touch(t, filepath.Join(root, "DCIM", "Camera", "new.MP4"), now)
	touch(t, filepath.Join(root, "Pictures", "Screenshots", "mid.png"), now.Add(-time.Hour))
	touch(t, filepath.Join(root, "Download", "doc.pdf"), now)
	touch(t, filepath.Join(root, "Android", "media", "app.jpg"), now)
	touch(t, filepath.Join(root, ".thumbnails", "t.jpg"), now)

	got := scan(context.Background(), root)
	want := []string{
		filepath.Join(root, "DCIM", "Camera", "new.MP4"),
		filepath.Join(root, "Pictures", "Screenshots", "mid.png"),
		filepath.Join(root, "DCIM", "Camera", "old.jpg"),
	}
	if len(got) != len(want) {
		t.Fatalf("scan = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scan = %q, want %q", got, want)
		}
	}
}

func TestPrewarmerBuildsOnlyMissingThumbnails(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	for _, n := range []string{"a.jpg", "b.jpg", "c.mp4", "broken.heic"} {
		touch(t, filepath.Join(root, "DCIM", n), now)
	}

	var mu sync.Mutex
	generated := map[string]bool{}
	cached := map[string]bool{filepath.Join(root, "DCIM", "a.jpg"): true}
	p := &Prewarmer{
		size:    256,
		withCOM: func(fn func()) { fn() },
		has: func(path string, size int) bool {
			mu.Lock()
			defer mu.Unlock()
			return cached[path]
		},
		generate: func(path string, size int) error {
			if size != 256 {
				t.Errorf("size = %d", size)
			}
			if filepath.Base(path) == "broken.heic" {
				return errors.New("no codec")
			}
			mu.Lock()
			defer mu.Unlock()
			generated[path] = true
			return nil
		},
	}

	p.Start(root)
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := p.Status()
		if st.Scanned && !st.Running {
			if st.Total != 4 || st.Ready != 3 || st.Failed != 1 {
				t.Fatalf("status = %+v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pass did not finish: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Stop()

	mu.Lock()
	defer mu.Unlock()
	if generated[filepath.Join(root, "DCIM", "a.jpg")] {
		t.Error("regenerated a thumbnail that was already cached")
	}
	if !generated[filepath.Join(root, "DCIM", "b.jpg")] || !generated[filepath.Join(root, "DCIM", "c.mp4")] {
		t.Errorf("missing thumbnails not built: %v", generated)
	}
	if st := p.Status(); st.Running || st.Total != 0 {
		t.Errorf("status after Stop = %+v", st)
	}
}

func TestStopInterruptsAPass(t *testing.T) {
	root := t.TempDir()
	for i := range 50 {
		touch(t, filepath.Join(root, "DCIM", string(rune('a'+i%26))+string(rune('a'+i/26))+".jpg"), time.Now())
	}
	started := make(chan struct{}, 1)
	p := &Prewarmer{
		size:    256,
		withCOM: func(fn func()) { fn() },
		has:     func(string, int) bool { return false },
		generate: func(string, int) error {
			select {
			case started <- struct{}{}:
			default:
			}
			time.Sleep(20 * time.Millisecond)
			return nil
		},
	}
	p.Start(root)
	<-started
	stopped := make(chan struct{})
	go func() { p.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return")
	}
	if st := p.Status(); st.Ready >= 50 {
		t.Errorf("pass was not interrupted: %+v", st)
	}
}
