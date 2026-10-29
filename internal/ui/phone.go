package ui

import (
	"bytes"
	"fmt"
	"image/png"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	qrcode "github.com/skip2/go-qrcode"

	"wintp/internal/link"
	"wintp/internal/mount"
	"wintp/internal/util"
)

// PhoneController is implemented by the coordinator for the phone-link panel.
type PhoneController interface {
	PhoneLinkEnabled() bool
	SetPhoneLinkEnabled(bool) error
	PhonePairingURI() (string, error)
	PhoneDevices() []link.Device
	RemovePhoneDevice(id string) error
	PhoneInbox() string
	PhoneDrive() PhoneDriveInfo
	MountPhoneDrive(letter string, autoMount bool) error
	UnmountPhoneDrive()
	ForgetPhoneDrive() error
	SetPhoneThumbs(on bool) error
	OpenRules() []link.OpenRule
	SetOpenRules([]link.OpenRule) error
	PCShares() []link.Share
	SetPCShares([]link.Share) error
}

// PhoneDriveInfo describes the phone-as-a-drive state.
type PhoneDriveInfo struct {
	Online    bool   // phone is currently sharing its storage
	Addr      string // phone address when online
	Exists    bool   // a drive connection has been created
	Letter    string
	AutoMount bool
	State     mount.State

	Thumbs        bool // pre-load thumbnails while mounted
	ThumbsRunning bool
	ThumbsScanned bool
	ThumbsTotal   int
	ThumbsReady   int
	ThumbsFailed  int
}

// phoneRow is the "Phone" entry in the sidebar, styled like a connection row.
type phoneRow struct {
	ctrl PhoneController

	surface *tapSurface
	sub     *canvas.Text
	dot     *canvas.Circle
}

