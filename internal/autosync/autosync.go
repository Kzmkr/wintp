// Package autosync runs scheduled rclone copy/sync/bisync jobs that keep a
// local folder and a folder on a connection's remote in step.
//
// Each enabled job runs shortly after startup and then every
// IntervalMinutes. One-way jobs use "rclone copy" (or "rclone sync" when
// Mirror is set, which also deletes). Two-way jobs use "rclone bisync"; the
// very first run of a two-way job uses --resync, which merges both sides
// without deleting anything, and later runs are incremental.
package autosync

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"wintp/internal/mount"
	"wintp/internal/proc"
	"wintp/internal/store"
)

// State is a job's run state.
type State int

const (
	StateIdle    State = iota // never run this session
	StateRunning              // rclone is running
	StateOK                   // last run succeeded
	StateError                // last run failed
)

// Status describes a job's last and next run.
type Status struct {
	State   State
	LastRun time.Time // start of the last finished run
	NextRun time.Time // zero when the job is disabled
	Message string    // error text after a failed run
}

// Store is the subset of *store.Store the manager needs.
type Store interface {
	SyncJobs() []*store.SyncJob
	SyncJob(id string) (*store.SyncJob, bool)
	SaveSyncJob(*store.SyncJob) error
	Get(id string) (*store.Connection, bool)
}

// runFunc runs rclone with args for conn and returns its combined output.
type runFunc func(ctx context.Context, conn *store.Connection, args []string) (string, error)

const (
	startupDelay = 10 * time.Second
	tickEvery    = 30 * time.Second
)

// Manager schedules and runs sync jobs.
type Manager struct {
	bin, conf string
	store     Store
	run       runFunc

	// OnChange is called (from a worker goroutine) whenever a job's status
	// changes.
	OnChange func(jobID string)

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	status  map[string]*Status
	running map[string]context.CancelFunc
}

// New returns a manager that runs rcloneBin with the given rclone.conf.
func New(rcloneBin, rcloneConf string, st Store) *Manager {
	m := &Manager{
		bin:     rcloneBin,
		conf:    rcloneConf,
		store:   st,
		status:  make(map[string]*Status),
		running: make(map[string]context.CancelFunc),
	}
	m.run = m.execRclone
	m.ctx, m.cancel = context.WithCancel(context.Background())
	return m
}

// Start begins the scheduling loop.
func (m *Manager) Start() {
	go func() {
		select {
		case <-time.After(startupDelay):
		case <-m.ctx.Done():
			return
		}
		t := time.NewTicker(tickEvery)
		defer t.Stop()
		for {
			m.tick(time.Now())
			select {
			case <-t.C:
			case <-m.ctx.Done():
				return
			}
		}
	}()
}

// Stop cancels running jobs and the scheduler.
func (m *Manager) Stop() { m.cancel() }

// tick starts every enabled job that is due.
func (m *Manager) tick(now time.Time) {
	for _, j := range m.store.SyncJobs() {
		if !j.Enabled {
			continue
		}
		m.mu.Lock()
		st := m.status[j.ID]
		m.mu.Unlock()
		if st == nil || st.LastRun.IsZero() || !now.Before(st.LastRun.Add(interval(j))) {
			m.RunNow(j.ID)
		}
	}
}

func interval(j *store.SyncJob) time.Duration {
	if j.IntervalMinutes < 1 {
		return time.Minute
	}
	return time.Duration(j.IntervalMinutes) * time.Minute
}

// RunNow starts a job immediately unless it is already running.
func (m *Manager) RunNow(id string) {
	if ctx, cancel, ok := m.claim(id); ok {
		go m.execute(ctx, cancel, id)
	}
}

// runJob runs a job synchronously unless it is already running.
func (m *Manager) runJob(id string) {
	if ctx, cancel, ok := m.claim(id); ok {
		m.execute(ctx, cancel, id)
	}
}

// Cancel stops a running job (e.g. after it was edited) and clears its
// status so an enabled job runs again on the next tick.
func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	if cancel, ok := m.running[id]; ok {
		cancel()
	}
	delete(m.status, id)
	m.mu.Unlock()
	m.changed(id)
}

// IsRunning reports whether any job is currently running.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.running) > 0
}

// Status returns the job's current status.
func (m *Manager) Status(id string) Status {
	m.mu.Lock()
	var st Status
	if s := m.status[id]; s != nil {
		st = *s
	}
	m.mu.Unlock()
	if j, ok := m.store.SyncJob(id); ok && j.Enabled && st.State != StateRunning {
		if st.LastRun.IsZero() {
			st.NextRun = time.Now()
		} else {
			st.NextRun = st.LastRun.Add(interval(j))
		}
	}
	return st
}

func (m *Manager) changed(id string) {
	if m.OnChange != nil {
		m.OnChange(id)
	}
}

