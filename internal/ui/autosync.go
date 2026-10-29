package ui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"wintp/internal/autosync"
	"wintp/internal/store"
)

// SyncController is implemented by the coordinator for the auto-sync page.
type SyncController interface {
	Connections() []*store.Connection
	SyncJobs() []*store.SyncJob
	SyncJobStatus(id string) autosync.Status
	SaveSyncJob(job *store.SyncJob) error
	DeleteSyncJob(id string) error
	RunSyncJob(id string)
}

var directionLabels = []struct {
	dir     store.SyncDirection
	label   string
	arrow   string
	caption string
}{
	{store.SyncUpload, "Upload", "→", "Copies new and changed files from this PC to the remote."},
	{store.SyncDownload, "Download", "←", "Copies new and changed files from the remote to this PC."},
	{store.SyncTwoWay, "Two-way", "⇄", "Changes on either side are synced to the other. If a file changed on both sides, the newer one wins and the other is kept as a numbered copy."},
}

func directionInfo(d store.SyncDirection) (label, arrow, caption string) {
	for _, x := range directionLabels {
		if x.dir == d {
			return x.label, x.arrow, x.caption
		}
	}
	return directionInfo(store.SyncUpload)
}

var syncIntervals = []struct {
	minutes int
	label   string
}{
	{5, "Every 5 minutes"},
	{15, "Every 15 minutes"},
	{30, "Every 30 minutes"},
	{60, "Every hour"},
	{180, "Every 3 hours"},
	{360, "Every 6 hours"},
	{1440, "Once a day"},
}

func intervalLabel(minutes int) string {
	for _, x := range syncIntervals {
		if x.minutes == minutes {
			return x.label
		}
	}
	return fmt.Sprintf("Every %d minutes", minutes)
}

