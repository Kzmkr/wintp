package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"wintp/internal/cred"
	"wintp/internal/mount"
	rcloneSvc "wintp/internal/rclone"
	"wintp/internal/singleinstance"
	"wintp/internal/store"
	"wintp/internal/tray"
	"wintp/internal/ui"
	"wintp/internal/util"
)

func main() {
	instanceLock, err := singleinstance.Acquire("wintp-single-instance")
	if err == singleinstance.ErrAlreadyRunning {
		return
	}
	if err != nil {
		log.Fatalf("single instance lock: %v", err)
	}
	defer instanceLock.Release()

	a := app.New()
	a.SetIcon(nil) // updated after tray initializes

	// App data directory.
	appDir := filepath.Join(os.Getenv("APPDATA"), "wintp")

	// Set up file logging next to the executable so it is easy to find.
	// If the install directory is not writable, fall back to the app data dir.
	logFile, logPath := openLogFile(appDir)
	if logFile != nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	log.Printf("wintp starting - appDir=%s log=%s", appDir, logPath)

	// Locate rclone.
	rcloneBin, err := util.FindRclone()
	if err != nil {
		log.Printf("rclone not found: %v", err)
		// Show a dummy window so dialog works before tray is up.
		w := a.NewWindow("wintp - Setup required")
		dialog.ShowError(err, w)
		w.ShowAndRun()
		return
	}

	log.Printf("rclone: %s", rcloneBin)

	// Check WinFsp.
	if err := util.CheckWinFsp(); err != nil {
		log.Printf("WinFsp check: %v", err)
	} else {
		log.Printf("WinFsp: OK")
	}

	// Initialize store.
	st, err := store.New(appDir)
	if err != nil {
		log.Fatalf("store init: %v", err)
	}
	rc := rcloneSvc.NewManager(rcloneBin, st.RcloneConf(), st.KnownHostsPath())

	// Initialize supervisor.
	sv := mount.NewSupervisor(rcloneBin, st.RcloneConf(), st.KnownHostsPath())

	// App coordinator.
	coord := &coordinator{
		app:       a,
		store:     st,
		sv:        sv,
		rc:        rc,
		rcloneBin: rcloneBin,
		appDir:    appDir,
	}

	coord.SyncRemotes()

	// Build tray (sets up the system tray menu).
	t := tray.New(a, coord)
	coord.tray = t

	// Build the single persistent window (hidden until user opens settings).
	mainWin := ui.OpenMainWindow(a, coord)
	coord.mainWin = mainWin

	sv.OnStatusChange = func(connID string, s mount.Status) {
		coord.tray.Rebuild()
		coord.mainWin.Refresh()
	}

	// Auto-mount connections.
	for _, c := range st.All() {
		if c.AutoMount {
			c := c
			go func() {
				if err := sv.Mount(c); err != nil {
					log.Printf("auto-mount %s: %v", c.Name, err)
				}
			}()
		}
	}

	// Fyne runs its event loop here - returns when Quit() is called.
	a.Run()

	// Clean shutdown.
	sv.UnmountAll()
}

// openLogFile tries to create a log file next to the executable, falling back
// to the app data directory if that location is not writable.
func openLogFile(appDir string) (*os.File, string) {
	logName := "wintp.log"
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		path := filepath.Join(exeDir, logName)
		f, err := os.OpenFile(path,
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			return f, path
		}
	}
	logDir := filepath.Join(appDir, "logs")
	_ = os.MkdirAll(logDir, 0700)
	path := filepath.Join(logDir, logName)
	f, err := os.OpenFile(path,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("cannot open log file %s: %v", path, err)
		return nil, ""
	}
	return f, path
}

// coordinator implements tray.Controller and ui.SettingsController.
type coordinator struct {
	app       fyne.App
	store     *store.Store
	sv        *mount.Supervisor
	rc        *rcloneSvc.Manager
	tray      *tray.Tray
	mainWin   *ui.MainWindow
	rcloneBin string
	appDir    string
}

// tray.Controller implementation

func (c *coordinator) Connections() []*store.Connection { return c.store.All() }

func (c *coordinator) MountStatus(id string) mount.Status { return c.sv.MountStatus(id) }

func (c *coordinator) RequestMount(id string) {
	conn, ok := c.store.Get(id)
	if !ok {
		return
	}
	go c.doMount(conn)
}

func (c *coordinator) doMount(conn *store.Connection) {
	if err := c.sv.Mount(conn); err != nil {
		var unknown *util.UnknownHostKeyError
		if errors.As(err, &unknown) {
			c.promptHostKey(conn, unknown.Info, func() {
				if err := util.AddKnownHost(c.store.KnownHostsPath(), unknown.Info); err != nil {
					c.showError("Failed to save host key", err.Error())
					return
				}
				go c.doMount(conn)
			})
			return
		}
		log.Printf("mount %s: %v", conn.Name, err)
		c.showError("Mount failed: "+conn.Name, err.Error())
	}
}

