package main

import (
	"log"

	"fyne.io/fyne/v2"

	"wintp/internal/autosync"
	"wintp/internal/store"
)

// initAutoSync creates the sync job scheduler. Start it once the main
// window exists, since status changes refresh the window.
func (c *coordinator) initAutoSync() {
	c.autosync = autosync.New(c.rcloneBin, c.store.RcloneConf(), c.store)
	c.autosync.OnChange = func(string) {
		if c.mainWin != nil {
			c.mainWin.Refresh()
		}
	}
}

// ui.SyncController implementation

func (c *coordinator) SyncJobs() []*store.SyncJob { return c.store.SyncJobs() }

func (c *coordinator) SyncJobStatus(id string) autosync.Status { return c.autosync.Status(id) }

func (c *coordinator) SaveSyncJob(job *store.SyncJob) error {
	old, existed := c.store.SyncJob(job.ID)
	if existed && old.SameTarget(job) {
		job.BisyncReady = old.BisyncReady
	} else {
		// New folders or direction: a two-way job must merge from scratch.
		job.BisyncReady = false
	}
	if err := c.store.SaveSyncJob(job); err != nil {
		return err
	}
	if !existed || !old.SameTarget(job) || (job.Enabled && !old.Enabled) {
		// Stop a run against the old target and run the new one soon.
		c.autosync.Cancel(job.ID)
	}
	return nil
}

func (c *coordinator) DeleteSyncJob(id string) error {
	c.autosync.Cancel(id)
	return c.store.DeleteSyncJob(id)
}

func (c *coordinator) RunSyncJob(id string) { c.autosync.RunNow(id) }

// deleteSyncJobsFor removes the sync jobs of a connection being deleted.
func (c *coordinator) deleteSyncJobsFor(connID string) {
	for _, j := range c.store.SyncJobs() {
		if j.ConnectionID == connID {
			c.autosync.Cancel(j.ID)
		}
	}
	if err := c.store.DeleteSyncJobsFor(connID); err != nil {
		log.Printf("delete sync jobs for %s: %v", connID, err)
	}
}

// tray.Controller

func (c *coordinator) SyncAllNow() {
	for _, j := range c.store.SyncJobs() {
		if j.Enabled {
			c.autosync.RunNow(j.ID)
		}
	}
}

func (c *coordinator) OpenAutoSync() {
	fyne.Do(func() { c.mainWin.ShowAutoSync() })
}