// relTime formats t relative to now ("3 min ago", "in 12 min").
func relTime(t time.Time) string {
	d := time.Until(t)
	future := d > 0
	if !future {
		d = -d
	}
	var s string
	switch {
	case d < time.Minute:
		if future {
			return "in under a minute"
		}
		return "just now"
	case d < time.Hour:
		s = fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		s = fmt.Sprintf("%d h", int(d.Hours()))
	default:
		s = fmt.Sprintf("%d days", int(d.Hours()/24))
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}

// ---------------------------------------------------------------------------
// Sidebar row
// ---------------------------------------------------------------------------

type syncRow struct {
	ctrl    SyncController
	surface *tapSurface
	sub     *canvas.Text
	dot     *canvas.Circle
}

func newSyncRow(ctrl SyncController, onTap func()) *syncRow {
	r := &syncRow{ctrl: ctrl}
	b := newBadge("⇄", 32)
	b.lbl.TextSize = 15
	name := newText("Auto sync", 13, colText, true)
	r.sub = newText("", 11.5, colMuted, false)
	r.dot = canvas.NewCircle(colFaint)
	dot := container.NewCenter(container.NewGridWrap(fyne.NewSize(8, 8), r.dot))
	content := inset(7, 7, 8, 10, container.NewBorder(nil, nil,
		inset(0, 0, 0, 10, container.NewCenter(b.root)), inset(0, 0, 8, 0, dot),
		container.NewVBox(layout.NewSpacer(), vbox(3, name, r.sub), layout.NewSpacer())))
	r.surface = newTapSurface(navRowStyle, content, onTap)
	r.Update()
	return r
}

func (r *syncRow) Object() fyne.CanvasObject { return r.surface }
func (r *syncRow) SetSelected(v bool)        { r.surface.SetSelected(v) }

// Update summarises all jobs in the row's subtitle and dot.
func (r *syncRow) Update() {
	jobs := r.ctrl.SyncJobs()
	enabled, running, failed := 0, 0, 0
	for _, j := range jobs {
		if j.Enabled {
			enabled++
		}
		switch r.ctrl.SyncJobStatus(j.ID).State {
		case autosync.StateRunning:
			running++
		case autosync.StateError:
			failed++
		}
	}
	var text string
	var dot color.Color = colFaint
	switch {
	case len(jobs) == 0:
		text = "Folders  ·  none set up"
	case running > 0:
		text = "Folders  ·  syncing…"
		dot = colAccent
	case failed > 0:
		text = fmt.Sprintf("Folders  ·  %d failed", failed)
		dot = colError
	case enabled == 0:
		text = "Folders  ·  paused"
	default:
		word := "jobs"
		if enabled == 1 {
			word = "job"
		}
		text = fmt.Sprintf("Folders  ·  %d %s on", enabled, word)
		dot = colSuccess
	}
	setText(r.sub, text)
	r.dot.FillColor = dot
	r.dot.Refresh()
}

// ---------------------------------------------------------------------------
// Auto-sync page
// ---------------------------------------------------------------------------

type syncPanel struct {
	ctrl     SyncController
	win      func() fyne.Window
	onChange func()

	content fyne.CanvasObject
	list    *fyne.Container
}

func newSyncPanel(ctrl SyncController, win func() fyne.Window) *syncPanel {
	p := &syncPanel{ctrl: ctrl, win: win, list: vbox(16)}

	newBtn := widget.NewButtonWithIcon("New sync job", theme.ContentAddIcon(), func() { p.edit(nil) })
	newBtn.Importance = widget.HighImportance
	header := pageHeader(
		newText("Auto sync", 20, colText, true),
		newText("Keep folders on this PC in sync with your connections, even when no drive is mounted.", 12.5, colMuted, false),
		container.NewCenter(newBtn))

	p.content = container.NewBorder(header, nil, nil, nil, pageBody(p.list))
	p.Refresh()
	go func() {
		// Keep "3 min ago" / "in 12 min" current.
		for range time.Tick(30 * time.Second) {
			fyne.Do(p.Refresh)
		}
	}()
	return p
}

func (p *syncPanel) Content() fyne.CanvasObject { return p.content }

func (p *syncPanel) changed() {
	p.Refresh()
	if p.onChange != nil {
		p.onChange()
	}
}

// Refresh rebuilds the job cards. Call on the UI thread.
func (p *syncPanel) Refresh() {
	jobs := p.ctrl.SyncJobs()
	conns := map[string]*store.Connection{}
	for _, c := range p.ctrl.Connections() {
		conns[c.ID] = c
	}

	p.list.Objects = nil
	if len(jobs) == 0 {
		p.list.Add(p.emptyCard(len(conns) > 0))
	}
	for _, j := range jobs {
		p.list.Add(p.jobCard(j, conns[j.ConnectionID]))
	}
	p.list.Refresh()
}

func (p *syncPanel) emptyCard(haveConns bool) fyne.CanvasObject {
	msg := "Create a sync job to copy a folder to a server or cloud on a schedule, or keep both sides in step."
	if !haveConns {
		msg = "Add a connection first, then create a sync job that uses it."
	}
	_, body := wrapText(msg)
	return card("No sync jobs yet", "", body)
}

func (p *syncPanel) jobCard(j *store.SyncJob, conn *store.Connection) fyne.CanvasObject {
	st := p.ctrl.SyncJobStatus(j.ID)

	code, connName := "?", "missing connection"
	if conn != nil {
		code, connName = badgeFor(providerForConn(conn)), conn.Name
	}
	_, arrow, _ := directionInfo(j.Direction)
	dirLabel, _, _ := directionInfo(j.Direction)

	name := newText(j.Name, 14, colText, true)
	path := widget.NewLabel(fmt.Sprintf("%s  %s  %s:%s", j.LocalPath, arrow, connName, j.RemotePath))
	path.Importance = widget.LowImportance
	path.SizeName = theme.SizeNameCaptionText
	path.Truncation = fyne.TextTruncateEllipsis

	pill := newStatusPill()
	var statusLine string
	switch {
	case st.State == autosync.StateRunning:
		pill.Set("Syncing", colAccent)
	case st.State == autosync.StateError:
		pill.Set("Failed", colError)
		statusLine = st.Message
	case !j.Enabled:
		pill.Set("Paused", colFaint)
	case st.State == autosync.StateOK:
		pill.Set("Up to date", colSuccess)
	default:
		pill.Set("Waiting", colFaint)
	}

	var when []string
	when = append(when, dirLabel, intervalLabel(j.IntervalMinutes))
	if !st.LastRun.IsZero() {
		when = append(when, "last "+relTime(st.LastRun))
	}
	if j.Enabled && !st.NextRun.IsZero() && st.State != autosync.StateRunning {
		when = append(when, "next "+relTime(st.NextRun))
	}
	meta := newText(strings.Join(when, "  ·  "), 11.5, colMuted, false)

	toggle := newToggleSwitch(j.Enabled, func(on bool) {
		cp := *j
		cp.Enabled = on
		if err := p.ctrl.SaveSyncJob(&cp); err != nil {
			dialog.ShowError(err, p.win())
		}
		p.changed()
	})

	syncBtn := widget.NewButtonWithIcon("Sync now", theme.ViewRefreshIcon(), func() {
		p.ctrl.RunSyncJob(j.ID)
	})
	if st.State == autosync.StateRunning || conn == nil {
		syncBtn.Disable()
	}
	editBtn := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() { p.edit(j) })
	delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Delete sync job",
			fmt.Sprintf("Delete %q? Files already synced are left alone.", j.Name),
			func(ok bool) {
				if !ok {
					return
				}
				if err := p.ctrl.DeleteSyncJob(j.ID); err != nil {
					dialog.ShowError(err, p.win())
				}
				p.changed()
			}, p.win())
	})
	delBtn.Importance = widget.LowImportance

	top := container.NewBorder(nil, nil,
		inset(0, 0, 0, 12, container.NewCenter(newBadge(code, 36).root)),
		container.NewCenter(toggle),
		vbox(3, hbox(10, name, pill.root), unpadded(path)))

	body := []fyne.CanvasObject{top, divider(),
		container.NewBorder(nil, nil, nil, hbox(6, syncBtn, editBtn, delBtn),
			container.NewVBox(layout.NewSpacer(), meta, layout.NewSpacer()))}
	if statusLine != "" {
		errLbl := widget.NewLabel(statusLine)
		errLbl.Wrapping = fyne.TextWrapWord
		errLbl.Importance = widget.DangerImportance
		errLbl.SizeName = theme.SizeNameCaptionText
		body = append(body[:1], append([]fyne.CanvasObject{unpadded(errLbl)}, body[1:]...)...)
	}
	return card("", "", body...)
}