func newPhoneRow(ctrl PhoneController, onTap func()) *phoneRow {
	r := &phoneRow{ctrl: ctrl}

	b := newBadge("PH", 32)
	name := newText("Phone", 13, colText, true)
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

func (r *phoneRow) Object() fyne.CanvasObject { return r.surface }

// SetSelected highlights the row while the phone panel is showing.
func (r *phoneRow) SetSelected(sel bool) { r.surface.SetSelected(sel) }

// Update re-reads the link state into the row.
func (r *phoneRow) Update() {
	on := r.ctrl.PhoneLinkEnabled()
	n := len(r.ctrl.PhoneDevices())
	switch {
	case !on:
		setText(r.sub, "Clipboard & files  ·  off")
		r.dot.FillColor = theme.Color(theme.ColorNameDisabled)
	case n == 0:
		setText(r.sub, "Clipboard & files  ·  not paired")
		r.dot.FillColor = theme.Color(theme.ColorNameWarning)
	default:
		word := "phones"
		if n == 1 {
			word = "phone"
		}
		setText(r.sub, fmt.Sprintf("Clipboard & files  ·  %d %s", n, word))
		r.dot.FillColor = theme.Color(theme.ColorNameSuccess)
	}
	r.dot.Refresh()
}

// phonePanel is the right-hand editor pane for the phone entry.
type phonePanel struct {
	ctrl     PhoneController
	onChange func() // called after link state or device list changes

	content fyne.CanvasObject
	toggle  *toggleSwitch
	pairBtn *widget.Button
	qrBox   *fyne.Container
	status  *widget.Label
	devices *fyne.Container

	// phone drive card
	driveStatus *widget.Label
	driveLetter *widget.Select
	driveAuto   *toggleSwitch
	driveBtn    *widget.Button
	driveForget *widget.Button
	driveThumbs *toggleSwitch
	thumbStatus *widget.Label
	driveBusy   bool
	driveSeeded bool
}

func newPhonePanel(ctrl PhoneController, win fyne.Window) *phonePanel {
	p := &phonePanel{ctrl: ctrl}

	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord
	p.status.Importance = widget.LowImportance
	p.status.SizeName = theme.SizeNameCaptionText
	p.qrBox = container.NewCenter()
	p.qrBox.Hide()
	p.devices = container.NewVBox()

	p.toggle = newToggleSwitch(ctrl.PhoneLinkEnabled(), p.onToggle)
	p.pairBtn = widget.NewButtonWithIcon("Show pairing QR code", theme.VisibilityIcon(), p.showQR)

	p.pairBtn.Importance = widget.HighImportance
	header := pageHeader(
		newText("Phone", 20, colText, true),
		newText("Share the clipboard and send files with your Android phone.", 12.5, colMuted, false),
		nil)

	linkCard := card("", "", settingsRow(
		"Phone link", "Listen on your local network so a paired phone can connect.", p.toggle))

	pairCard := card("Pair a phone", "Open the wintp link app and scan the code.",
		container.NewHBox(p.pairBtn),
		p.qrBox,
		unpadded(p.status),
	)

	p.driveStatus = widget.NewLabel("")
	p.driveStatus.Wrapping = fyne.TextWrapWord
	p.driveLetter = widget.NewSelect(nil, nil)
	p.driveAuto = newToggleSwitch(false, nil)
	p.driveBtn = widget.NewButtonWithIcon("Mount", theme.MediaPlayIcon(), p.onDriveButton)
	p.driveBtn.Importance = widget.HighImportance
	p.driveForget = widget.NewButtonWithIcon("Remove drive", theme.DeleteIcon(), p.onDriveForget)
	p.driveStatus.SizeName = theme.SizeNameCaptionText
	p.driveThumbs = newToggleSwitch(false, p.onThumbsToggle)
	p.thumbStatus = widget.NewLabel("")
	p.thumbStatus.SizeName = theme.SizeNameCaptionText
	p.thumbStatus.Importance = widget.LowImportance
	p.driveStatus.Importance = widget.LowImportance
	driveCard := card("Phone drive", "Browse the phone's files in File Explorer.",
		unpadded(p.driveStatus),
		container.NewGridWithColumns(2, field("Drive letter", p.driveLetter), layout.NewSpacer()),
		divider(),
		settingsRow("Mount on startup", "Mount when wintp starts and the phone is sharing.", p.driveAuto),
		divider(),
		vbox(8,
			settingsRow("Pre-load thumbnails",
				"Builds File Explorer thumbnails for the phone's photos and videos in the background, newest first, so folders open instantly. Each file is downloaded once.",
				p.driveThumbs),
			unpadded(p.thumbStatus)),
		divider(),
		container.NewHBox(p.driveBtn, p.driveForget),
	)

	devicesCard := card("Paired phones", "",
		p.devices,
		divider(),
		sectionHeading("Received files", ctrl.PhoneInbox()),
	)

	p.content = container.NewBorder(header, nil, nil, nil,
		pageBody(linkCard, pairCard, driveCard, openRulesCard(ctrl, win), pcSharesCard(ctrl, win), devicesCard))
	p.Refresh()
	go p.watchDrive()
	return p
}

// watchDrive keeps the drive status current: the phone can start or stop
// sharing at any time without wintp being told.
func (p *phonePanel) watchDrive() {
	for range time.Tick(3 * time.Second) {
		fyne.Do(p.refreshDrive)
	}
}

func (p *phonePanel) onDriveButton() {
	info := p.ctrl.PhoneDrive()
	if info.State == mount.StateMounted {
		p.ctrl.UnmountPhoneDrive()
		return
	}
	letter := p.driveLetter.Selected
	if letter == "" {
		p.driveStatus.SetText("Pick a drive letter first.")
		return
	}
	auto := p.driveAuto.Checked
	p.driveBusy = true
	p.refreshDrive()
	go func() {
		err := p.ctrl.MountPhoneDrive(letter, auto)
		fyne.Do(func() {
			p.driveBusy = false
			if err != nil {
				p.driveStatus.SetText("Cannot mount: " + err.Error())
				return
			}
			p.refreshDrive()
		})
	}()
}

func (p *phonePanel) onDriveForget() {
	go func() {
		err := p.ctrl.ForgetPhoneDrive()
		fyne.Do(func() {
			if err != nil {
				p.driveStatus.SetText("Error: " + err.Error())
			}
			p.driveSeeded = false
			p.refreshDrive()
		})
	}()
}

// refreshDrive updates the phone drive card. Call on the UI thread.
func (p *phonePanel) refreshDrive() {
	info := p.ctrl.PhoneDrive()

	// Seed the letter list once, and again after the drive is removed.
	if !p.driveSeeded {
		letters := util.FreeDriveLetters()
		if info.Letter != "" {
			found := false
			for _, l := range letters {
				found = found || l == info.Letter
			}
			if !found {
				letters = append([]string{info.Letter}, letters...)
			}
		}
		p.driveLetter.Options = letters
		p.driveLetter.Refresh()
		switch {
		case info.Letter != "":
			p.driveLetter.SetSelected(info.Letter)
		case len(letters) > 0:
			p.driveLetter.SetSelected(letters[len(letters)-1])
		}
		p.driveAuto.SetChecked(info.AutoMount)
		p.driveSeeded = true
	}

	switch {
	case !p.ctrl.PhoneLinkEnabled():
		p.driveStatus.SetText("Turn on Phone link first.")
	case info.State == mount.StateMounted:
		p.driveStatus.SetText("Mounted as " + info.Letter + ":")
	case info.State == mount.StateMounting:
		p.driveStatus.SetText("Mounting…")
	case info.Online:
		p.driveStatus.SetText("Phone is sharing its storage (" + info.Addr + ").")
	default:
		p.driveStatus.SetText(`Phone is not sharing. Turn on "Share phone storage" in the wintp link app.`)
	}

	if info.State == mount.StateMounted {
		p.driveBtn.SetText("Unmount " + info.Letter + ":")
		p.driveBtn.SetIcon(theme.MediaStopIcon())
		p.driveBtn.Importance = widget.MediumImportance
	} else {
		p.driveBtn.SetText("Mount")
		p.driveBtn.SetIcon(theme.MediaPlayIcon())
		p.driveBtn.Importance = widget.HighImportance
	}
	p.driveBtn.Refresh()
	if p.driveBusy || info.State == mount.StateMounting || info.State == mount.StateUnmounting ||
		!p.ctrl.PhoneLinkEnabled() {
		p.driveBtn.Disable()
	} else {
		p.driveBtn.Enable()
	}
	if info.Exists {
		p.driveForget.Show()
	} else {
		p.driveForget.Hide()
	}
	p.refreshThumbs(info)
}

func (p *phonePanel) onThumbsToggle(on bool) {
	if err := p.ctrl.SetPhoneThumbs(on); err != nil {
		p.driveThumbs.SetChecked(!on)
		p.thumbStatus.SetText(err.Error())
		return
	}
	p.refreshDrive()
}

// refreshThumbs shows thumbnail pre-loading progress. Call on the UI thread.
func (p *phonePanel) refreshThumbs(info PhoneDriveInfo) {
	p.driveThumbs.SetChecked(info.Exists && info.Thumbs)
	if info.Exists {
		p.driveThumbs.Show()
	} else {
		p.driveThumbs.Hide()
	}
	var text string
	switch {
	case !info.Exists:
		text = "Available once the phone drive is set up."
	case !info.Thumbs:
		text = "Off. Explorer builds thumbnails only when you open a folder."
	case info.State != mount.StateMounted:
		text = "Starts when the phone drive is mounted."
	case !info.ThumbsScanned:
		text = "Looking for photos and videos…"
	case info.ThumbsTotal == 0:
		text = "No photos or videos found."
	default:
		text = fmt.Sprintf("%s of %s thumbnails ready", thousands(info.ThumbsReady), thousands(info.ThumbsTotal))
		if info.ThumbsFailed > 0 {
			text += fmt.Sprintf(" · %s without a preview", thousands(info.ThumbsFailed))
		}
		if info.ThumbsRunning && info.ThumbsReady+info.ThumbsFailed < info.ThumbsTotal {
			text += " · building…"
		}
	}
	p.thumbStatus.SetText(text)
}

// thousands formats n with comma group separators ("12,345").
func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (p *phonePanel) Content() fyne.CanvasObject { return p.content }

func (p *phonePanel) changed() {
	if p.onChange != nil {
		p.onChange()
	}
}

func (p *phonePanel) onToggle(on bool) {
	// Starting the server reads the clipboard via the UI thread, so it must
	// not run on it (fyne.DoAndWait would deadlock).
	go func() {
		err := p.ctrl.SetPhoneLinkEnabled(on)
		fyne.Do(func() {
			if err != nil {
				p.toggle.SetChecked(!on)
				p.status.SetText("Error: " + err.Error())
				return
			}
			p.Refresh()
			p.changed()
		})
	}()
}

func (p *phonePanel) showQR() {
	uri, err := p.ctrl.PhonePairingURI()
	if err != nil {
		p.status.SetText("Cannot pair: " + err.Error())
		return
	}
	data, err := qrPNG(uri)
	if err != nil {
		p.status.SetText("QR error: " + err.Error())
		return
	}
	img := canvas.NewImageFromResource(fyne.NewStaticResource("pair.png", data))
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(260, 260))
	p.qrBox.Objects = []fyne.CanvasObject{img}
	p.qrBox.Show()
	p.qrBox.Refresh()
	p.status.SetText("The code works once, for 5 minutes.")
}

