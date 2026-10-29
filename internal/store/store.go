package store

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type Store struct {
	mu          sync.RWMutex
	connections []*Connection
	syncJobs    []*SyncJob
	dir         string
	knownHosts  string
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if _, err := os.Stat(knownHosts); os.IsNotExist(err) {
		_ = os.WriteFile(knownHosts, []byte{}, 0600)
	}
	s := &Store{dir: dir, knownHosts: knownHosts}

	if _, err := os.Stat(s.RcloneConf()); os.IsNotExist(err) {
		if err := os.WriteFile(s.RcloneConf(), []byte{}, 0600); err != nil {
			return nil, fmt.Errorf("create rclone.conf: %w", err)
		}
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	if err := s.loadSyncJobs(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) configPath() string     { return filepath.Join(s.dir, "connections.json") }
func (s *Store) RcloneConf() string     { return filepath.Join(s.dir, "rclone.conf") }
func (s *Store) KnownHostsPath() string { return s.knownHosts }

func (s *Store) load() error {
	data, err := os.ReadFile(s.configPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read connections: %w", err)
	}
	if err := json.Unmarshal(data, &s.connections); err != nil {
		return fmt.Errorf("parse connections: %w", err)
	}
	migrated := false
	for _, c := range s.connections {
		if c.Provider == "" && c.LegacyProtocol != "" {
			migrateV1(c)
			migrated = true
		}
	}
	if migrated {
		if err := s.save(); err != nil {
			log.Printf("store: migration save: %v", err)
		}
	}
	return nil
}

// migrateV1 converts an old-style (v1) connection to the generic Config map format.
func migrateV1(c *Connection) {
	if c.Config == nil {
		c.Config = make(map[string]string)
	}
	if c.LegacyHost != "" {
		c.Config["host"] = c.LegacyHost
	}
	if c.LegacyPort > 0 {
		c.Config["port"] = strconv.Itoa(c.LegacyPort)
	}
	if c.LegacyUsername != "" {
		c.Config["user"] = c.LegacyUsername
	}
	c.Config[ConfigKeyPassEnv] = "PASS"

	switch c.LegacyProtocol {
	case "ftps":
		c.Provider = "ftp"
		c.Config["tls"] = "true"
		c.Config[ConfigKeyUIType] = "ftps"
	case "sftp":
		c.Provider = "sftp"
		c.Config[ConfigKeyUIType] = "sftp"
		if c.LegacySSHKey != "" {
			c.Config["key_file"] = c.LegacySSHKey
		}
	default:
		c.Provider = "ftp"
		c.Config[ConfigKeyUIType] = "ftp"
	}

	// Clear legacy fields so they are omitted from the next save.
	c.LegacyProtocol = ""
	c.LegacyHost = ""
	c.LegacyPort = 0
	c.LegacyUsername = ""
	c.LegacySSHKey = ""
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.connections, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.configPath(), data, 0600)
}

func (s *Store) All() []*Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Connection, len(s.connections))
	copy(out, s.connections)
	return out
}

func (s *Store) Get(id string) (*Connection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.connections {
		if c.ID == id {
			cp := *c
			cp.Config = make(map[string]string, len(c.Config))
			for k, v := range c.Config {
				cp.Config[k] = v
			}
			return &cp, true
		}
	}
	return nil, false
}

func (s *Store) Add(c *Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if s.driveLetterInUse(c.ID, c.DriveLetter) {
		return fmt.Errorf("drive letter %s: is already in use", c.DriveLetter)
	}
	s.connections = append(s.connections, c)
	return s.save()
}

func (s *Store) Update(c *Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.driveLetterInUse(c.ID, c.DriveLetter) {
		return fmt.Errorf("drive letter %s: is already in use", c.DriveLetter)
	}
	for i, existing := range s.connections {
		if existing.ID == c.ID {
			s.connections[i] = c
			return s.save()
		}
	}
	return fmt.Errorf("connection %s not found", c.ID)
}

// driveLetterInUse reports whether another connection already uses the given drive letter.
func (s *Store) driveLetterInUse(excludeID, letter string) bool {
	if letter == "" {
		return false
	}
	for _, existing := range s.connections {
		if existing.ID != excludeID && strings.EqualFold(existing.DriveLetter, letter) {
			return true
		}
	}
	return false
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.connections {
		if c.ID == id {
			s.connections = append(s.connections[:i], s.connections[i+1:]...)
			return s.save()
		}
	}
	return fmt.Errorf("connection %s not found", id)
}

// RemoteName returns the rclone remote name for a connection ID.
func RemoteName(id string) string { return "wintp-" + id }
