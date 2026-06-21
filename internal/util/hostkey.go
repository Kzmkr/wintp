package util

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// KnownHostsPath returns the path to our per-app known_hosts file.
func KnownHostsPath(appDir string) string {
	return filepath.Join(appDir, "known_hosts")
}

// EnsureKnownHosts creates the known_hosts file if it doesn't exist.
func EnsureKnownHosts(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.WriteFile(path, []byte{}, 0600)
	}
	return nil
}

// HostKeyInfo represents a newly seen SSH host key awaiting user confirmation.
type HostKeyInfo struct {
	Host        string
	Port        int
	Fingerprint string
	key         ssh.PublicKey
}

// UnknownHostKeyError is returned when an SSH host key is not yet trusted.
type UnknownHostKeyError struct {
	Info *HostKeyInfo
}

func (e *UnknownHostKeyError) Error() string {
	return fmt.Sprintf("unknown SSH host key for %s:%d\nfingerprint: %s", e.Info.Host, e.Info.Port, e.Info.Fingerprint)
}

// EnsureHostKnown checks whether host:port has a trusted key in knownHostsFile.
//
// If the key is already trusted, it returns (nil, nil).
// If the key is unknown, it returns (*HostKeyInfo, nil) so the caller can ask
// the user whether to trust it.
// If the key differs from the trusted one or another error occurs, it returns
// (nil, error).
func EnsureHostKnown(knownHostsFile, host string, port int) (*HostKeyInfo, error) {
	if knownHostsFile == "" {
		return nil, fmt.Errorf("known_hosts file path is empty")
	}
	if err := EnsureKnownHosts(knownHostsFile); err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	callback, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}

	key, err := scanHostKey(host, port)
	if err != nil {
		return nil, err
	}

	if err := callback(addr, &hostAddr{addr: addr}, key); err != nil {
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			if len(keyErr.Want) > 0 {
				return nil, fmt.Errorf("host key for %s does not match known_hosts (possible MITM)", addr)
			}
			// Host not known - return info for confirmation.
			return &HostKeyInfo{
				Host:        host,
				Port:        port,
				Fingerprint: ssh.FingerprintSHA256(key),
				key:         key,
			}, nil
		}
		return nil, fmt.Errorf("known_hosts check for %s: %w", addr, err)
	}
	return nil, nil
}

// AddKnownHost appends the host key from info to knownHostsFile.
func AddKnownHost(knownHostsFile string, info *HostKeyInfo) error {
	if info == nil || info.key == nil {
		return fmt.Errorf("no host key to add")
	}
	if err := EnsureKnownHosts(knownHostsFile); err != nil {
		return err
	}
	f, err := os.OpenFile(knownHostsFile, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open known_hosts: %w", err)
	}
	defer f.Close()
	addr := net.JoinHostPort(info.Host, strconv.Itoa(info.Port))
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, info.key)
	_, err = fmt.Fprintln(f, line)
	return err
}

type hostAddr struct {
	addr string
}

func (h *hostAddr) Network() string { return "tcp" }
func (h *hostAddr) String() string  { return h.addr }

func scanHostKey(host string, port int) (ssh.PublicKey, error) {
	const timeout = 10 * time.Second
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	var seen ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: "scan",
		Auth: []ssh.AuthMethod{ssh.Password("")},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = key
			return nil
		},
		Timeout: timeout,
	}

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	// Set deadline so the handshake can't block forever.
	_ = conn.SetDeadline(time.Now().Add(timeout))
	// NewClientConn will fail at auth (wrong password) but we get the host key first.
	sshConn, _, _, _ := ssh.NewClientConn(conn, addr, cfg)
	if sshConn != nil {
		_ = sshConn.Close()
	}
	_ = conn.Close()

	if seen == nil {
		return nil, fmt.Errorf("server at %s sent no host key", addr)
	}
	return seen, nil
}
