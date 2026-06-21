package ui

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"wintp/internal/mount"
	"wintp/internal/store"
	"wintp/internal/util"
)


var rcloneSizeOrDurationPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?([KMGTPEkmgtpe]i?[Bb]?|[Bb]|ms|[smhdwMy])?$`)

// SettingsController is the interface the window uses to talk to the app coordinator.
type SettingsController interface {
	Connections() []*store.Connection
	MountStatus(id string) mount.Status
	RequestMount(id string)
	RequestUnmount(id string)
	AddConnection(conn *store.Connection, password string) error
	UpdateConnection(conn *store.Connection, password string) error
	DeleteConnection(id string) error
	Password(id string) (string, error)
	TestConnection(conn *store.Connection, password string) error
	OAuthAuthorized(id string) bool
	AuthorizeConnection(conn *store.Connection, rcloneType string) error
	RcloneBin() string
	RcloneConf() string
	KnownHostsPath() string
	AddKnownHost(info *util.HostKeyInfo) error
	OnSettingsChange()
}

// MainWindow is the single persistent connections/settings window.
type MainWindow struct {
	win  fyne.Window
	ctrl SettingsController

	// list state
	conns       []*store.Connection
	selectedIdx int // -1=none, ==len(conns)=new

	list *widget.List

	// fixed editor widgets (always present)
	editorTitle    *widget.Label
	providerSel    *widget.Select // provider selector
	nameEntry      *widget.Entry
	pathEntry      *widget.Entry
	driveSelect    *widget.Select
	cacheSelect    *widget.Select
	autoMountChk   *widget.Check
	autoReconChk   *widget.Check
	testStatusLbl  *widget.Label
	oauthStatusLbl *widget.Label
	mountBtn       *widget.Button
	saveBtn        *widget.Button
	cancelBtn      *widget.Button
	deleteBtn      *widget.Button

	// VFS / mount tuning widgets
	cacheMaxSizeEntry  *widget.Entry
	cacheMaxAgeEntry   *widget.Entry
	dirCacheTimeEntry  *widget.Entry
	bufferSizeEntry    *widget.Entry
	readChunkSizeEntry *widget.Entry

	// dynamic provider-specific form
	providerDef    *ProviderDef
	providerFields map[string]*widget.Entry // key -> entry widget
	fieldForm      *widget.Form             // rebuilt per provider
	fieldFormWrap  *fyne.Container          // container.NewMax holding fieldForm

	// right panel switcher
	rightStack    *fyne.Container
	hintContent   fyne.CanvasObject
	editorContent fyne.CanvasObject
}

// OpenMainWindow creates the window (hidden) and returns the handle.
func OpenMainWindow(a fyne.App, ctrl SettingsController) *MainWindow {
	mw := &MainWindow{ctrl: ctrl, selectedIdx: -1}
	mw.conns = ctrl.Connections()
	mw.initFixedWidgets()
	mw.buildWindow(a)
	return mw
}

func (mw *MainWindow) Show() {
	mw.win.Show()
	mw.win.RequestFocus()
}

// Window returns the underlying Fyne window for use by callers that need a
// dialog parent.
func (mw *MainWindow) Window() fyne.Window { return mw.win }

// Refresh re-loads the connection list and updates mount button. Safe from any goroutine.
func (mw *MainWindow) Refresh() {
	fyne.Do(func() {
		mw.conns = mw.ctrl.Connections()
		mw.list.Refresh()
		if mw.selectedIdx >= 0 && mw.selectedIdx < len(mw.conns) {
			mw.refreshMountBtn(mw.conns[mw.selectedIdx])
		}
	})
}

// window construction

func (mw *MainWindow) buildWindow(a fyne.App) {
	mw.win = a.NewWindow("wintp")
	mw.win.Resize(fyne.NewSize(900, 580))
	mw.win.SetCloseIntercept(mw.win.Hide)

	mw.hintContent = mw.buildHint()
	mw.fieldFormWrap = container.NewStack()
	mw.editorContent = mw.buildEditorShell()
	mw.rightStack = container.NewStack(mw.hintContent)

	split := container.NewHSplit(
		mw.buildLeft(),
		container.NewPadded(mw.rightStack),
	)
	split.Offset = 0.32
	mw.win.SetContent(split)
}

func (mw *MainWindow) buildLeft() fyne.CanvasObject {
	heading := widget.NewLabelWithStyle("Connections", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	mw.list = widget.NewList(
		func() int { return len(mw.conns) },
		func() fyne.CanvasObject {
			return widget.NewLabel("[ ] Placeholder   X:")
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id >= len(mw.conns) {
				return
			}
			c := mw.conns[id]
			st := mw.ctrl.MountStatus(c.ID)
			o.(*widget.Label).SetText(statusDot(st.State) + " " + c.Name + "   " + c.DriveLetter + ":")
		},
	)
	mw.list.OnSelected = func(id widget.ListItemID) {
		if id < len(mw.conns) {
			mw.showEditor(id)
		}
	}

	addBtn := widget.NewButtonWithIcon("Add Connection", theme.ContentAddIcon(), func() {
		mw.list.UnselectAll()
		mw.showNewEditor()
	})

	return container.NewBorder(
		container.NewPadded(heading),
		container.NewPadded(addBtn),
		nil, nil,
		mw.list,
	)
}

func (mw *MainWindow) buildHint() fyne.CanvasObject {
	lbl := widget.NewLabel("Select a connection to edit,\nor click Add Connection to create one.")
	lbl.Alignment = fyne.TextAlignCenter
	lbl.Wrapping = fyne.TextWrapWord
	return container.NewCenter(lbl)
}

func (mw *MainWindow) initFixedWidgets() {
	mw.editorTitle = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	// Build provider name list in group order
	var providerNames []string
	for _, group := range ProviderGroups {
		for _, p := range Providers {
			if p.Group == group {
				providerNames = append(providerNames, p.Name)
			}
		}
	}

	mw.providerSel = widget.NewSelect(providerNames, nil)
	mw.providerSel.OnChanged = func(name string) {
		def := FindProviderByName(name)
		if def == nil {
			return
		}
		mw.rebuildProviderFields(def, nil)
	}

	mw.nameEntry = widget.NewEntry()
	mw.nameEntry.PlaceHolder = "My Server"

	mw.pathEntry = widget.NewEntry()
	mw.pathEntry.SetText("/")

	mw.driveSelect = widget.NewSelect(nil, nil)

	mw.cacheSelect = widget.NewSelect(
		[]string{string(store.CacheModeOff), string(store.CacheModeMinimal),
			string(store.CacheModeWrites), string(store.CacheModeFull)},
		nil,
	)

	mw.autoMountChk = widget.NewCheck("Auto-mount on startup", nil)
	mw.autoReconChk = widget.NewCheck("Auto-reconnect on error", nil)

	mw.testStatusLbl = widget.NewLabel("")
	mw.testStatusLbl.Wrapping = fyne.TextWrapWord

	mw.oauthStatusLbl = widget.NewLabel("")
	mw.oauthStatusLbl.Wrapping = fyne.TextWrapWord

	mw.mountBtn = widget.NewButton("Mount", nil)
	mw.mountBtn.Importance = widget.SuccessImportance

	mw.saveBtn = widget.NewButton("Save", nil)
	mw.saveBtn.Importance = widget.HighImportance

	mw.cancelBtn = widget.NewButton("Cancel", nil)

	mw.deleteBtn = widget.NewButton("Delete", nil)
	mw.deleteBtn.Importance = widget.DangerImportance

	mw.cacheMaxSizeEntry = widget.NewEntry()
	mw.cacheMaxSizeEntry.PlaceHolder = "e.g. 10G  (empty = unlimited)"

	mw.cacheMaxAgeEntry = widget.NewEntry()
	mw.cacheMaxAgeEntry.PlaceHolder = "e.g. 1h  (empty = rclone default)"

	mw.dirCacheTimeEntry = widget.NewEntry()
	mw.dirCacheTimeEntry.PlaceHolder = "e.g. 5m  (empty = rclone default)"

	mw.bufferSizeEntry = widget.NewEntry()
	mw.bufferSizeEntry.PlaceHolder = "e.g. 16M  (empty = rclone default)"

	mw.readChunkSizeEntry = widget.NewEntry()
	mw.readChunkSizeEntry.PlaceHolder = "e.g. 128M  (empty = rclone default)"
}

// buildEditorShell builds the static editor chrome; the dynamic form area is in fieldFormWrap.
func (mw *MainWindow) buildEditorShell() fyne.CanvasObject {
	topForm := widget.NewForm(
		widget.NewFormItem("Name", mw.nameEntry),
		widget.NewFormItem("Provider", mw.providerSel),
	)

	bottomForm := widget.NewForm(
		widget.NewFormItem("Remote Path", mw.pathEntry),
		widget.NewFormItem("Drive Letter", mw.driveSelect),
		widget.NewFormItem("VFS Cache Mode", mw.cacheSelect),
	)

	cacheForm := widget.NewForm(
		widget.NewFormItem("Max Cache Size", mw.cacheMaxSizeEntry),
		widget.NewFormItem("Max Cache Age", mw.cacheMaxAgeEntry),
		widget.NewFormItem("Dir Cache Time", mw.dirCacheTimeEntry),
		widget.NewFormItem("Buffer Size", mw.bufferSizeEntry),
		widget.NewFormItem("Read Chunk Size", mw.readChunkSizeEntry),
	)
	cacheSection := widget.NewAccordion(
		widget.NewAccordionItem("Advanced Cache Settings", cacheForm),
	)

	testBtn := widget.NewButton("Test Connection", mw.testConnection)
	testRow := container.NewBorder(nil, nil, nil, testBtn, mw.testStatusLbl)

	authorizeBtn := widget.NewButton("Authorize in Browser", mw.doOAuthAuthorize)
	authorizeBtn.Importance = widget.SuccessImportance
	oauthSection := container.NewVBox(authorizeBtn, mw.oauthStatusLbl)

	scrollable := container.NewScroll(container.NewVBox(
		topForm,
		widget.NewSeparator(),
		mw.fieldFormWrap, // provider-specific fields live here
		oauthSection,
		widget.NewSeparator(),
		bottomForm,
		cacheSection,
		mw.autoMountChk,
		mw.autoReconChk,
	))

	return container.NewBorder(
		container.NewVBox(mw.editorTitle, widget.NewSeparator(), mw.mountBtn),
		container.NewVBox(
			widget.NewSeparator(),
			testRow,
			widget.NewSeparator(),
			container.NewHBox(mw.saveBtn, mw.cancelBtn, mw.deleteBtn),
		),
		nil, nil,
		scrollable,
	)
}

// rebuildProviderFields swaps in a new form for the selected provider.
// existing is an optional Config map to pre-fill from (for edit mode); nil uses defaults.
func (mw *MainWindow) rebuildProviderFields(def *ProviderDef, existing map[string]string) {
	mw.providerDef = def
	mw.providerFields = make(map[string]*widget.Entry)

	var items []*widget.FormItem
	for _, f := range def.Fields {
		e := widget.NewEntry()
		if f.IsPassword {
			e = widget.NewPasswordEntry()
		}
		if existing != nil {
			if f.IsPassword {
				// password comes from cred manager, populated separately
			} else if v, ok := existing[f.Key]; ok {
				e.SetText(v)
			} else if f.Default != "" {
				e.SetText(f.Default)
			}
		} else {
			e.SetText(f.Default)
		}
		if f.Placeholder != "" {
			e.PlaceHolder = f.Placeholder
		}
		mw.providerFields[f.Key] = e
		items = append(items, widget.NewFormItem(f.Label, e))
	}

	mw.fieldForm = widget.NewForm(items...)
	mw.fieldFormWrap.Objects = []fyne.CanvasObject{mw.fieldForm}
	mw.fieldFormWrap.Refresh()

	// Show/hide the OAuth section and test button based on auth type.
	// We rebuild the editor shell so toggle visibility via oauthStatusLbl parent;
	// for simplicity just update the label text so the section is always rendered
	// but the Authorize button is only meaningful for OAuth.
	if def.IsOAuth {
		mw.testStatusLbl.SetText("(use Authorize to verify OAuth credentials)")
	} else {
		mw.testStatusLbl.SetText("")
		mw.oauthStatusLbl.SetText("")
	}
}

// editor show / hide

func (mw *MainWindow) showEditor(idx int) {
	mw.selectedIdx = idx
	c := mw.conns[idx]

	mw.editorTitle.SetText("Edit - " + c.Name)
	mw.nameEntry.SetText(c.Name)
	mw.pathEntry.SetText(c.RemotePath)
	mw.autoMountChk.SetChecked(c.AutoMount)
	mw.autoReconChk.SetChecked(c.AutoReconnect)
	mw.cacheSelect.SetSelected(string(c.CacheMode))

	// Determine the UI type (prefer _uiType from Config, fall back to Provider).
	uiType := c.Config[store.ConfigKeyUIType]
	if uiType == "" {
		uiType = c.Provider
	}
	def := FindProvider(uiType)
	if def == nil && len(Providers) > 0 {
		def = &Providers[0]
	}
	if def != nil {
		mw.providerSel.SetSelected(def.Name)
		mw.rebuildProviderFields(def, c.Config)
		// Load password into the password field.
		if def.PasswordField != "" {
			if pw, err := mw.ctrl.Password(c.ID); err == nil {
				if e, ok := mw.providerFields[def.PasswordField]; ok {
					e.SetText(pw)
				}
			}
		}
		if def.IsOAuth {
			if mw.ctrl.RcloneBin() != "" {
				if mw.ctrl.OAuthAuthorized(c.ID) {
					mw.oauthStatusLbl.SetText("Authorized - reconnect if token expired")
				} else {
					mw.oauthStatusLbl.SetText("Not yet authorized - click Authorize to connect")
				}
			}
		}
	}

	// Drive letter - always include the current one even if in use.
	drives := util.FreeDriveLetters()
	hasCurrent := false
	for _, d := range drives {
		if d == c.DriveLetter {
			hasCurrent = true
			break
		}
	}
	if !hasCurrent && c.DriveLetter != "" {
		drives = append([]string{c.DriveLetter}, drives...)
	}
	mw.driveSelect.Options = drives
	mw.driveSelect.Refresh()
	mw.driveSelect.SetSelected(c.DriveLetter)

	mw.cacheMaxSizeEntry.SetText(c.CacheMaxSize)
	mw.cacheMaxAgeEntry.SetText(c.CacheMaxAge)
	mw.dirCacheTimeEntry.SetText(c.DirCacheTime)
	mw.bufferSizeEntry.SetText(c.BufferSize)
	mw.readChunkSizeEntry.SetText(c.ReadChunkSize)

	mw.refreshMountBtn(c)
	mw.testStatusLbl.SetText("")
	mw.deleteBtn.Show()
	mw.saveBtn.OnTapped = func() { mw.saveEdit() }
	mw.cancelBtn.OnTapped = func() { mw.cancelEdit() }
	mw.deleteBtn.OnTapped = func() { mw.deleteEdit() }

	mw.rightStack.Objects = []fyne.CanvasObject{mw.editorContent}
	mw.rightStack.Refresh()
}

func (mw *MainWindow) showNewEditor() {
	mw.selectedIdx = len(mw.conns)

	mw.editorTitle.SetText("Add Connection")
	mw.nameEntry.SetText("")
	mw.pathEntry.SetText("/")
	mw.autoMountChk.SetChecked(false)
	mw.autoReconChk.SetChecked(false)
	mw.cacheSelect.SetSelected(string(store.CacheModeFull))
	mw.testStatusLbl.SetText("")
	mw.oauthStatusLbl.SetText("")
	mw.mountBtn.Hide()
	mw.deleteBtn.Hide()
	mw.cacheMaxSizeEntry.SetText("")
	mw.cacheMaxAgeEntry.SetText("")
	mw.dirCacheTimeEntry.SetText("")
	mw.bufferSizeEntry.SetText("")
	mw.readChunkSizeEntry.SetText("")

	// Default to first provider.
	if len(Providers) > 0 {
		def := &Providers[0]
		mw.providerSel.SetSelected(def.Name)
		mw.rebuildProviderFields(def, nil)
	}

	drives := util.FreeDriveLetters()
	mw.driveSelect.Options = drives
	mw.driveSelect.Refresh()
	if len(drives) > 0 {
		mw.driveSelect.SetSelected(drives[0])
	}

	mw.saveBtn.OnTapped = func() { mw.saveNew() }
	mw.cancelBtn.OnTapped = func() { mw.cancelEdit() }

	mw.rightStack.Objects = []fyne.CanvasObject{mw.editorContent}
	mw.rightStack.Refresh()
}

func (mw *MainWindow) cancelEdit() {
	mw.selectedIdx = -1
	mw.list.UnselectAll()
	mw.rightStack.Objects = []fyne.CanvasObject{mw.hintContent}
	mw.rightStack.Refresh()
}

func (mw *MainWindow) refreshMountBtn(c *store.Connection) {
	st := mw.ctrl.MountStatus(c.ID)
	switch st.State {
	case mount.StateMounted:
		mw.mountBtn.SetText("Unmount " + c.DriveLetter + ":")
		mw.mountBtn.Importance = widget.DangerImportance
		mw.mountBtn.OnTapped = func() { mw.ctrl.RequestUnmount(c.ID) }
		mw.mountBtn.Show()
	case mount.StateMounting, mount.StateUnmounting:
		mw.mountBtn.SetText("Working...")
		mw.mountBtn.Importance = widget.MediumImportance
		mw.mountBtn.OnTapped = nil
		mw.mountBtn.Show()
	case mount.StateError:
		mw.mountBtn.SetText("Retry Mount " + c.DriveLetter + ":")
		mw.mountBtn.Importance = widget.WarningImportance
		mw.mountBtn.OnTapped = func() { mw.ctrl.RequestMount(c.ID) }
		mw.mountBtn.Show()
	default:
		mw.mountBtn.SetText("Mount " + c.DriveLetter + ":")
		mw.mountBtn.Importance = widget.SuccessImportance
		mw.mountBtn.OnTapped = func() { mw.ctrl.RequestMount(c.ID) }
		mw.mountBtn.Show()
	}
}

// form actions

func (mw *MainWindow) currentFormConn() *store.Connection {
	def := mw.providerDef
	cfg := make(map[string]string)

	// Merge provider preset config.
	if def != nil {
		for k, v := range def.PresetConfig {
			cfg[k] = v
		}
		cfg[store.ConfigKeyUIType] = def.Type
		if def.PassEnvSuffix != "" {
			cfg[store.ConfigKeyPassEnv] = def.PassEnvSuffix
		}
		if def.IsOAuth {
			cfg[store.ConfigKeyOAuth] = "true"
		}
	}

	// Merge form field values (skip password - goes to cred manager).
	if def != nil {
		for _, f := range def.Fields {
			if f.IsPassword {
				continue
			}
			if e, ok := mw.providerFields[f.Key]; ok {
				v := strings.TrimSpace(e.Text)
				if v != "" {
					cfg[f.Key] = v
				}
			}
		}
	}

	rcloneType := ""
	if def != nil {
		rcloneType = def.RcloneType
	}

	return &store.Connection{
		Name:          strings.TrimSpace(mw.nameEntry.Text),
		Provider:      rcloneType,
		Config:        cfg,
		DriveLetter:   mw.driveSelect.Selected,
		RemotePath:    mw.pathEntry.Text,
		CacheMode:     store.CacheMode(mw.cacheSelect.Selected),
		AutoMount:     mw.autoMountChk.Checked,
		AutoReconnect: mw.autoReconChk.Checked,
		CacheMaxSize:  strings.TrimSpace(mw.cacheMaxSizeEntry.Text),
		CacheMaxAge:   strings.TrimSpace(mw.cacheMaxAgeEntry.Text),
		DirCacheTime:  strings.TrimSpace(mw.dirCacheTimeEntry.Text),
		BufferSize:    strings.TrimSpace(mw.bufferSizeEntry.Text),
		ReadChunkSize: strings.TrimSpace(mw.readChunkSizeEntry.Text),
	}
}

func (mw *MainWindow) currentPassword() string {
	def := mw.providerDef
	if def == nil || def.PasswordField == "" {
		return ""
	}
	if e, ok := mw.providerFields[def.PasswordField]; ok {
		return e.Text
	}
	return ""
}

func (mw *MainWindow) validate() bool {
	if strings.TrimSpace(mw.nameEntry.Text) == "" {
		dialog.ShowError(fmt.Errorf("name is required"), mw.win)
		return false
	}
	if mw.driveSelect.Selected == "" {
		dialog.ShowError(fmt.Errorf("drive letter is required"), mw.win)
		return false
	}
	letter := mw.driveSelect.Selected
	if len(letter) != 1 || letter[0] < 'A' || letter[0] > 'Z' {
		dialog.ShowError(fmt.Errorf("drive letter must be a single letter A-Z"), mw.win)
		return false
	}
	// Ensure the drive letter is not already used by another connection.
	currentID := ""
	if mw.selectedIdx >= 0 && mw.selectedIdx < len(mw.conns) {
		currentID = mw.conns[mw.selectedIdx].ID
	}
	for _, c := range mw.ctrl.Connections() {
		if c.ID != currentID && strings.EqualFold(c.DriveLetter, letter) {
			dialog.ShowError(fmt.Errorf("drive letter %s: is already used by %q", letter, c.Name), mw.win)
			return false
		}
	}
	if mw.providerDef == nil {
		dialog.ShowError(fmt.Errorf("provider is required"), mw.win)
		return false
	}
	for _, f := range mw.providerDef.Fields {
		e, ok := mw.providerFields[f.Key]
		if !ok {
			continue
		}
		value := strings.TrimSpace(e.Text)
		if value == "" && !f.Optional {
			dialog.ShowError(fmt.Errorf("%s is required", f.Label), mw.win)
			return false
		}
		if value == "" {
			continue
		}
		if f.Key == "port" {
			p, err := strconv.Atoi(value)
			if err != nil || p < 1 || p > 65535 {
				dialog.ShowError(fmt.Errorf("%s must be a TCP port from 1 to 65535", f.Label), mw.win)
				return false
			}
		}
		if strings.Contains(strings.ToLower(f.Key), "url") {
			u, err := url.Parse(value)
			if err != nil || u.Scheme == "" || u.Host == "" {
				dialog.ShowError(fmt.Errorf("%s must be a valid URL", f.Label), mw.win)
				return false
			}
		}
	}
	if strings.TrimSpace(mw.pathEntry.Text) == "" {
		dialog.ShowError(fmt.Errorf("remote path is required"), mw.win)
		return false
	}
	for label, value := range map[string]string{
		"max cache size":  mw.cacheMaxSizeEntry.Text,
		"max cache age":   mw.cacheMaxAgeEntry.Text,
		"dir cache time":  mw.dirCacheTimeEntry.Text,
		"buffer size":     mw.bufferSizeEntry.Text,
		"read chunk size": mw.readChunkSizeEntry.Text,
	} {
		value = strings.TrimSpace(value)
		if value != "" && !rcloneSizeOrDurationPattern.MatchString(value) {
			dialog.ShowError(fmt.Errorf("%s has an unsupported format", label), mw.win)
			return false
		}
	}
	return true
}

func (mw *MainWindow) saveNew() {
	if !mw.validate() {
		return
	}
	conn := mw.currentFormConn()
	if err := mw.ctrl.AddConnection(conn, mw.currentPassword()); err != nil {
		dialog.ShowError(err, mw.win)
		return
	}
	mw.ctrl.OnSettingsChange()
	mw.conns = mw.ctrl.Connections()
	mw.list.Refresh()
	mw.cancelEdit()
}

func (mw *MainWindow) saveEdit() {
	if !mw.validate() || mw.selectedIdx < 0 || mw.selectedIdx >= len(mw.conns) {
		return
	}
	conn := mw.currentFormConn()
	conn.ID = mw.conns[mw.selectedIdx].ID
	if err := mw.ctrl.UpdateConnection(conn, mw.currentPassword()); err != nil {
		dialog.ShowError(err, mw.win)
		return
	}
	mw.ctrl.OnSettingsChange()
	mw.conns = mw.ctrl.Connections()
	mw.list.Refresh()
	mw.cancelEdit()
}

func (mw *MainWindow) deleteEdit() {
	if mw.selectedIdx < 0 || mw.selectedIdx >= len(mw.conns) {
		return
	}
	c := mw.conns[mw.selectedIdx]
	dialog.ShowConfirm(
		"Delete connection",
		fmt.Sprintf("Delete %q — This cannot be undone.", c.Name),
		func(ok bool) {
			if !ok {
				return
			}
			if err := mw.ctrl.DeleteConnection(c.ID); err != nil {
				dialog.ShowError(err, mw.win)
				return
			}
			mw.ctrl.OnSettingsChange()
			mw.conns = mw.ctrl.Connections()
			mw.list.Refresh()
			mw.cancelEdit()
		},
		mw.win,
	)
}

func (mw *MainWindow) testConnection() {
	def := mw.providerDef
	if def != nil && def.IsOAuth {
		mw.testStatusLbl.SetText("Use the Authorize button for OAuth providers")
		return
	}

	// Snapshot form data on the main thread before handing off to the worker.
	conn := mw.currentFormConn()
	pw := mw.currentPassword()
	mw.testStatusLbl.SetText("Testing...")
	log.Printf("test connection: provider=%s", conn.Provider)

	go func(conn *store.Connection, pw string) {
		err := mw.ctrl.TestConnection(conn, pw)
		fyne.Do(func() {
			if err != nil {
				var unknown *util.UnknownHostKeyError
				if errors.As(err, &unknown) {
					mw.promptHostKey(unknown.Info, func() {
						if addErr := mw.ctrl.AddKnownHost(unknown.Info); addErr != nil {
							mw.testStatusLbl.SetText(" " + addErr.Error())
							log.Printf("add known host failed: %v", addErr)
							return
						}
						mw.testConnection()
					})
					return
				}
				mw.testStatusLbl.SetText(" " + err.Error())
				log.Printf("test connection failed: %v", err)
			} else {
				mw.testStatusLbl.SetText("Connection OK")
				log.Println("test connection succeeded")
			}
		})
	}(conn, pw)
}

func (mw *MainWindow) promptHostKey(info *util.HostKeyInfo, onConfirm func()) {
	msg := fmt.Sprintf("The SSH server %s:%d presented a new host key:\n\n%s\n\nDo you want to trust it?", info.Host, info.Port, info.Fingerprint)
	d := dialog.NewConfirm("Trust host key?", msg, func(ok bool) {
		if ok {
			onConfirm()
		}
	}, mw.win)
	d.Show()
}

// doOAuthAuthorize saves the connection first (to get an ID) then runs the rclone
// OAuth browser flow and updates the remote config with the resulting token.
func (mw *MainWindow) doOAuthAuthorize() {
	if !mw.validate() {
		return
	}
	def := mw.providerDef
	if def == nil || !def.IsOAuth {
		return
	}

	mw.oauthStatusLbl.SetText("Saving connection...")

	var connID string
	if mw.selectedIdx >= 0 && mw.selectedIdx < len(mw.conns) {
		conn := mw.currentFormConn()
		conn.ID = mw.conns[mw.selectedIdx].ID
		connID = conn.ID
		if err := mw.ctrl.UpdateConnection(conn, ""); err != nil {
			mw.oauthStatusLbl.SetText("X Save failed: " + err.Error())
			return
		}
	} else {
		conn := mw.currentFormConn()
		if err := mw.ctrl.AddConnection(conn, ""); err != nil {
			mw.oauthStatusLbl.SetText("X Save failed: " + err.Error())
			return
		}
		connID = conn.ID
	}

	mw.oauthStatusLbl.SetText("Opening browser for authorization...")

	go func() {
		err := mw.ctrl.AuthorizeConnection(&store.Connection{ID: connID}, def.RcloneType)
		fyne.Do(func() {
			if err != nil {
				mw.oauthStatusLbl.SetText("X Authorization failed: " + err.Error())
				return
			}
			mw.oauthStatusLbl.SetText("Authorized")
			mw.ctrl.OnSettingsChange()
			mw.conns = mw.ctrl.Connections()
			mw.list.Refresh()
			for i, c := range mw.conns {
				if c.ID == connID {
					mw.showEditor(i)
					return
				}
			}
			mw.cancelEdit()
		})
	}()
}

// helpers


func statusDot(s mount.State) string {
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
