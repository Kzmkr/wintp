package link

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Device is a paired phone. Only a hash of its bearer token is stored.
type Device struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Hash    string    `json:"hash"`
	AddedAt time.Time `json:"added_at"`
}

type deviceStore struct {
	mu      sync.RWMutex
	path    string
	devices []Device
}

func loadDevices(dir string) (*deviceStore, error) {
	ds := &deviceStore{path: filepath.Join(dir, "devices.json")}
	data, err := os.ReadFile(ds.path)
	if os.IsNotExist(err) {
		return ds, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &ds.devices); err != nil {
		return nil, err
	}
	return ds, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (ds *deviceStore) saveLocked() error {
	data, err := json.MarshalIndent(ds.devices, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ds.path, data, 0600)
}

func (ds *deviceStore) add(d Device) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.devices = append(ds.devices, d)
	return ds.saveLocked()
}

func (ds *deviceStore) lookup(token string) (Device, bool) {
	h := hashToken(token)
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	for _, d := range ds.devices {
		if d.Hash == h {
			return d, true
		}
	}
	return Device{}, false
}

func (ds *deviceStore) list() []Device {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return append([]Device(nil), ds.devices...)
}

func (ds *deviceStore) remove(id string) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	out := ds.devices[:0]
	for _, d := range ds.devices {
		if d.ID != id {
			out = append(out, d)
		}
	}
	ds.devices = out
	return ds.saveLocked()
}