// Refresh re-reads link state and the device list. Call on the UI thread.
func (p *phonePanel) Refresh() {
	defer p.refreshDrive()
	on := p.ctrl.PhoneLinkEnabled()
	p.toggle.SetChecked(on)
	if on {
		p.pairBtn.Enable()
		if len(p.qrBox.Objects) == 0 {
			p.status.SetText("")
		}
	} else {
		p.pairBtn.Disable()
		p.qrBox.Objects = nil
		p.qrBox.Hide()
		p.status.SetText("Turn on Phone link to pair a phone.")
	}

	p.devices.Objects = nil
	for _, d := range p.ctrl.PhoneDevices() {
		d := d
		remove := widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
			_ = p.ctrl.RemovePhoneDevice(d.ID)
			p.Refresh()
			p.changed()
		})
		remove.Importance = widget.LowImportance
		info := vbox(3, newText(d.Name, 13, colText, true),
			newText("Paired "+d.AddedAt.Format("Jan 2, 2006"), 11.5, colMuted, false))
		p.devices.Add(container.NewBorder(nil, nil,
			inset(0, 0, 0, 10, container.NewCenter(newBadge("PH", 30).root)),
			container.NewCenter(remove),
			container.NewVBox(layout.NewSpacer(), info, layout.NewSpacer())))
	}
	if len(p.devices.Objects) == 0 {
		p.devices.Add(newText("No paired phones yet.", 12.5, colMuted, false))
	}
	p.devices.Refresh()
}

func qrPNG(content string) ([]byte, error) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, q.Image(512)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
