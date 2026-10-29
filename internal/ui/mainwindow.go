package ui

import (
	"errors"
	"fmt"
	"image/color"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"wintp/internal/mount"
	"wintp/internal/store"
	"wintp/internal/util"
)

var rcloneSizeOrDurationPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?([KMGTPEkmgtpe]i?[Bb]?|[Bb]|ms|[smhdwMy])?$`)

// SettingsController is the interface the window uses to talk to the app coordinator.
type SettingsController interface {
	PhoneController
	SyncController
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
	conns       []*store.Connection // currently displayed (filtered) connections
	selectedIdx int                 // -1=none, ==len(conns)=new

	list        *widget.List
	searchEntry *widget.Entry
	footerLbl   *canvas.Text
	footerDot   *canvas.Circle

	// phone link entry (sidebar row + right-hand panel)
	phoneRow   *phoneRow
	phonePanel *phonePanel

	// auto sync entry (sidebar row + right-hand panel)
	syncRow   *syncRow
	syncPanel *syncPanel

	// fixed editor widgets (always present)
	editorTitle     *canvas.Text
	editorSubtitle  *canvas.Text
	statusPill      *statusPill
	authorizeBtn    *widget.Button
	dangerCard      fyne.CanvasObject
	providerCards   []*providerCard // featured provider quick-pick grid
	providerSel     *widget.Select  // full catalog fallback ("more providers")
	nameEntry       *widget.Entry
	pathEntry       *widget.Entry
	driveSelect     *widget.Select
	cacheSeg        *segmentedControl
	cacheCaptionLbl *widget.Label
	autoMountToggle *toggleSwitch
	autoReconToggle *toggleSwitch
	testStatusLbl   *widget.Label
	oauthStatusLbl  *widget.Label
	mountBtn        *widget.Button
	mountBtnWrap    fyne.CanvasObject // short-button wrapper; Hide/Show this, not mountBtn, to collapse its space
	saveBtn         *widget.Button
	cancelBtn       *widget.Button
	deleteBtn       *widget.Button

	// VFS / mount tuning widgets
	cacheMaxSizeEntry  *widget.Entry
	cacheMaxAgeEntry   *widget.Entry
	dirCacheTimeEntry  *widget.Entry
	bufferSizeEntry    *widget.Entry
	readChunkSizeEntry *widget.Entry

	// dynamic provider-specific form
	providerDef    *ProviderDef
	providerFields map[string]*widget.Entry // key -> entry widget
	fieldForm      *fyne.Container          // rebuilt per provider; rows of one or two paired fields
	fieldFormWrap  *fyne.Container          // holds fieldForm

	// right panel switcher
	rightStack    *fyne.Container
	hintContent   fyne.CanvasObject
	editorContent fyne.CanvasObject
}

// OpenMainWindow creates the window (hidden) and returns the handle.
func OpenMainWindow(a fyne.App, ctrl SettingsController) *MainWindow {
	mw := &MainWindow{ctrl: ctrl, selectedIdx: -1}
	mw.conns = ctrl.Connections()
	// The window must exist before the panels: their dialogs use it as parent.
	mw.win = a.NewWindow("wintp")
	mw.phonePanel = newPhonePanel(ctrl, mw.win)
	mw.syncPanel = newSyncPanel(ctrl, func() fyne.Window { return mw.win })
	mw.initFixedWidgets()
	mw.buildWindow(a)
	mw.updateFooter()
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
		mw.phonePanel.Refresh()
		mw.phoneRow.Update()
		mw.syncPanel.Refresh()
		mw.syncRow.Update()
		mw.applyFilter()
		if mw.selectedIdx >= 0 && mw.selectedIdx < len(mw.conns) {
			mw.refreshMountBtn(mw.conns[mw.selectedIdx])
		}
	})
}

// applyFilter re-reads connections from the controller and narrows mw.conns
// to those matching the search box, then refreshes the list and footer.
func (mw *MainWindow) applyFilter() {
	all := mw.ctrl.Connections()
	q := ""
	if mw.searchEntry != nil {
		q = strings.ToLower(strings.TrimSpace(mw.searchEntry.Text))
	}
	if q == "" {
		mw.conns = all
	} else {
		filtered := make([]*store.Connection, 0, len(all))
		for _, c := range all {
			if strings.Contains(strings.ToLower(c.Name), q) ||
				strings.Contains(strings.ToLower(providerDisplayName(c)), q) {
				filtered = append(filtered, c)
			}
		}
		mw.conns = filtered
	}
	if mw.list != nil {
		mw.list.Refresh()
	}
	mw.updateFooter()
}

// updateFooter sets the sidebar's "N connections · M mounted" summary line,
// always counting the full (unfiltered) connection set.
func (mw *MainWindow) updateFooter() {
	if mw.footerLbl == nil {
		return
	}
	all := mw.ctrl.Connections()
	mounted := 0
	for _, c := range all {
		if mw.ctrl.MountStatus(c.ID).State == mount.StateMounted {
			mounted++
		}
	}
	word := "connections"
	if len(all) == 1 {
		word = "connection"
	}
	mountedText := fmt.Sprintf("%d mounted", mounted)
	if mounted == 0 {
		mountedText = "none mounted"
	}
	setText(mw.footerLbl, fmt.Sprintf("%d %s  ·  %s", len(all), word, mountedText))
	if mounted > 0 {
		mw.footerDot.FillColor = colSuccess
	} else {
		mw.footerDot.FillColor = colFaint
	}
	mw.footerDot.Refresh()
}

// window construction

const sidebarWidth = 284

func (mw *MainWindow) buildWindow(a fyne.App) {
	mw.win.Resize(fyne.NewSize(1040, 680))
	mw.win.SetCloseIntercept(mw.win.Hide)
	mw.win.SetPadded(false)

	mw.hintContent = mw.buildHint()
	mw.fieldFormWrap = container.NewStack()
	mw.editorContent = mw.buildEditorShell()
	mw.rightStack = container.NewStack(mw.hintContent)

	edge := canvas.NewRectangle(colBorder)
	edge.SetMinSize(fyne.NewSize(1, 1))
	sidebar := container.NewBorder(nil, nil, nil, edge,
		container.New(fixedWidthLayout{width: sidebarWidth}, mw.buildLeft()))

	mw.win.SetContent(container.NewBorder(nil, nil, sidebar, nil, mw.rightStack))
}

// connRowItem is one entry in the sidebar connection list.
type connRowItem struct {
	widget.BaseWidget
	badge *badge
	name  *widget.Label
	sub   *widget.Label
	dot   *canvas.Circle
	root  fyne.CanvasObject
}

func newConnRowItem() *connRowItem {
	r := &connRowItem{
		badge: newBadge("", 32),
		name:  widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		sub:   widget.NewLabel(""),
		dot:   canvas.NewCircle(colFaint),
	}
	r.name.Truncation = fyne.TextTruncateEllipsis
	r.sub.Truncation = fyne.TextTruncateEllipsis
	r.sub.Importance = widget.LowImportance
	r.sub.SizeName = theme.SizeNameCaptionText
	dot := container.NewCenter(container.NewGridWrap(fyne.NewSize(8, 8), r.dot))
	r.root = inset(7, 7, 8, 10, container.NewBorder(nil, nil,
		inset(0, 0, 0, 10, container.NewCenter(r.badge.root)), inset(0, 0, 8, 0, dot),
		vbox(2, unpadded(r.name), unpadded(r.sub))))
	r.ExtendBaseWidget(r)
	return r
}

func (r *connRowItem) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.root)
}

func (mw *MainWindow) buildLeft() fyne.CanvasObject {
	logoBg := canvas.NewRectangle(colAccent)
	logoBg.CornerRadius = 8
	logoTxt := newText("W", 15, color.White, true)
	logo := container.NewGridWrap(fyne.NewSize(30, 30), container.NewStack(logoBg, container.NewCenter(logoTxt)))
	brand := vbox(1, newText("wintp", 15, colText, true), newText("Network drives", 11, colMuted, false))

	addBtn := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		mw.list.UnselectAll()
		mw.showNewEditor()
	})
	addBtn.Importance = widget.LowImportance
	brandRow := container.NewBorder(nil, nil,
		hbox(10, logo, container.NewCenter(brand)), container.NewCenter(addBtn))

	mw.searchEntry = widget.NewEntry()
	mw.searchEntry.PlaceHolder = "Search connections"
	mw.searchEntry.ActionItem = widget.NewIcon(theme.SearchIcon())
	mw.searchEntry.OnChanged = func(string) {
		mw.cancelEdit()
		mw.applyFilter()
	}

	mw.phoneRow = newPhoneRow(mw.ctrl, mw.ShowPhone)
	mw.phonePanel.onChange = mw.phoneRow.Update
	mw.syncRow = newSyncRow(mw.ctrl, mw.ShowAutoSync)
	mw.syncPanel.onChange = func() {
		mw.syncRow.Update()
		mw.ctrl.OnSettingsChange()
	}

	mw.list = widget.NewList(
		func() int { return len(mw.conns) },
		func() fyne.CanvasObject { return newConnRowItem() },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id >= len(mw.conns) {
				return
			}
			c := mw.conns[id]
			r := o.(*connRowItem)
			r.badge.SetText(badgeFor(providerForConn(c)))
			r.dot.FillColor = statusColor(mw.ctrl.MountStatus(c.ID).State)
			r.dot.Refresh()
			r.name.SetText(c.Name)
			r.sub.SetText(c.DriveLetter + ":  ·  " + providerDisplayName(c))
		},
	)
	mw.list.HideSeparators = true
	mw.list.OnSelected = func(id widget.ListItemID) {
		if id < len(mw.conns) {
			mw.showEditor(id)
		}
	}

	top := vbox(8,
		inset(18, 6, 16, 12, brandRow),
		inset(0, 6, 12, 12, mw.searchEntry),
		inset(0, 0, 16, 16, overline("PHONE & SYNC")),
		inset(0, 0, 8, 8, vbox(2, mw.phoneRow.Object(), mw.syncRow.Object())),
		inset(6, 0, 16, 16, overline("CONNECTIONS")),
	)

	mw.footerLbl = newText("", 11.5, colMuted, false)
	mw.footerDot = canvas.NewCircle(colFaint)
	footer := container.NewVBox(
		divider(),
		inset(12, 12, 18, 16, hbox(8,
			container.NewCenter(container.NewGridWrap(fyne.NewSize(7, 7), mw.footerDot)),
			mw.footerLbl,
		)),
	)

	bg := canvas.NewRectangle(colSidebar)
	return container.NewStack(bg, container.NewBorder(top, footer, nil, nil, inset(0, 0, 8, 8, mw.list)))
}

func (mw *MainWindow) buildHint() fyne.CanvasObject {
	tileBg := canvas.NewRectangle(colAccentSoft)
	tileBg.CornerRadius = 14
	icon := widget.NewIcon(theme.StorageIcon())
	tile := container.NewGridWrap(fyne.NewSize(60, 60),
		container.NewStack(tileBg, inset(14, 14, 14, 14, icon)))

	title := newText("No connection selected", 17, colText, true)
	title.Alignment = fyne.TextAlignCenter
	body1 := newText("Pick a connection on the left to edit it,", 12.5, colMuted, false)
	body2 := newText("or add one to mount a server as a drive.", 12.5, colMuted, false)
	body1.Alignment, body2.Alignment = fyne.TextAlignCenter, fyne.TextAlignCenter

	newBtn := widget.NewButtonWithIcon("New connection", theme.ContentAddIcon(), func() {
		mw.list.UnselectAll()
		mw.showNewEditor()
	})
	newBtn.Importance = widget.HighImportance

	return container.NewCenter(vbox(6,
		container.NewCenter(tile),
		inset(12, 0, 0, 0, title),
		body1, body2,
		inset(14, 0, 0, 0, container.NewCenter(newBtn)),
	))
}

// cacheModeCaptions gives each VFS cache mode a one-line explanation shown
// under the segmented control, matching rclone's --vfs-cache-mode semantics.
var cacheModeCaptions = map[string]string{
	string(store.CacheModeOff):     "No local cache. Lowest memory use, slowest for repeated reads.",
	string(store.CacheModeMinimal): "Caches file structure only. Good default for most servers.",
	string(store.CacheModeWrites):  "Caches writes on disk. Needed by apps that seek while writing.",
	string(store.CacheModeFull):    "Caches reads and writes on disk. Best compatibility with Office and editors.",
}

func (mw *MainWindow) initFixedWidgets() {
	mw.editorTitle = newText("", 20, colText, true)
	mw.editorSubtitle = newText("", 12.5, colMuted, false)
	mw.statusPill = newStatusPill()

	// Full catalog fallback dropdown, in group order, for providers not in
	// the featured quick-pick grid.
	var providerNames []string
	for _, group := range ProviderGroups {
		for _, p := range Providers {
			if p.Group == group {
				providerNames = append(providerNames, p.Name)
			}
		}
	}

	mw.providerSel = widget.NewSelect(providerNames, nil)
	mw.providerSel.PlaceHolder = "More providers…"
	mw.providerSel.OnChanged = func(name string) {
		def := FindProviderByName(name)
		if def == nil {
			return
		}
		mw.applyProviderSelection(def, nil)
		setText(mw.editorSubtitle, def.Name+"  →  "+mw.driveSelect.Selected+":")
	}

	mw.providerCards = nil
	for _, def := range FeaturedProviders() {
		def := def
		mw.providerCards = append(mw.providerCards, newProviderCard(def, func() {
			mw.applyProviderSelection(def, nil)
			setText(mw.editorSubtitle, def.Name+"  →  "+mw.driveSelect.Selected+":")
		}))
	}

	mw.nameEntry = widget.NewEntry()
	mw.nameEntry.PlaceHolder = "My server"

	mw.pathEntry = widget.NewEntry()
	mw.pathEntry.SetText("/")

	mw.driveSelect = widget.NewSelect(nil, nil)

	mw.cacheCaptionLbl = widget.NewLabel("")
	mw.cacheCaptionLbl.Importance = widget.LowImportance
	mw.cacheCaptionLbl.SizeName = theme.SizeNameCaptionText
	mw.cacheCaptionLbl.Wrapping = fyne.TextWrapWord

	mw.cacheSeg = newSegmentedControl(
		[]string{string(store.CacheModeOff), string(store.CacheModeMinimal),
			string(store.CacheModeWrites), string(store.CacheModeFull)},
		func(v string) { mw.cacheCaptionLbl.SetText(cacheModeCaptions[v]) },
	)

	mw.autoMountToggle = newToggleSwitch(false, nil)
	mw.autoReconToggle = newToggleSwitch(false, nil)

	mw.testStatusLbl = widget.NewLabel("")
	mw.testStatusLbl.Wrapping = fyne.TextWrapWord
	mw.testStatusLbl.SizeName = theme.SizeNameCaptionText

	mw.oauthStatusLbl = widget.NewLabel("")
	mw.oauthStatusLbl.Wrapping = fyne.TextWrapWord
	mw.oauthStatusLbl.SizeName = theme.SizeNameCaptionText

	mw.mountBtn = widget.NewButtonWithIcon("Mount", theme.MediaPlayIcon(), nil)

	mw.saveBtn = widget.NewButtonWithIcon("Save changes", theme.ConfirmIcon(), nil)
	mw.saveBtn.Importance = widget.HighImportance

	mw.cancelBtn = widget.NewButton("Cancel", nil)

	mw.deleteBtn = widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), nil)
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

// setText updates a canvas.Text in place.
func setText(t *canvas.Text, s string) {
	t.Text = s
	t.Refresh()
}

// pageHeader is the title bar at the top of a right-hand panel.
func pageHeader(title, subtitle fyne.CanvasObject, trailing fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(
		inset(22, 18, 28, 24, container.NewBorder(nil, nil, nil, trailing, vbox(5, title, subtitle))),
		divider(),
	)
}

// pageBody wraps a panel's cards in a scrolling, padded column.
func pageBody(cards ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVScroll(inset(22, 28, 28, 28, vbox(16, cards...)))
}

// buildEditorShell builds the static editor chrome; the dynamic form area is in fieldFormWrap.
func (mw *MainWindow) buildEditorShell() fyne.CanvasObject {
	cardsGrid := container.NewGridWithColumns(3)
	for _, c := range mw.providerCards {
		cardsGrid.Add(c.Object())
	}

	testBtn := widget.NewButtonWithIcon("Test connection", theme.ViewRefreshIcon(), mw.testConnection)
	mw.authorizeBtn = widget.NewButtonWithIcon("Authorize in browser", theme.LoginIcon(), mw.doOAuthAuthorize)
	mw.authorizeBtn.Importance = widget.HighImportance

	connectionCard := card("Connection", "Give it a name and choose where the files live.",
		field("Name", mw.nameEntry),
		field("Provider", vbox(10, cardsGrid, mw.providerSel)),
	)

	signInCard := card("Sign in", "Passwords are stored in Windows Credential Manager.",
		mw.fieldFormWrap, // provider-specific fields live here
		container.NewBorder(nil, nil, hbox(8, testBtn, mw.authorizeBtn), nil,
			inset(0, 0, 12, 0, container.NewVBox(layout.NewSpacer(), unpadded(mw.testStatusLbl), layout.NewSpacer()))),
		mw.oauthStatusLbl,
	)

	mountCard := card("Mount", "How the drive shows up in File Explorer.",
		container.NewGridWithColumns(2,
			field("Remote path", mw.pathEntry),
			field("Drive letter", mw.driveSelect),
		),
		field("VFS cache", vbox(8, mw.cacheSeg.CanvasObject(), unpadded(mw.cacheCaptionLbl))),
		divider(),
		settingsRow("Mount on startup", "Connect when wintp launches.", mw.autoMountToggle),
		divider(),
		settingsRow("Reconnect on error", "Retry automatically if the connection drops.", mw.autoReconToggle),
	)

	advanced := widget.NewAccordion(widget.NewAccordionItem("Advanced cache settings",
		inset(8, 4, 0, 0, vbox(12,
			container.NewGridWithColumns(2,
				field("Max cache size", mw.cacheMaxSizeEntry),
				field("Max cache age", mw.cacheMaxAgeEntry),
			),
			container.NewGridWithColumns(2,
				field("Dir cache time", mw.dirCacheTimeEntry),
				field("Buffer size", mw.bufferSizeEntry),
			),
			container.NewGridWithColumns(2,
				field("Read chunk size", mw.readChunkSizeEntry),
				layout.NewSpacer(),
			),
		))))
	advancedCard := card("", "", advanced)

	mw.dangerCard = card("", "",
		settingsRow("Delete connection", "Removes it from wintp along with its saved password.", mw.deleteBtn))

	const actionBtnHeight = 34
	mw.mountBtnWrap = shortButton(actionBtnHeight, mw.mountBtn)
	actions := container.NewCenter(hbox(8,
		mw.mountBtnWrap,
		shortButton(actionBtnHeight, mw.cancelBtn),
		shortButton(actionBtnHeight, mw.saveBtn),
	))

	titleRow := hbox(12, mw.editorTitle, mw.statusPill.root)
	header := pageHeader(titleRow, mw.editorSubtitle, actions)

	return container.NewBorder(header, nil, nil, nil,
		pageBody(connectionCard, signInCard, mountCard, advancedCard, mw.dangerCard))
}

// setProviderSelection reflects def as the chosen provider across both the
// featured card grid and the "more providers" dropdown, keeping them
// mutually exclusive without re-triggering each other's OnChanged handler.
func (mw *MainWindow) setProviderSelection(def *ProviderDef) {
	featured := false
	for _, card := range mw.providerCards {
		sel := card.title == def.Name
		card.SetSelected(sel)
		if sel {
			featured = true
		}
	}
	switch {
	case featured && mw.providerSel.Selected != "":
		mw.providerSel.ClearSelected()
	case !featured && mw.providerSel.Selected != def.Name:
		mw.providerSel.SetSelected(def.Name)
	}
}

// applyProviderSelection selects def in the picker and rebuilds the
// provider-specific form fields, optionally pre-filled from existing.
func (mw *MainWindow) applyProviderSelection(def *ProviderDef, existing map[string]string) {
	mw.setProviderSelection(def)
	mw.rebuildProviderFields(def, existing)
}

// rebuildProviderFields swaps in a new form for the selected provider.
// existing is an optional Config map to pre-fill from (for edit mode); nil uses defaults.
func (mw *MainWindow) rebuildProviderFields(def *ProviderDef, existing map[string]string) {
	mw.providerDef = def
	mw.providerFields = make(map[string]*widget.Entry)

	buildEntry := func(f FormField) *widget.Entry {
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
		return e
	}

	// Fields sharing a Row tag (e.g. host+port, user+pass) are laid out two
	// to a row instead of each spanning the full width.
	var rows []fyne.CanvasObject
	for i := 0; i < len(def.Fields); {
		f := def.Fields[i]
		item1 := field(f.Label, buildEntry(f))

		if f.Row != "" && i+1 < len(def.Fields) && def.Fields[i+1].Row == f.Row {
			f2 := def.Fields[i+1]
			item2 := field(f2.Label, buildEntry(f2))
			rows = append(rows, container.NewGridWithColumns(2, item1, item2))
			i += 2
			continue
		}

		rows = append(rows, item1)
		i++
	}

	mw.fieldForm = vbox(12, rows...)
	mw.fieldFormWrap.Objects = []fyne.CanvasObject{mw.fieldForm}
	mw.fieldFormWrap.Refresh()

	// Show/hide the OAuth section and test button based on auth type.
	// We rebuild the editor shell so toggle visibility via oauthStatusLbl parent;
	// for simplicity just update the label text so the section is always rendered
	// but the Authorize button is only meaningful for OAuth.
	if def.IsOAuth {
		mw.authorizeBtn.Show()
		mw.oauthStatusLbl.Show()
		mw.testStatusLbl.SetText("Use Authorize to verify OAuth credentials.")
	} else {
		mw.authorizeBtn.Hide()
		mw.oauthStatusLbl.Hide()
		mw.testStatusLbl.SetText("")
		mw.oauthStatusLbl.SetText("")
	}
}

// editor show / hide

func (mw *MainWindow) showEditor(idx int) {
	mw.phoneRow.SetSelected(false)
	mw.syncRow.SetSelected(false)
	mw.selectedIdx = idx
	c := mw.conns[idx]

	setText(mw.editorTitle, c.Name)
	setText(mw.editorSubtitle, providerDisplayName(c)+"  →  "+c.DriveLetter+":")
	mw.nameEntry.SetText(c.Name)
	mw.pathEntry.SetText(c.RemotePath)
	mw.autoMountToggle.SetChecked(c.AutoMount)
	mw.autoReconToggle.SetChecked(c.AutoReconnect)
	mw.cacheSeg.SetSelected(string(c.CacheMode))
	mw.cacheCaptionLbl.SetText(cacheModeCaptions[string(c.CacheMode)])

	def := providerForConn(c)
	if def == nil && len(Providers) > 0 {
		def = &Providers[0]
	}
	if def != nil {
		mw.applyProviderSelection(def, c.Config)
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
	mw.dangerCard.Show()
	mw.saveBtn.SetText("Save changes")
	mw.saveBtn.OnTapped = func() { mw.saveEdit() }
	mw.cancelBtn.OnTapped = func() { mw.cancelEdit() }
	mw.deleteBtn.OnTapped = func() { mw.deleteEdit() }

	mw.rightStack.Objects = []fyne.CanvasObject{mw.editorContent}
	mw.rightStack.Refresh()
}

func (mw *MainWindow) showNewEditor() {
	mw.phoneRow.SetSelected(false)
	mw.syncRow.SetSelected(false)
	mw.selectedIdx = len(mw.conns)

	setText(mw.editorTitle, "New connection")
	mw.nameEntry.SetText("")
	mw.pathEntry.SetText("/")
	mw.autoMountToggle.SetChecked(false)
	mw.autoReconToggle.SetChecked(false)
	mw.cacheSeg.SetSelected(string(store.CacheModeFull))
	mw.cacheCaptionLbl.SetText(cacheModeCaptions[string(store.CacheModeFull)])
	mw.testStatusLbl.SetText("")
	mw.oauthStatusLbl.SetText("")
	mw.mountBtnWrap.Hide()
	mw.dangerCard.Hide()
	mw.statusPill.root.Hide()
	mw.saveBtn.SetText("Create")
	mw.cacheMaxSizeEntry.SetText("")
	mw.cacheMaxAgeEntry.SetText("")
	mw.dirCacheTimeEntry.SetText("")
	mw.bufferSizeEntry.SetText("")
	mw.readChunkSizeEntry.SetText("")

	drives := util.FreeDriveLetters()
	mw.driveSelect.Options = drives
	mw.driveSelect.Refresh()
	if len(drives) > 0 {
		mw.driveSelect.SetSelected(drives[0])
	}

	// Default to the first featured provider, falling back to the full catalog.
	def := Providers[0]
	if fp := FeaturedProviders(); len(fp) > 0 {
		def = *fp[0]
	}
	mw.applyProviderSelection(&def, nil)
	setText(mw.editorSubtitle, def.Name+"  →  "+mw.driveSelect.Selected+":")

	mw.saveBtn.OnTapped = func() { mw.saveNew() }
	mw.cancelBtn.OnTapped = func() { mw.cancelEdit() }

	mw.rightStack.Objects = []fyne.CanvasObject{mw.editorContent}
	mw.rightStack.Refresh()
}

// ShowPhone shows the window with the phone entry selected.
func (mw *MainWindow) ShowPhone() {
	mw.Show()
	mw.list.UnselectAll()
	mw.selectedIdx = -1
	mw.phonePanel.Refresh()
	mw.phoneRow.Update()
	mw.syncRow.SetSelected(false)
	mw.phoneRow.SetSelected(true)
	mw.rightStack.Objects = []fyne.CanvasObject{mw.phonePanel.Content()}
	mw.rightStack.Refresh()
}

// ShowAutoSync shows the window with the auto-sync entry selected.
func (mw *MainWindow) ShowAutoSync() {
	mw.Show()
	mw.list.UnselectAll()
	mw.selectedIdx = -1
	mw.syncPanel.Refresh()
	mw.syncRow.Update()
	mw.phoneRow.SetSelected(false)
	mw.syncRow.SetSelected(true)
	mw.rightStack.Objects = []fyne.CanvasObject{mw.syncPanel.Content()}
	mw.rightStack.Refresh()
}

func (mw *MainWindow) cancelEdit() {
	mw.phoneRow.SetSelected(false)
	mw.syncRow.SetSelected(false)
	mw.selectedIdx = -1
	mw.list.UnselectAll()
	mw.rightStack.Objects = []fyne.CanvasObject{mw.hintContent}
	mw.rightStack.Refresh()
}

func (mw *MainWindow) refreshMountBtn(c *store.Connection) {
	st := mw.ctrl.MountStatus(c.ID)
	mw.statusPill.Set(statusText(st.State), statusColor(st.State))
	mw.statusPill.root.Show()
	switch st.State {
	case mount.StateMounted:
		mw.mountBtn.SetIcon(theme.MediaStopIcon())
		mw.mountBtn.SetText("Unmount " + c.DriveLetter + ":")
		mw.mountBtn.Importance = widget.MediumImportance
		mw.mountBtn.OnTapped = func() { mw.ctrl.RequestUnmount(c.ID) }
		mw.mountBtn.Enable()
	case mount.StateMounting, mount.StateUnmounting:
		mw.mountBtn.SetIcon(theme.ViewRefreshIcon())
		mw.mountBtn.SetText("Working…")
		mw.mountBtn.Importance = widget.MediumImportance
		mw.mountBtn.OnTapped = nil
		mw.mountBtn.Disable()
	case mount.StateError:
		mw.mountBtn.SetIcon(theme.MediaPlayIcon())
		mw.mountBtn.SetText("Retry Mount " + c.DriveLetter + ":")
		mw.mountBtn.Importance = widget.WarningImportance
		mw.mountBtn.OnTapped = func() { mw.ctrl.RequestMount(c.ID) }
		mw.mountBtn.Enable()
	default:
		mw.mountBtn.SetIcon(theme.MediaPlayIcon())
		mw.mountBtn.SetText("Mount " + c.DriveLetter + ":")
		mw.mountBtn.Importance = widget.MediumImportance
		mw.mountBtn.OnTapped = func() { mw.ctrl.RequestMount(c.ID) }
		mw.mountBtn.Enable()
	}
	mw.mountBtn.Refresh()
	mw.mountBtnWrap.Show()
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
		CacheMode:     store.CacheMode(mw.cacheSeg.Selected),
		AutoMount:     mw.autoMountToggle.Checked,
		AutoReconnect: mw.autoReconToggle.Checked,
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
	mw.applyFilter()
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
	mw.applyFilter()
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
			mw.applyFilter()
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
			mw.applyFilter()
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

func statusText(s mount.State) string {
	switch s {
	case mount.StateMounted:
		return "Mounted"
	case mount.StateMounting:
		return "Mounting"
	case mount.StateUnmounting:
		return "Unmounting"
	case mount.StateError:
		return "Error"
	default:
		return "Not mounted"
	}
}

func statusColor(s mount.State) color.Color {
	switch s {
	case mount.StateMounted:
		return theme.Color(theme.ColorNameSuccess)
	case mount.StateMounting, mount.StateUnmounting:
		return theme.Color(theme.ColorNameWarning)
	case mount.StateError:
		return theme.Color(theme.ColorNameError)
	default:
		return theme.Color(theme.ColorNameDisabled)
	}
}

// providerUIType resolves the UI provider type for a connection, preferring
// the stored _uiType hint and falling back to the raw rclone provider.
func providerUIType(c *store.Connection) string {
	if t := c.Config[store.ConfigKeyUIType]; t != "" {
		return t
	}
	return c.Provider
}

// providerForConn resolves a connection's ProviderDef, or nil if unknown.
func providerForConn(c *store.Connection) *ProviderDef {
	return FindProvider(providerUIType(c))
}

// providerDisplayName resolves the friendly provider name shown in the
// connection's UI type (falling back to the raw rclone provider string).
func providerDisplayName(c *store.Connection) string {
	if def := providerForConn(c); def != nil {
		return def.Name
	}
	return c.Provider
}
