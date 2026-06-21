package mount

import (
	"log"
	"sync"
	"time"

	"wintp/internal/store"
)

type Supervisor struct {
	mu      sync.Mutex
	mounts  map[string]*Mount // keyed by connection ID
	stopChs map[string]chan struct{}

	rcloneBin      string
	rcloneConf     string
	knownHostsFile string

	// OnStatusChange is called when a mount's state changes.
	OnStatusChange func(connID string, s Status)
}

func NewSupervisor(rcloneBin, rcloneConf, knownHostsFile string) *Supervisor {
	return &Supervisor{
		mounts:         make(map[string]*Mount),
		stopChs:        make(map[string]chan struct{}),
		rcloneBin:      rcloneBin,
		rcloneConf:     rcloneConf,
		knownHostsFile: knownHostsFile,
	}
}

func (sv *Supervisor) MountStatus(id string) Status {
	sv.mu.Lock()
	m, ok := sv.mounts[id]
	sv.mu.Unlock()
	if !ok {
		return Status{State: StateIdle}
	}
	return m.Status()
}

// Mount starts a managed mount for the given connection.
func (sv *Supervisor) Mount(conn *store.Connection) error {
	sv.mu.Lock()
	if m, exists := sv.mounts[conn.ID]; exists {
		state := m.State()
		if state == StateMounted || state == StateMounting || state == StateUnmounting {
			sv.mu.Unlock()
			return nil
		}
		sv.mu.Unlock()
		err := m.Start()
		sv.cleanupIfOrphaned(conn.ID, m)
		return err
	}

	m := New(conn, sv.rcloneBin, sv.rcloneConf, sv.knownHostsFile)
	sv.mounts[conn.ID] = m
	stopCh := make(chan struct{})
	sv.stopChs[conn.ID] = stopCh
	sv.mu.Unlock()

	go sv.watch(conn, m, stopCh)
	err := m.Start()
	sv.cleanupIfOrphaned(conn.ID, m)
	return err
}

// cleanupIfOrphaned stops a mount if it was unmounted while it was starting.
func (sv *Supervisor) cleanupIfOrphaned(id string, m *Mount) {
	sv.mu.Lock()
	_, tracked := sv.mounts[id]
	sv.mu.Unlock()
	if !tracked {
		m.Stop()
	}
}

// Unmount stops a managed mount.
func (sv *Supervisor) Unmount(id string) {
	sv.mu.Lock()
	m, ok := sv.mounts[id]
	stopCh, hasStop := sv.stopChs[id]
	if ok {
		delete(sv.mounts, id)
		delete(sv.stopChs, id)
	}
	sv.mu.Unlock()

	if hasStop {
		close(stopCh)
	}
	if ok {
		m.Stop()
	}
}

// UnmountAll cleanly stops all active mounts - call on app shutdown.
func (sv *Supervisor) UnmountAll() {
	sv.mu.Lock()
	ids := make([]string, 0, len(sv.mounts))
	for id := range sv.mounts {
		ids = append(ids, id)
	}
	sv.mu.Unlock()

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sv.Unmount(id)
		}(id)
	}
	wg.Wait()
}

func (sv *Supervisor) watch(conn *store.Connection, m *Mount, stopCh <-chan struct{}) {
	backoff := 2 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		select {
		case status, ok := <-m.StatusCh:
			if !ok {
				return
			}
			if sv.OnStatusChange != nil {
				sv.OnStatusChange(conn.ID, status)
			}
			if status.State == StateError {
				log.Printf("[%s] mount error: %s", conn.Name, status.Message)
				if conn.AutoReconnect {
					log.Printf("[%s] reconnecting in %s", conn.Name, backoff)
					select {
					case <-time.After(backoff):
					case <-stopCh:
						return
					}
					backoff = min(backoff*2, maxBackoff)
					if err := m.Start(); err != nil {
						log.Printf("[%s] reconnect failed: %v", conn.Name, err)
					} else {
						backoff = 2 * time.Second
					}
					// If unmount happened while we were reconnecting, stop the mount.
					select {
					case <-stopCh:
						m.Stop()
						return
					default:
					}
				}
			}
		case <-stopCh:
			return
		}
	}
}
