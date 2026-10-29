package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// SyncDirection is which way an auto-sync job copies files.
type SyncDirection string

const (
	SyncUpload   SyncDirection = "upload"   // local folder -> remote
	SyncDownload SyncDirection = "download" // remote -> local folder
	SyncTwoWay   SyncDirection = "twoway"   // rclone bisync
)

// SyncJob keeps a local folder and a folder on a connection's remote in
// sync, re-run every IntervalMinutes while Enabled.
type SyncJob struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	ConnectionID    string        `json:"connection_id"`
	LocalPath       string        `json:"local_path"`
	RemotePath      string        `json:"remote_path"`
	Direction       SyncDirection `json:"direction"`
	IntervalMinutes int           `json:"interval_minutes"`
	Enabled         bool          `json:"enabled"`

	// Mirror makes one-way jobs delete files at the destination that no
	// longer exist at the source (rclone sync instead of rclone copy).
	Mirror bool `json:"mirror,omitempty"`

	// BisyncReady is set after a two-way job's first (--resync) run has
	// succeeded; later runs are incremental.
	BisyncReady bool `json:"bisync_ready,omitempty"`
}

// SameTarget reports whether two jobs sync the same pair of folders the same
// way, i.e. whether a two-way job's bisync state is still valid.
func (j *SyncJob) SameTarget(o *SyncJob) bool {
	return j.ConnectionID == o.ConnectionID && j.LocalPath == o.LocalPath &&
		j.RemotePath == o.RemotePath && j.Direction == o.Direction
}

func (s *Store) syncJobsPath() string { return filepath.Join(s.dir, "syncjobs.json") }

func (s *Store) loadSyncJobs() error {
	data, err := os.ReadFile(s.syncJobsPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read sync jobs: %w", err)
	}
	if err := json.Unmarshal(data, &s.syncJobs); err != nil {
		return fmt.Errorf("parse sync jobs: %w", err)
	}
	return nil
}

func (s *Store) saveSyncJobs() error {
	data, err := json.MarshalIndent(s.syncJobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.syncJobsPath(), data, 0600)
}

// SyncJobs returns copies of all sync jobs.
func (s *Store) SyncJobs() []*SyncJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*SyncJob, len(s.syncJobs))
	for i, j := range s.syncJobs {
		cp := *j
		out[i] = &cp
	}
	return out
}

// SyncJob returns a copy of the job with the given ID.
func (s *Store) SyncJob(id string) (*SyncJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, j := range s.syncJobs {
		if j.ID == id {
			cp := *j
			return &cp, true
		}
	}
	return nil, false
}

// SaveSyncJob adds j (when its ID is empty) or replaces the stored job.
func (s *Store) SaveSyncJob(j *SyncJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *j
	if cp.ID == "" {
		cp.ID = uuid.NewString()
		j.ID = cp.ID
		s.syncJobs = append(s.syncJobs, &cp)
		return s.saveSyncJobs()
	}
	for i, existing := range s.syncJobs {
		if existing.ID == cp.ID {
			s.syncJobs[i] = &cp
			return s.saveSyncJobs()
		}
	}
	return fmt.Errorf("sync job %s not found", cp.ID)
}

// DeleteSyncJob removes a job.
func (s *Store) DeleteSyncJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, j := range s.syncJobs {
		if j.ID == id {
			s.syncJobs = append(s.syncJobs[:i], s.syncJobs[i+1:]...)
			return s.saveSyncJobs()
		}
	}
	return fmt.Errorf("sync job %s not found", id)
}

// DeleteSyncJobsFor removes every job that uses the given connection.
func (s *Store) DeleteSyncJobsFor(connID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.syncJobs[:0]
	for _, j := range s.syncJobs {
		if j.ConnectionID != connID {
			kept = append(kept, j)
		}
	}
	s.syncJobs = kept
	return s.saveSyncJobs()
}
