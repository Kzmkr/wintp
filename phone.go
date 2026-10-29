package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"
	"golang.org/x/sys/windows"

	"wintp/internal/link"
	"wintp/internal/mount"
	"wintp/internal/radio"
	rcloneSvc "wintp/internal/rclone"
	"wintp/internal/store"
	"wintp/internal/thumbs"
	"wintp/internal/ui"
)

// fyneClipboard adapts Fyne's clipboard to link.Clipboard. Fyne requires UI
// calls on its main goroutine, so every access is marshalled with fyne.Do.
type fyneClipboard struct{ app fyne.App }

func (c fyneClipboard) Text() (s string) {
	fyne.DoAndWait(func() { s = c.app.Clipboard().Content() })
	return s
}

func (c fyneClipboard) SetText(s string) {
	fyne.DoAndWait(func() { c.app.Clipboard().SetContent(s) })
}

// openInBrowser opens an already-validated http(s) URL in the default browser.
// It avoids cmd.exe so the URL is never interpreted by a shell.
func openInBrowser(u string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
}

var mediaKeys = map[string]uintptr{
	"playpause": 0xB3, // VK_MEDIA_PLAY_PAUSE
	"next":      0xB0, // VK_MEDIA_NEXT_TRACK
	"prev":      0xB1, // VK_MEDIA_PREV_TRACK
	"stop":      0xB2, // VK_MEDIA_STOP
	"volup":     0xAF, // VK_VOLUME_UP
	"voldown":   0xAE, // VK_VOLUME_DOWN
	"mute":      0xAD, // VK_VOLUME_MUTE
}

var procKeybdEvent = windows.NewLazySystemDLL("user32.dll").NewProc("keybd_event")

// sendMediaKey presses and releases the Windows media key for action, which
// whatever app is handling media (Spotify, a browser, VLC...) responds to.
func sendMediaKey(action string) error {
	vk, ok := mediaKeys[action]
	if !ok {
		return fmt.Errorf("unknown media action %q", action)
	}
	const keyEventExtended, keyEventKeyUp = 0x1, 0x2
	if err := procKeybdEvent.Find(); err != nil {
		return err
	}
	procKeybdEvent.Call(vk, 0, keyEventExtended, 0)
	procKeybdEvent.Call(vk, 0, keyEventExtended|keyEventKeyUp, 0)
	return nil
}

type linkConfig struct {
	Enabled   bool            `json:"enabled"`
	OpenRules []link.OpenRule `json:"open_rules,omitempty"`
	Shares    []link.Share    `json:"shares,omitempty"`
}

func (c *coordinator) linkDir() string { return filepath.Join(c.appDir, "link") }

func (c *coordinator) loadLinkConfig() linkConfig {
	var cfg linkConfig
	if data, err := os.ReadFile(filepath.Join(c.linkDir(), "config.json")); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	return cfg
}

