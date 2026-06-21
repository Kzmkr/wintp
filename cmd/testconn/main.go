// testconn is a CLI tool for testing wintp connection logic without the GUI.
//
// Usage:
//
//	set WINTP_TEST_PASSWORD=password
//	testconn -host test.rebex.net -port 21 -proto ftp  -user demo
//	testconn -host test.rebex.net -port 990 -proto ftps -user demo
//	testconn -host example.com    -port 22  -proto sftp -user admin
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"wintp/internal/mount"
	"wintp/internal/store"
	"wintp/internal/util"
)

func main() {
	host := flag.String("host", "", "Server hostname")
	port := flag.Int("port", 0, "Port (default: 21 for FTP/FTPS, 22 for SFTP)")
	proto := flag.String("proto", "ftp", "Protocol: ftp | ftps | sftp")
	user := flag.String("user", "", "Username")
	path := flag.String("path", "/", "Remote path to list")
	keyFile := flag.String("key", "", "SSH private key file (SFTP only)")
	flag.Parse()

	if *host == "" || *user == "" {
		flag.Usage()
		os.Exit(1)
	}

	pass := os.Getenv("WINTP_TEST_PASSWORD")

	if *port == 0 {
		*port = store.DefaultPort(*proto)
	}

	// Locate rclone next to the binary or on PATH.
	exe, _ := os.Executable()
	rcloneBin := filepath.Join(filepath.Dir(exe), "rclone.exe")
	if _, err := os.Stat(rcloneBin); err != nil {
		rcloneBin = "rclone"
	}

	tmpDir, err := os.MkdirTemp("", "wintp-testconn-*")
	if err != nil {
		log.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rcloneConf := filepath.Join(tmpDir, "rclone.conf")
	_ = os.WriteFile(rcloneConf, []byte{}, 0600)
	knownHosts := filepath.Join(tmpDir, "known_hosts")
	_ = os.WriteFile(knownHosts, []byte{}, 0600)

	// Build a Connection using the generic Config map.
	rcloneType := store.RcloneTypeFor(*proto)
	cfg := map[string]string{
		"host":                 *host,
		"port":                 strconv.Itoa(*port),
		"user":                 *user,
		store.ConfigKeyUIType:  *proto,
		store.ConfigKeyPassEnv: "PASS",
	}
	if *proto == "ftps" {
		cfg["tls"] = "true"
	}
	if *proto == "sftp" && *keyFile != "" {
		cfg["key_file"] = *keyFile
	}

	conn := &store.Connection{
		Provider:   rcloneType,
		Config:     cfg,
		RemotePath: *path,
	}

	fmt.Printf("Testing %s://%s@%s:%d%s ...\n", *proto, *user, *host, *port, *path)

	if err := mount.TestConnection(rcloneBin, rcloneConf, knownHosts, conn, pass); err != nil {
		var unknown *util.UnknownHostKeyError
		if errors.As(err, &unknown) {
			fmt.Fprintf(os.Stderr, "FAIL: unknown SSH host key\n%s:%d -> %s\nAdd it to your known_hosts file to continue.\n", unknown.Info.Host, unknown.Info.Port, unknown.Info.Fingerprint)
		} else {
			fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		}
		os.Exit(1)
	}
	fmt.Println("OK - connection successful")
}