// claim marks a job as running, failing if it already is.
func (m *Manager) claim(id string) (context.Context, context.CancelFunc, bool) {
	m.mu.Lock()
	if _, busy := m.running[id]; busy {
		m.mu.Unlock()
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.running[id] = cancel
	prev := m.status[id]
	st := &Status{State: StateRunning}
	if prev != nil {
		st.LastRun = prev.LastRun
	}
	m.status[id] = st
	m.mu.Unlock()
	m.changed(id)
	return ctx, cancel, true
}

func (m *Manager) execute(ctx context.Context, cancel context.CancelFunc, id string) {
	defer cancel()
	started := time.Now()
	msg, err := m.syncOnce(ctx, id)

	m.mu.Lock()
	delete(m.running, id)
	if ctx.Err() != nil && m.ctx.Err() == nil {
		// Cancelled because the job was edited or deleted: Cancel already
		// reset the status.
		m.mu.Unlock()
		return
	}
	st := &Status{State: StateOK, LastRun: started}
	if err != nil {
		st.State = StateError
		st.Message = msg
		log.Printf("autosync %s: %v: %s", id, err, msg)
	}
	m.status[id] = st
	m.mu.Unlock()
	m.changed(id)
}

// syncOnce runs one pass of the job. On failure it returns a short,
// user-facing message along with the error.
func (m *Manager) syncOnce(ctx context.Context, id string) (string, error) {
	j, ok := m.store.SyncJob(id)
	if !ok {
		return "Job no longer exists.", errors.New("job not found")
	}
	conn, ok := m.store.Get(j.ConnectionID)
	if !ok {
		return "Its connection was removed.", errors.New("connection not found")
	}
	if strings.TrimSpace(j.LocalPath) == "" {
		return "No local folder set.", errors.New("no local path")
	}
	if j.Direction == store.SyncUpload {
		if fi, err := os.Stat(j.LocalPath); err != nil || !fi.IsDir() {
			return "Local folder not found: " + j.LocalPath, fmt.Errorf("stat local: %v", err)
		}
	} else if err := os.MkdirAll(j.LocalPath, 0o755); err != nil {
		return "Cannot create local folder: " + err.Error(), err
	}

	resync := j.Direction == store.SyncTwoWay && !j.BisyncReady
	out, err := m.run(ctx, conn, Args(j, conn, m.conf, resync))
	if err != nil {
		if j.Direction == store.SyncTwoWay && j.BisyncReady && strings.Contains(out, "--resync") {
			// bisync lost its state (e.g. listings deleted); merge again next run.
			m.setBisyncReady(j, false)
		}
		return lastLine(out, err), err
	}
	if resync {
		m.setBisyncReady(j, true)
	}
	return "", nil
}

// setBisyncReady persists the flag unless the job was edited meanwhile.
func (m *Manager) setBisyncReady(ran *store.SyncJob, ready bool) {
	cur, ok := m.store.SyncJob(ran.ID)
	if !ok || !cur.SameTarget(ran) {
		return
	}
	cur.BisyncReady = ready
	if err := m.store.SaveSyncJob(cur); err != nil {
		log.Printf("autosync %s: save: %v", ran.ID, err)
	}
}

// Remote returns the rclone path ("wintp-<id>:<path>") for a job.
func Remote(j *store.SyncJob, conn *store.Connection) string {
	return store.RemoteName(conn.ID) + ":" + strings.TrimSpace(j.RemotePath)
}

// Args builds the rclone arguments for one pass of a job.
func Args(j *store.SyncJob, conn *store.Connection, rcloneConf string, resync bool) []string {
	remote := Remote(j, conn)
	var args []string
	switch j.Direction {
	case store.SyncTwoWay:
		args = []string{"bisync", j.LocalPath, remote,
			"--resilient", "--recover", "--max-lock", "2m",
			"--conflict-resolve", "newer", "--conflict-loser", "num",
			"--create-empty-src-dirs"}
		if resync {
			args = append(args, "--resync")
		}
	case store.SyncDownload:
		args = []string{copyCmd(j), remote, j.LocalPath}
	default:
		args = []string{copyCmd(j), j.LocalPath, remote}
	}
	return append(args, "--config", rcloneConf, "--log-level", "NOTICE")
}

func copyCmd(j *store.SyncJob) string {
	if j.Mirror {
		return "sync"
	}
	return "copy"
}

// lastLine picks the most useful line of rclone output for display.
func lastLine(out string, err error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		// Drop rclone's "2026/10/29 20:01:02 ERROR : " prefix.
		if k := strings.Index(l, ": "); k >= 0 && strings.Contains(l[:k], "ERROR") {
			l = strings.TrimSpace(l[k+2:])
		}
		return l
	}
	return err.Error()
}

func (m *Manager) execRclone(ctx context.Context, conn *store.Connection, args []string) (string, error) {
	cmd := proc.CommandContext(ctx, m.bin, args...)
	cmd.Env = mount.Env(m.bin, conn)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return "", err
	}
	if err := proc.AssignToKillJob(cmd); err != nil {
		log.Printf("autosync: rclone won't be auto-killed with wintp: %v", err)
	}
	err := cmd.Wait()
	return buf.String(), err
}