// edit opens the job editor; j is nil for a new job.
func (p *syncPanel) edit(j *store.SyncJob) {
	conns := p.ctrl.Connections()
	if len(conns) == 0 {
		dialog.ShowInformation("No connections", "Add a connection first, then create a sync job that uses it.", p.win())
		return
	}
	isNew := j == nil
	if isNew {
		j = &store.SyncJob{Direction: store.SyncUpload, IntervalMinutes: 15, Enabled: true,
			ConnectionID: conns[0].ID, RemotePath: conns[0].RemotePath}
	}
	job := *j

	nameEntry := widget.NewEntry()
	nameEntry.SetText(job.Name)
	nameEntry.PlaceHolder = "Documents backup"

	connNames := make([]string, len(conns))
	connByName := map[string]*store.Connection{}
	for i, c := range conns {
		connNames[i] = c.Name
		connByName[c.Name] = c
	}
	remoteEntry := widget.NewEntry()
	remoteEntry.SetText(job.RemotePath)
	remoteEntry.PlaceHolder = "/backup/documents"
	connSel := widget.NewSelect(connNames, nil)
	for _, c := range conns {
		if c.ID == job.ConnectionID {
			connSel.SetSelected(c.Name)
		}
	}
	connSel.OnChanged = func(name string) {
		// Suggest the connection's own root until the user types a path.
		if c := connByName[name]; c != nil && strings.TrimSpace(remoteEntry.Text) == "" {
			remoteEntry.SetText(c.RemotePath)
		}
	}

	localEntry := widget.NewEntry()
	localEntry.SetText(job.LocalPath)
	localEntry.PlaceHolder = `C:\Users\you\Documents`
	browse := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
			if err != nil || u == nil {
				return
			}
			localEntry.SetText(filepath.FromSlash(u.Path()))
			if strings.TrimSpace(nameEntry.Text) == "" {
				nameEntry.SetText(filepath.Base(localEntry.Text))
			}
		}, p.win())
		d.Show()
	})

	caption, captionObj := wrapText("")
	mirror := newToggleSwitch(job.Mirror, nil)
	mirrorRow := settingsRow("Mirror deletions",
		"Also delete files at the destination that were removed at the source.", mirror)

	var dirOpts []string
	for _, x := range directionLabels {
		dirOpts = append(dirOpts, x.label)
	}
	dirSeg := newSegmentedControl(dirOpts, nil)
	applyDir := func(label string) {
		for _, x := range directionLabels {
			if x.label == label {
				job.Direction = x.dir
				caption.SetText(x.caption)
			}
		}
		if job.Direction == store.SyncTwoWay {
			mirrorRow.Hide()
		} else {
			mirrorRow.Show()
		}
	}
	dirSeg.OnChanged = applyDir
	curLabel, _, _ := directionInfo(job.Direction)
	dirSeg.SetSelected(curLabel)
	applyDir(curLabel)

	var intervalOpts []string
	for _, x := range syncIntervals {
		intervalOpts = append(intervalOpts, x.label)
	}
	intervalSel := widget.NewSelect(intervalOpts, nil)
	intervalSel.SetSelected(intervalLabel(job.IntervalMinutes))

	enabled := newToggleSwitch(job.Enabled, nil)

	form := vbox(14,
		field("Name", nameEntry),
		field("Local folder", container.NewBorder(nil, nil, nil, browse, localEntry)),
		container.NewGridWithColumns(2,
			field("Connection", connSel),
			field("Remote folder", remoteEntry),
		),
		field("Direction", vbox(8, dirSeg.CanvasObject(), captionObj)),
		mirrorRow,
		container.NewGridWithColumns(2, field("Schedule", intervalSel), layout.NewSpacer()),
		divider(),
		settingsRow("Enabled", "Run on the schedule, and once shortly after wintp starts.", enabled),
	)

	title := "New sync job"
	if !isNew {
		title = "Edit sync job"
	}
	var d dialog.Dialog
	save := widget.NewButtonWithIcon("Save", theme.ConfirmIcon(), func() {
		c := connByName[connSel.Selected]
		job.Name = strings.TrimSpace(nameEntry.Text)
		job.LocalPath = strings.TrimSpace(localEntry.Text)
		job.RemotePath = strings.TrimSpace(remoteEntry.Text)
		job.Mirror = mirror.Checked && job.Direction != store.SyncTwoWay
		job.Enabled = enabled.Checked
		for _, x := range syncIntervals {
			if x.label == intervalSel.Selected {
				job.IntervalMinutes = x.minutes
			}
		}
		if job.Name == "" && job.LocalPath != "" {
			job.Name = filepath.Base(job.LocalPath)
		}
		switch {
		case c == nil:
			dialog.ShowError(fmt.Errorf("choose a connection"), p.win())
			return
		case job.LocalPath == "" || !filepath.IsAbs(job.LocalPath):
			dialog.ShowError(fmt.Errorf("local folder must be a full path, e.g. C:\\Users\\you\\Documents"), p.win())
			return
		case job.Name == "":
			dialog.ShowError(fmt.Errorf("name is required"), p.win())
			return
		}
		job.ConnectionID = c.ID
		if err := p.ctrl.SaveSyncJob(&job); err != nil {
			dialog.ShowError(err, p.win())
			return
		}
		d.Hide()
		p.changed()
	})
	save.Importance = widget.HighImportance
	cancel := widget.NewButton("Cancel", func() { d.Hide() })

	content := container.NewBorder(nil,
		inset(16, 0, 0, 0, container.NewBorder(nil, nil, nil, hbox(8, cancel, save))),
		nil, nil, container.NewVScroll(inset(4, 4, 0, 8, form)))
	d = dialog.NewCustomWithoutButtons(title, content, p.win())
	d.Resize(fyne.NewSize(600, 640))
	d.Show()
}