func (c *coordinator) saveLinkConfig(cfg linkConfig) error {
	if err := os.MkdirAll(c.linkDir(), 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(cfg)
	return os.WriteFile(filepath.Join(c.linkDir(), "config.json"), data, 0600)
}

// initLink creates the link server (without listening) and starts it if the
// user previously enabled phone link.
func (c *coordinator) initLink() {
	home, _ := os.UserHomeDir()
	inbox := filepath.Join(home, "Downloads", "wintp")
	srv, err := link.New(c.linkDir(), inbox, 0, fyneClipboard{c.app})
	if err != nil {
		log.Printf("link: init: %v", err)
		return
	}
	srv.OnDevice = func() {
		if c.mainWin != nil {
			c.mainWin.Refresh()
		}
	}
	srv.OpenURL = openInBrowser
	srv.Shares = func() []link.Share { return c.loadLinkConfig().Shares }
	srv.OnFile = func(name string, _ int64) {
		rule, ok := link.MatchRule(c.loadLinkConfig().OpenRules, name)
		if !ok {
			return
		}
		if err := rule.Open(filepath.Join(inbox, name)); err != nil {
			log.Printf("link: open %s: %v", name, err)
		}
	}
	srv.Media = sendMediaKey
	srv.Bluetooth = func(set *bool) (string, error) {
		return radio.Bluetooth(context.Background(), set)
	}
	c.link = srv
	if c.loadLinkConfig().Enabled {
		go func() {
			if err := srv.Start(); err != nil {
				log.Printf("link: start: %v", err)
				return
			}
			c.linkRunning.Store(true)
			if c.mainWin != nil {
				c.mainWin.Refresh()
			}
		}()
	}
}

const phoneDriveID = "phone-drive"

// PhoneDrive reports the state of the phone-as-a-drive feature.
func (c *coordinator) PhoneDrive() ui.PhoneDriveInfo {
	info := ui.PhoneDriveInfo{State: mount.StateIdle}
	if c.link != nil {
		info.Addr, info.Online = c.link.DriveOnline()
	}
	if conn, ok := c.store.Get(phoneDriveID); ok {
		info.Exists = true
		info.Letter = conn.DriveLetter
		info.AutoMount = conn.AutoMount
		info.State = c.sv.MountStatus(phoneDriveID).State
		info.Thumbs = conn.Config[store.ConfigKeyThumbs] != "false"
	}
	st := c.thumbs.Status()
	info.ThumbsRunning, info.ThumbsScanned = st.Running, st.Scanned
	info.ThumbsTotal, info.ThumbsReady, info.ThumbsFailed = st.Total, st.Ready, st.Failed
	return info
}

func (c *coordinator) initThumbs() {
	c.thumbs = thumbs.New()
	c.thumbs.OnChange = func() {
		if c.mainWin != nil {
			c.mainWin.Refresh()
		}
	}
}

// syncThumbs runs thumbnail pre-loading exactly while the phone drive is
// mounted and the feature is on. Safe from any goroutine; Stop waits for
// in-flight thumbnails, so the work happens off the caller's goroutine.
func (c *coordinator) syncThumbs() {
	go func() {
		conn, ok := c.store.Get(phoneDriveID)
		if ok && conn.Config[store.ConfigKeyThumbs] != "false" &&
			c.sv.MountStatus(phoneDriveID).State == mount.StateMounted {
			c.thumbs.Start(conn.DriveLetter + `:\`)
			return
		}
		c.thumbs.Stop()
	}()
}

// SetPhoneThumbs turns thumbnail pre-loading for the phone drive on or off.
func (c *coordinator) SetPhoneThumbs(on bool) error {
	conn, ok := c.store.Get(phoneDriveID)
	if !ok {
		return errors.New("set up the phone drive first")
	}
	conn.Config[store.ConfigKeyThumbs] = strconv.FormatBool(on)
	if err := c.store.Update(conn); err != nil {
		return err
	}
	c.syncThumbs()
	return nil
}

// MountPhoneDrive creates or updates the phone drive connection and mounts it.
func (c *coordinator) MountPhoneDrive(letter string, autoMount bool) error {
	if c.link == nil || !c.linkRunning.Load() {
		return errors.New("turn on Phone link first")
	}
	conn := &store.Connection{
		ID:            phoneDriveID,
		Name:          "Phone",
		Provider:      "webdav",
		Config:        c.link.DriveConfig(),
		DriveLetter:   letter,
		CacheMode:     store.CacheModeWrites,
		AutoMount:     autoMount,
		AutoReconnect: true,
	}
	conn.Config[store.ConfigKeyUIType] = "webdav"
	conn.Config[store.ConfigKeyThumbs] = "true"
	if old, ok := c.store.Get(phoneDriveID); ok && old.Config[store.ConfigKeyThumbs] != "" {
		conn.Config[store.ConfigKeyThumbs] = old.Config[store.ConfigKeyThumbs]
	}
	// rclone expects the webdav password obscured; plain text there makes every
	// request fail with 401 (shown in Explorer as an I/O error).
	obscured := rcloneSvc.ObscurePassword(context.Background(), nil, c.rcloneBin, conn.Config["pass"])
	if obscured == "" {
		return errors.New("could not prepare the drive password")
	}
	conn.Config["pass"] = obscured
	if _, exists := c.store.Get(phoneDriveID); exists {
		c.sv.Unmount(phoneDriveID)
		if err := c.UpdateConnection(conn, ""); err != nil {
			return err
		}
	} else if err := c.AddConnection(conn, ""); err != nil {
		return err
	}
	c.tray.Rebuild()
	go c.doMount(conn)
	return nil
}

func (c *coordinator) UnmountPhoneDrive() { go c.sv.Unmount(phoneDriveID) }

// ForgetPhoneDrive removes the phone drive connection entirely.
func (c *coordinator) ForgetPhoneDrive() error {
	if _, ok := c.store.Get(phoneDriveID); !ok {
		return nil
	}
	err := c.DeleteConnection(phoneDriveID)
	c.tray.Rebuild()
	return err
}

func (c *coordinator) OpenPhoneLink() {
	fyne.Do(func() { c.mainWin.ShowPhone() })
}

// ui.PhoneController implementation

func (c *coordinator) PhoneLinkEnabled() bool { return c.linkRunning.Load() }

func (c *coordinator) SetPhoneLinkEnabled(on bool) error {
	if c.link == nil {
		return errors.New("phone link unavailable (see log)")
	}
	if on == c.linkRunning.Load() {
		return nil
	}
	if on {
		if err := c.link.Start(); err != nil {
			return err
		}
	} else {
		c.link.Stop()
	}
	c.linkRunning.Store(on)
	cfg := c.loadLinkConfig()
	cfg.Enabled = on
	return c.saveLinkConfig(cfg)
}

func (c *coordinator) PhonePairingURI() (string, error) {
	if c.link == nil || !c.linkRunning.Load() {
		return "", errors.New("phone link is not enabled")
	}
	return c.link.NewPairing(), nil
}

func (c *coordinator) PhoneDevices() []link.Device {
	if c.link == nil {
		return nil
	}
	return c.link.Devices()
}

func (c *coordinator) RemovePhoneDevice(id string) error { return c.link.RemoveDevice(id) }

func (c *coordinator) OpenRules() []link.OpenRule { return c.loadLinkConfig().OpenRules }

func (c *coordinator) SetOpenRules(rules []link.OpenRule) error {
	cfg := c.loadLinkConfig()
	cfg.OpenRules = rules
	return c.saveLinkConfig(cfg)
}

func (c *coordinator) PCShares() []link.Share { return c.loadLinkConfig().Shares }

func (c *coordinator) SetPCShares(shares []link.Share) error {
	cfg := c.loadLinkConfig()
	cfg.Shares = shares
	return c.saveLinkConfig(cfg)
}

func (c *coordinator) PhoneInbox() string {
	if c.link == nil {
		return ""
	}
	return c.link.Inbox()
}
