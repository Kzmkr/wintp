package tray

import (
	"fmt"
	"log"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"wintp/internal/mount"
	"wintp/internal/store"
)

// Controller is implemented by the app coordinator to handle tray actions.
type Controller interface {
	Connections() []*store.Connection
	MountStatus(id string) mount.Status
	RequestMount(id string)
	RequestUnmount(id string)
	OpenSettings()
	OpenPhoneLink()
	MountAll()
	UnmountAll()
	SetAutostart(enabled bool) error
	AutostartEnabled() bool
	Quit()
}

type Tray struct {
	app  fyne.App
	ctrl Controller
	desk desktop.App

	mu   sync.Mutex
	menu *fyne.Menu
}

func New(app fyne.App, ctrl Controller) *Tray {
	desk, ok := app.(desktop.App)
	if !ok {
		log.Fatal("tray: app does not support desktop mode")
	}
	t := &Tray{app: app, ctrl: ctrl, desk: desk}
	// Call rebuild() directly here - we're on the main goroutine, before a.Run().
	t.rebuild()
	return t
}

// Rebuild regenerates the tray menu from current connection state.
// Safe to call from any goroutine - marshals onto the Fyne thread.
func (t *Tray) Rebuild() {
	fyne.Do(func() { t.rebuild() })
}

func (t *Tray) rebuild() {
	t.mu.Lock()
	defer t.mu.Unlock()

	conns := t.ctrl.Connections()
	var items []*fyne.MenuItem

	for _, c := range conns {
		conn := c // capture
		status := t.ctrl.MountStatus(conn.ID)
		dot := dotChar(status.State)
		label := fmt.Sprintf("%s %s (%s:)", dot, conn.Name, conn.DriveLetter)
		item := fyne.NewMenuItem(label, func() {
			s := t.ctrl.MountStatus(conn.ID)
			if s.State == mount.StateMounted {
				t.ctrl.RequestUnmount(conn.ID)
			} else if s.State == mount.StateIdle || s.State == mount.StateError {
				t.ctrl.RequestMount(conn.ID)
			}
			t.Rebuild()
		})
		items = append(items, item)
	}

	if len(conns) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
	}

	items = append(items,
		fyne.NewMenuItem("Settings", func() { t.ctrl.OpenSettings() }),
		fyne.NewMenuItem("Phone link...", func() { t.ctrl.OpenPhoneLink() }),
		fyne.NewMenuItem("Mount all", func() { t.ctrl.MountAll(); t.Rebuild() }),
		fyne.NewMenuItem("Unmount all", func() { t.ctrl.UnmountAll(); t.Rebuild() }),
		fyne.NewMenuItemSeparator(),
	)

	autostartLabel := "Start with Windows"
	if t.ctrl.AutostartEnabled() {
		autostartLabel = "On - Start with Windows"
	}
	items = append(items, fyne.NewMenuItem(autostartLabel, func() {
		enabled := t.ctrl.AutostartEnabled()
		if err := t.ctrl.SetAutostart(!enabled); err != nil {
			log.Printf("autostart toggle: %v", err)
		}
		t.Rebuild()
	}))

	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { t.ctrl.Quit() }),
	)

	t.menu = fyne.NewMenu("wintp", items...)
	t.desk.SetSystemTrayMenu(t.menu)
	t.desk.SetSystemTrayIcon(aggregateIcon(t.ctrl))
}

func dotChar(s mount.State) string {
	switch s {
	case mount.StateMounted:
		return "[M]"
	case mount.StateMounting, mount.StateUnmounting:
		return "[...]"
	case mount.StateError:
		return "[X]"
	default:
		return "[ ]"
	}
}

func aggregateIcon(ctrl Controller) fyne.Resource {
	conns := ctrl.Connections()
	hasError := false
	hasMounted := false
	for _, c := range conns {
		s := ctrl.MountStatus(c.ID)
		switch s.State {
		case mount.StateMounted:
			hasMounted = true
		case mount.StateError:
			hasError = true
		}
	}
	switch {
	case hasError:
		return iconError
	case hasMounted:
		return iconActive
	default:
		return iconIdle
	}
}

// Placeholder icon resources - replaced by real PNGs at build time.
var (
	iconIdle   = fyne.NewStaticResource("icon_idle.png", iconIdleBytes)
	iconActive = fyne.NewStaticResource("icon_active.png", iconActiveBytes)
	iconError  = fyne.NewStaticResource("icon_error.png", iconErrorBytes)
)
