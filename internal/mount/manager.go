package mount

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"wintp/internal/cred"
	"wintp/internal/proc"
	"wintp/internal/store"
	"wintp/internal/util"
)

type State string

const (
	StateIdle       State = "idle"
	StateMounting   State = "mounting"
	StateMounted    State = "mounted"
	StateUnmounting State = "unmounting"
	StateError      State = "error"
)

type Status struct {
	State   State
	Message string
}

type Mount struct {
	mu             sync.Mutex
	conn           *store.Connection
	rcloneBin      string
	rcloneConf     string
	knownHostsFile string

	state   State
	message string
	cmd     *exec.Cmd
	cancel  context.CancelFunc

	StatusCh chan Status
}

func New(conn *store.Connection, rcloneBin, rcloneConf, knownHostsFile string) *Mount {
	return &Mount{
		conn:           conn,
		rcloneBin:      rcloneBin,
		rcloneConf:     rcloneConf,
		knownHostsFile: knownHostsFile,
		state:          StateIdle,
		StatusCh:       make(chan Status, 8),
	}
}

func (m *Mount) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Mount) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{State: m.state, Message: m.message}
}

func (m *Mount) setState(s State, msg string) {
	m.mu.Lock()
	m.state = s
	m.message = msg
	m.mu.Unlock()
	m.publishStatus()
}

func (m *Mount) publishStatus() {
	m.mu.Lock()
	status := Status{State: m.state, Message: m.message}
	m.mu.Unlock()
	select {
	case m.StatusCh <- status:
	default:
	}
}

func (m *Mount) Start() error {
	m.mu.Lock()
	if m.state != StateIdle && m.state != StateError {
		m.mu.Unlock()
		return fmt.Errorf("mount already active (state: %s)", m.state)
	}
	if m.conn.DriveLetter == "" {
		m.state = StateError
		m.message = "drive letter is required"
		m.mu.Unlock()
		m.publishStatus()
		return fmt.Errorf("drive letter is required")
	}

	m.state = StateMounting
	m.message = "starting"
	// Create the context early so Stop can cancel a mount that is still starting.
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.mu.Unlock()
	m.publishStatus()

	if m.conn.Provider == "sftp" && m.knownHostsFile != "" {
		host := m.conn.Config["host"]
		port, _ := strconv.Atoi(m.conn.Config["port"])
		if port == 0 {
			port = 22
		}
		info, err := util.EnsureHostKnown(m.knownHostsFile, host, port)
		if err != nil {
			m.stopInternal(fmt.Errorf("host key check: %w", err))
			return err
		}
		if info != nil {
			m.stopInternal(&util.UnknownHostKeyError{Info: info})
			return &util.UnknownHostKeyError{Info: info}
		}
	}

	env := buildEnv(m.rcloneBin, m.conn)

	args := MountArgs(m.conn, m.rcloneConf)
	cmd := proc.CommandContext(ctx, m.rcloneBin, args...)
	cmd.Env = env

	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.stopInternal(err)
		return err
	}

	if err := cmd.Start(); err != nil {
		m.stopInternal(fmt.Errorf("start rclone: %w", err))
		return fmt.Errorf("start rclone: %w", err)
	}

	if err := proc.AssignToKillJob(cmd); err != nil {
		log.Printf("[%s] warning: mount process won't be auto-killed with wintp: %v", m.conn.Name, err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.mu.Unlock()

	ready := make(chan error, 1)
	go func() {
		readyClosed := false
		var captured []string

		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			log.Printf("[rclone %s] %s", m.conn.Name, line)
			captured = append(captured, line)

			result, ok := readinessFromLogLine(line)
			if !ok {
				continue
			}
			if !readyClosed {
				readyClosed = true
				ready <- result
			}
		}

		if !readyClosed {
			msg := "rclone exited before mount was ready"
			for i := len(captured) - 1; i >= 0; i-- {
				if t := strings.TrimSpace(captured[i]); t != "" {
					msg = t
					break
				}
			}
			ready <- fmt.Errorf("%s", msg)
		}
	}()

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case err := <-ready:
		if err != nil {
			m.stopInternal(err)
			return err
		}
	case <-ctx.Done():
		m.stopInternal(ctx.Err())
		return ctx.Err()
	case <-timer.C:
		m.stopInternal(fmt.Errorf("mount timed out after 30s"))
		return fmt.Errorf("mount timed out after 30s")
	}

	m.setState(StateMounted, "")

	go func() {
		exitErr := cmd.Wait()
		cur := m.State()
		if cur == StateMounted || cur == StateMounting {
			msg := "rclone exited unexpectedly"
			if exitErr != nil {
				msg = exitErr.Error()
			}
			m.setState(StateError, msg)
		}
	}()

	return nil
}

