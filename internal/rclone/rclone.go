package rclone

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"wintp/internal/proc"
	"wintp/internal/store"
)

type Runner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	Run(ctx context.Context, name string, args ...string) error
}

type ExecRunner struct{}

func (ExecRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return proc.CommandContext(ctx, name, args...).CombinedOutput()
}

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return proc.CommandContext(ctx, name, args...).Output()
}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	return proc.CommandContext(ctx, name, args...).Run()
}

type Manager struct {
	Bin            string
	ConfigPath     string
	KnownHostsPath string
	Runner         Runner
}

func NewManager(bin, configPath, knownHostsPath string) *Manager {
	return &Manager{
		Bin:            bin,
		ConfigPath:     configPath,
		KnownHostsPath: knownHostsPath,
		Runner:         ExecRunner{},
	}
}

func (m *Manager) runner() Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return ExecRunner{}
}

func (m *Manager) SyncRemote(ctx context.Context, c *store.Connection, oldID string) error {
	name := store.RemoteName(c.ID)
	if oldID != "" && oldID != c.ID {
		_ = m.DeleteRemote(ctx, oldID)
	}

	extraArgs := RemoteConfigArgs(c, m.KnownHostsPath)

	if c.Config[store.ConfigKeyOAuth] == "true" && m.RemoteHasToken(ctx, name) {
		updateArgs := append([]string{"config", "update", name}, extraArgs...)
		updateArgs = append(updateArgs, "--config", m.ConfigPath, "--non-interactive")
		out, err := m.runner().CombinedOutput(ctx, m.Bin, updateArgs...)
		if err != nil {
			log.Printf("rclone: config update %s: %v\n%s", name, err, out)
		} else {
			return nil
		}
	}

	createArgs := append([]string{"config", "create", name, c.RcloneType()}, extraArgs...)
	createArgs = append(createArgs, "--config", m.ConfigPath, "--non-interactive")
	out, err := m.runner().CombinedOutput(ctx, m.Bin, createArgs...)
	if err != nil {
		return fmt.Errorf("rclone config create: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func RemoteConfigArgs(c *store.Connection, knownHostsPath string) []string {
	var args []string
	keys := make([]string, 0, len(c.Config))
	for k := range c.Config {
		if strings.HasPrefix(k, "_") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := c.Config[k]
		args = append(args, k, v)
	}
	if c.RcloneType() == "sftp" && knownHostsPath != "" {
		args = append(args, "known_hosts_file", knownHostsPath)
	}
	return args
}

func (m *Manager) DeleteRemote(ctx context.Context, id string) error {
	name := store.RemoteName(id)
	out, err := m.runner().CombinedOutput(ctx, m.Bin, "config", "delete", name, "--config", m.ConfigPath)
	if err != nil {
		return fmt.Errorf("rclone config delete: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) RemoteHasToken(ctx context.Context, remoteName string) bool {
	out, err := m.runner().Output(ctx, m.Bin, "config", "show", remoteName, "--config", m.ConfigPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "access_token")
}

func (m *Manager) ConnectionHasToken(ctx context.Context, id string) bool {
	return m.RemoteHasToken(ctx, store.RemoteName(id))
}

func (m *Manager) Authorize(ctx context.Context, rcloneType string) (string, error) {
	out, err := m.runner().Output(ctx, m.Bin, "authorize", rcloneType)
	if err != nil {
		return "", fmt.Errorf("rclone authorize: %w", err)
	}
	return ParseAuthorizeToken(string(out))
}

func (m *Manager) AuthorizeWithTimeout(rcloneType string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return m.Authorize(ctx, rcloneType)
}

func (m *Manager) UpdateToken(ctx context.Context, id, token string) error {
	remoteName := store.RemoteName(id)
	out, err := m.runner().CombinedOutput(ctx, m.Bin,
		"config", "update", remoteName,
		"token", token,
		"--config", m.ConfigPath,
		"--non-interactive",
	)
	if err != nil {
		return fmt.Errorf("rclone token save: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func ParseAuthorizeToken(output string) (string, error) {
	const startMarker = "Paste the following into your remote machine --->"
	const endMarker = "<---End paste"

	start := strings.Index(output, startMarker)
	if start == -1 {
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
				return line, nil
			}
		}
		return "", fmt.Errorf("could not find token in rclone authorize output")
	}

	start += len(startMarker)
	end := strings.Index(output[start:], endMarker)
	if end == -1 {
		return "", fmt.Errorf("could not parse token boundaries in rclone authorize output")
	}
	return strings.TrimSpace(output[start : start+end]), nil
}

func ObscurePassword(ctx context.Context, runner Runner, rcloneBin, password string) string {
	if password == "" {
		return ""
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	out, err := runner.Output(ctx, rcloneBin, "obscure", password)
	if err != nil {
		log.Printf("rclone obscure failed, omitting password env var: %v", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}
