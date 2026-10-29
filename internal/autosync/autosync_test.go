package autosync

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"wintp/internal/store"
)

type fakeStore struct {
	mu    sync.Mutex
	jobs  map[string]*store.SyncJob
	conns map[string]*store.Connection
}

func (f *fakeStore) SyncJobs() []*store.SyncJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*store.SyncJob
	for _, j := range f.jobs {
		cp := *j
		out = append(out, &cp)
	}
	return out
}

func (f *fakeStore) SyncJob(id string) (*store.SyncJob, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

func (f *fakeStore) SaveSyncJob(j *store.SyncJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *j
	f.jobs[j.ID] = &cp
	return nil
}

func (f *fakeStore) Get(id string) (*store.Connection, bool) {
	c, ok := f.conns[id]
	return c, ok
}

func newTestManager(t *testing.T, job *store.SyncJob, run runFunc) (*Manager, *fakeStore) {
	t.Helper()
	fs := &fakeStore{
		jobs:  map[string]*store.SyncJob{job.ID: job},
		conns: map[string]*store.Connection{"c1": {ID: "c1"}},
	}
	m := New("rclone", "rclone.conf", fs)
	m.run = run
	t.Cleanup(m.Stop)
	return m, fs
}

func TestArgs(t *testing.T) {
	conn := &store.Connection{ID: "c1"}
	base := store.SyncJob{LocalPath: `C:\Docs`, RemotePath: "/backup"}
	tail := []string{"--config", "r.conf", "--log-level", "NOTICE"}

	cases := []struct {
		name   string
		dir    store.SyncDirection
		mirror bool
		resync bool
		want   []string
	}{
		{"upload copy", store.SyncUpload, false, false,
			append([]string{"copy", `C:\Docs`, "wintp-c1:/backup"}, tail...)},
		{"upload mirror", store.SyncUpload, true, false,
			append([]string{"sync", `C:\Docs`, "wintp-c1:/backup"}, tail...)},
		{"download", store.SyncDownload, false, false,
			append([]string{"copy", "wintp-c1:/backup", `C:\Docs`}, tail...)},
	}
	for _, c := range cases {
		j := base
		j.Direction, j.Mirror = c.dir, c.mirror
		if got := Args(&j, conn, "r.conf", c.resync); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	j := base
	j.Direction = store.SyncTwoWay
	got := Args(&j, conn, "r.conf", true)
	if got[0] != "bisync" || got[1] != `C:\Docs` || got[2] != "wintp-c1:/backup" {
		t.Errorf("bisync args: %q", got)
	}
	if !contains(got, "--resync") {
		t.Errorf("first bisync run should --resync: %q", got)
	}
	if contains(Args(&j, conn, "r.conf", false), "--resync") {
		t.Error("later bisync runs must not --resync")
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func waitIdle(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for m.IsRunning() {
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBisyncResyncsOnceThenIncremental(t *testing.T) {
	var calls [][]string
	var mu sync.Mutex
	job := &store.SyncJob{ID: "j1", ConnectionID: "c1", LocalPath: t.TempDir(),
		Direction: store.SyncTwoWay, Enabled: true, IntervalMinutes: 5}
	m, fs := newTestManager(t, job, func(_ context.Context, _ *store.Connection, args []string) (string, error) {
		mu.Lock()
		calls = append(calls, args)
		mu.Unlock()
		return "", nil
	})

	m.runJob("j1")
	if j, _ := fs.SyncJob("j1"); !j.BisyncReady {
		t.Fatal("BisyncReady not set after first successful run")
	}
	m.runJob("j1")
	if !contains(calls[0], "--resync") || contains(calls[1], "--resync") {
		t.Errorf("want resync only on first run, got %q", calls)
	}
	if st := m.Status("j1"); st.State != StateOK || st.NextRun.IsZero() {
		t.Errorf("status = %+v", st)
	}
}

func TestFailedRunReportsLastLine(t *testing.T) {
	job := &store.SyncJob{ID: "j1", ConnectionID: "c1", LocalPath: t.TempDir(),
		Direction: store.SyncUpload, Enabled: true, IntervalMinutes: 5}
	m, _ := newTestManager(t, job, func(context.Context, *store.Connection, []string) (string, error) {
		return "2026/10/29 20:01:02 NOTICE : x\n2026/10/29 20:01:02 ERROR : Attempt 1/3 failed: permission denied\n", errors.New("exit 1")
	})
	m.runJob("j1")
	st := m.Status("j1")
	if st.State != StateError || st.Message != "Attempt 1/3 failed: permission denied" {
		t.Errorf("status = %+v", st)
	}
}

func TestTickRunsDueJobsOnly(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	job := &store.SyncJob{ID: "j1", ConnectionID: "c1", LocalPath: t.TempDir(),
		Direction: store.SyncDownload, Enabled: true, IntervalMinutes: 10}
	m, fs := newTestManager(t, job, func(context.Context, *store.Connection, []string) (string, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		return "", nil
	})

	m.tick(time.Now()) // never run: due
	waitIdle(t, m)
	m.tick(time.Now()) // just ran: not due
	waitIdle(t, m)
	m.tick(time.Now().Add(11 * time.Minute)) // interval passed: due
	waitIdle(t, m)

	fs.mu.Lock()
	fs.jobs["j1"].Enabled = false
	fs.mu.Unlock()
	m.tick(time.Now().Add(time.Hour)) // disabled: never
	waitIdle(t, m)

	mu.Lock()
	defer mu.Unlock()
	if runs != 2 {
		t.Errorf("runs = %d, want 2", runs)
	}
}

func TestMissingLocalFolderForUpload(t *testing.T) {
	job := &store.SyncJob{ID: "j1", ConnectionID: "c1", LocalPath: `Z:\does\not\exist`,
		Direction: store.SyncUpload, Enabled: true}
	m, _ := newTestManager(t, job, func(context.Context, *store.Connection, []string) (string, error) {
		t.Error("rclone should not run")
		return "", nil
	})
	m.runJob("j1")
	if st := m.Status("j1"); st.State != StateError {
		t.Errorf("status = %+v", st)
	}
}