// MountArgs deliberately omits rclone's --network-mode: it registers the
// drive through the Windows network-provider (WNetAddConnection), which is
// scoped to the caller's logon session. If wintp is ever run elevated, the
// mount becomes invisible to the normal desktop session - it "connects" but
// never shows up in Explorer. The default WinFsp disk mode mounts a real
// local drive letter, which has no such session scoping.
func MountArgs(conn *store.Connection, rcloneConf string) []string {
	remote := store.RemoteName(conn.ID) + ":" + conn.RemotePath
	target := conn.DriveLetter + ":"
	args := []string{
		"mount", remote, target,
		"--config", rcloneConf,
		"--vfs-cache-mode", string(conn.CacheMode),
		"--volname", conn.Name,
		"--log-level", "INFO",
		"--use-json-log",
		"--no-console",
	}
	args = append(args, VolumeIdentityArgs(conn.ID)...)
	if conn.CacheMaxSize != "" {
		args = append(args, "--vfs-cache-max-size", conn.CacheMaxSize)
	}
	if conn.CacheMaxAge != "" {
		args = append(args, "--vfs-cache-max-age", conn.CacheMaxAge)
	}
	if conn.DirCacheTime != "" {
		args = append(args, "--dir-cache-time", conn.DirCacheTime)
	}
	if conn.BufferSize != "" {
		args = append(args, "--buffer-size", conn.BufferSize)
	}
	if conn.ReadChunkSize != "" {
		args = append(args, "--vfs-read-chunk-size", conn.ReadChunkSize)
	}
	return args
}

// volumeCreationTime is a fixed FILETIME (2024-01-01) reported for every
// wintp volume; together with the serial it identifies the volume.
const volumeCreationTime = 133485408000000000

// VolumeIdentityArgs pins the WinFsp volume serial number (derived from the
// connection ID) and creation time. By default WinFsp picks a new serial on
// every mount, and Windows keys its thumbnail cache on the volume, so every
// reconnect would make Explorer rebuild every thumbnail by reading whole
// files over the network. With a stable identity they are built once.
func VolumeIdentityArgs(connID string) []string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(connID))
	return []string{
		"-o", fmt.Sprintf("VolumeSerialNumber=%08x", h.Sum32()),
		"-o", fmt.Sprintf("VolumeCreationTime=%d", volumeCreationTime),
	}
}

// Stop cleanly shuts down the rclone mount. It is safe to call multiple times.
func (m *Mount) Stop() {
	m.mu.Lock()
	if m.state == StateIdle {
		m.mu.Unlock()
		return
	}
	m.state = StateUnmounting
	m.message = ""
	cancel := m.cancel
	cmd := m.cmd
	m.cancel = nil
	m.cmd = nil
	m.mu.Unlock()

	m.publishStatus()

	if cancel != nil {
		cancel()
	}

	if cmd != nil && cmd.Process != nil {
		// Give rclone a short moment to exit after the context is cancelled,
		// then forcefully terminate it.
		time.Sleep(500 * time.Millisecond)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	m.setState(StateIdle, "")
}

// stopInternal is used during Start to abort and return to idle.
func (m *Mount) stopInternal(err error) {
	m.Stop()
	if err != nil {
		m.setState(StateError, err.Error())
	}
}

type rcloneLogEntry struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

func readinessFromLogLine(line string) (error, bool) {
	var entry rcloneLogEntry
	if jsonErr := json.Unmarshal([]byte(line), &entry); jsonErr != nil {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "failed") || strings.Contains(lower, "error") {
			return fmt.Errorf("%s", strings.TrimSpace(line)), true
		}
		return nil, false
	}

	msg := strings.ToLower(entry.Msg)
	switch {
	case strings.Contains(msg, "filesystem is serving"),
		strings.Contains(msg, "vfs cache: cleaned"),
		strings.Contains(msg, "mounting on"):
		return nil, true
	case entry.Level == "error" || entry.Level == "critical" || entry.Level == "fatal":
		return fmt.Errorf("%s", entry.Msg), true
	default:
		return nil, false
	}
}