func (c *coordinator) promptHostKey(conn *store.Connection, info *util.HostKeyInfo, onConfirm func()) {
	fyne.Do(func() {
		w := c.app.NewWindow("Unknown SSH host key")
		w.Resize(fyne.NewSize(480, 200))
		msg := fmt.Sprintf("The SSH server %s:%d presented a new host key:\n\n%s\n\nDo you want to trust it?", info.Host, info.Port, info.Fingerprint)
		d := dialog.NewConfirm("Trust host key?", msg, func(ok bool) {
			if ok {
				go onConfirm()
			}
			w.Close()
		}, w)
		d.Show()
		w.Show()
	})
}

func (c *coordinator) showError(title, msg string) {
	fyne.Do(func() {
		w := c.app.NewWindow(title)
		w.Resize(fyne.NewSize(440, 160))
		w.SetContent(container.NewPadded(container.NewVBox(
			widget.NewLabel(msg),
			widget.NewButton("OK", func() { w.Close() }),
		)))
		w.Show()
	})
}

func (c *coordinator) RequestUnmount(id string) {
	go c.sv.Unmount(id)
}

func (c *coordinator) OpenSettings() {
	fyne.Do(func() { c.mainWin.Show() })
}

func (c *coordinator) MountAll() {
	for _, conn := range c.store.All() {
		conn := conn
		go func() {
			if err := c.sv.Mount(conn); err != nil {
				log.Printf("mount-all %s: %v", conn.Name, err)
			}
		}()
	}
}

func (c *coordinator) UnmountAll() { c.sv.UnmountAll() }

func (c *coordinator) SetAutostart(enabled bool) error { return util.SetAutostart(enabled) }

func (c *coordinator) AutostartEnabled() bool { return util.AutostartEnabled() }

func (c *coordinator) Quit() {
	c.sv.UnmountAll()
	c.app.Quit()
}

// ui.SettingsController implementation

func (c *coordinator) AddConnection(conn *store.Connection, password string) error {
	if err := c.store.Add(conn); err != nil {
		return err
	}
	if err := c.rc.SyncRemote(context.Background(), conn, ""); err != nil {
		_ = c.store.Delete(conn.ID)
		return err
	}
	if password != "" {
		if err := cred.Set(conn.ID, password); err != nil {
			return err
		}
	}
	return nil
}

func (c *coordinator) UpdateConnection(conn *store.Connection, password string) error {
	old, ok := c.store.Get(conn.ID)
	if !ok {
		return fmt.Errorf("connection %s not found", conn.ID)
	}
	if err := c.rc.SyncRemote(context.Background(), conn, old.ID); err != nil {
		return err
	}
	if err := c.store.Update(conn); err != nil {
		return err
	}
	if password != "" {
		if err := cred.Set(conn.ID, password); err != nil {
			return err
		}
	}
	return nil
}

func (c *coordinator) DeleteConnection(id string) error {
	c.sv.Unmount(id)
	if err := c.store.Delete(id); err != nil {
		return err
	}
	_ = cred.Delete(id)
	if err := c.rc.DeleteRemote(context.Background(), id); err != nil {
		log.Printf("delete remote %s: %v", id, err)
	}
	return nil
}

func (c *coordinator) RcloneBin() string      { return c.rcloneBin }
func (c *coordinator) RcloneConf() string     { return c.store.RcloneConf() }
func (c *coordinator) KnownHostsPath() string { return c.store.KnownHostsPath() }

func (c *coordinator) AddKnownHost(info *util.HostKeyInfo) error {
	return util.AddKnownHost(c.store.KnownHostsPath(), info)
}

func (c *coordinator) OnSettingsChange() {
	c.tray.Rebuild()
}

func (c *coordinator) SyncRemotes() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, conn := range c.store.All() {
		if err := c.rc.SyncRemote(ctx, conn, ""); err != nil {
			log.Printf("sync remote %s: %v", conn.Name, err)
		}
	}
}

func (c *coordinator) Password(id string) (string, error) {
	return cred.Get(id)
}

func (c *coordinator) TestConnection(conn *store.Connection, password string) error {
	return mount.TestConnection(
		c.rcloneBin,
		c.store.RcloneConf(),
		c.store.KnownHostsPath(),
		conn,
		password,
	)
}

func (c *coordinator) OAuthAuthorized(id string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.rc.ConnectionHasToken(ctx, id)
}

func (c *coordinator) AuthorizeConnection(conn *store.Connection, rcloneType string) error {
	token, err := c.rc.AuthorizeWithTimeout(rcloneType)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.rc.UpdateToken(ctx, conn.ID, token)
}