// TestConnection verifies credentials by running `rclone lsd` without mounting.
// For OAuth providers the token must already be in rclone.conf (auth done separately).
func TestConnection(rcloneBin, rcloneConf, knownHostsFile string, conn *store.Connection, password string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// OAuth providers: test against the real remote (token is in rclone.conf).
	if conn.Config[store.ConfigKeyOAuth] == "true" {
		remoteName := store.RemoteName(conn.ID)
		remote := remoteName + ":" + conn.RemotePath
		out, err := proc.CommandContext(ctx, rcloneBin, "lsd", remote,
			"--config", rcloneConf, "--max-depth", "1").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s", strings.TrimSpace(string(out)))
		}
		return nil
	}

	// Non-OAuth: use a temporary remote so we don't store credentials in rclone.conf.
	tmpName := "wintp-test-" + uuid.NewString()[:8]

	// SFTP: scan host key first.
	if conn.Provider == "sftp" && knownHostsFile != "" {
		host := conn.Config["host"]
		port, _ := strconv.Atoi(conn.Config["port"])
		if port == 0 {
			port = 22
		}
		info, err := util.EnsureHostKnown(knownHostsFile, host, port)
		if err != nil {
			return fmt.Errorf("host key check: %w", err)
		}
		if info != nil {
			return &util.UnknownHostKeyError{Info: info}
		}
	}

	// Build config create args from Config map.
	createArgs := []string{"config", "create", tmpName, conn.RcloneType()}
	for k, v := range conn.Config {
		if strings.HasPrefix(k, "_") {
			continue
		}
		createArgs = append(createArgs, k, v)
	}
	if conn.Provider == "sftp" && knownHostsFile != "" {
		createArgs = append(createArgs, "known_hosts_file", knownHostsFile)
	}
	createArgs = append(createArgs, "--config", rcloneConf, "--non-interactive")

	if out, err := proc.CommandContext(ctx, rcloneBin, createArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("configure remote: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		_ = proc.CommandContext(cleanupCtx, rcloneBin, "config", "delete", tmpName, "--config", rcloneConf).Run()
	}()

	env := buildEnvForName(rcloneBin, tmpName, conn.Config[store.ConfigKeyPassEnv], password)
	remote := tmpName + ":" + conn.RemotePath
	lsdCmd := proc.CommandContext(ctx, rcloneBin, "lsd", remote, "--config", rcloneConf, "--max-depth", "1")
	lsdCmd.Env = env
	out, err := lsdCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Env returns the environment for any rclone command that uses conn's
// remote, with its stored password injected.
func Env(rcloneBin string, conn *store.Connection) []string { return buildEnv(rcloneBin, conn) }

// buildEnv builds the environment for a mount command using the stored password.
func buildEnv(rcloneBin string, conn *store.Connection) []string {
	password, _ := cred.Get(conn.ID)
	return buildEnvForName(rcloneBin, store.RemoteName(conn.ID), conn.Config[store.ConfigKeyPassEnv], password)
}

// buildEnvForName builds RCLONE_CONFIG_<REMOTE>_<SUFFIX>=<password> if suffix and password are set.
// The remote name is sanitized to match rclone's env-var naming rules.
func buildEnvForName(rcloneBin, remoteName, passEnvSuffix, password string) []string {
	_ = rcloneBin
	env := os.Environ()
	if passEnvSuffix == "" || password == "" {
		return env
	}
	// rclone upper-cases the remote name and replaces any non-alphanumeric
	// character with an underscore.
	name := strings.ToUpper(remoteName)
	name = strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(name)
	key := fmt.Sprintf("RCLONE_CONFIG_%s_%s=%s", name, strings.ToUpper(passEnvSuffix), password)
	return append(env, key)
}
