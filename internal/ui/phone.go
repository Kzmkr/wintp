package ui

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
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
}

// phoneRow is the "Phone" entry in the sidebar, styled like a connection row.
type phoneRow struct {
	ctrl PhoneController

	root fyne.CanvasObject
	bg   *canvas.Rectangle
	sub  *widget.Label
	dot  *canvas.Circle
}

func newPhoneRow(ctrl PhoneController, onTap func()) *phoneRow {
	r := &phoneRow{ctrl: ctrl}

	badgeBg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	badgeBg.CornerRadius = 6
	badgeBg.StrokeWidth = 1
	badgeBg.StrokeColor = theme.Color(theme.ColorNameInputBorder)
	badgeLbl := widget.NewLabelWithStyle("PH", fyne.TextAlignCenter, fyne.TextStyle{Bold: true, Monospace: true})
	badge := container.NewGridWrap(fyne.NewSize(36, 36),
		container.NewStack(badgeBg, container.NewCenter(badgeLbl)))

	name := widget.NewLabelWithStyle("Phone", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	r.sub = widget.NewLabel("")
	r.sub.Importance = widget.LowImportance

	r.dot = canvas.NewCircle(theme.Color(theme.ColorNameDisabled))
	dotWrap := container.NewGridWrap(fyne.NewSize(8, 8), r.dot)

	r.bg = canvas.NewRectangle(color.Transparent)
	r.bg.CornerRadius = 4

	content := container.NewBorder(nil, nil, badge, container.NewCenter(dotWrap),
		container.NewVBox(name, r.sub))

	tap := widget.NewButton("", onTap)
	tap.Importance = widget.LowImportance

	r.root = container.NewStack(r.bg, container.NewPadded(content), tap)
	r.Update()
	return r
}

func (r *phoneRow) Object() fyne.CanvasObject { return r.root }

// SetSelected highlights the row while the phone panel is showing.
func (r *phoneRow) SetSelected(sel bool) {
	if sel {
		r.bg.FillColor = theme.Color(theme.ColorNameSelection)
	} else {
		r.bg.FillColor = color.Transparent
	}
	r.bg.Refresh()
}

// Update re-reads the link state into the row.
func (r *phoneRow) Update() {
	on := r.ctrl.PhoneLinkEnabled()
	n := len(r.ctrl.PhoneDevices())
	switch {
	case !on:
		r.sub.SetText("Clipboard & files  ·  off")
		r.dot.FillColor = theme.Color(theme.ColorNameDisabled)
	case n == 0:
		r.sub.SetText("Clipboard & files  ·  not paired")
		r.dot.FillColor = theme.Color(theme.ColorNameWarning)
	default:
		word := "phones"
		if n == 1 {
			word = "phone"
		}
		r.sub.SetText(fmt.Sprintf("Clipboard & files  ·  %d %s", n, word))
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
	driveBusy   bool
	driveSeeded bool
}

func newPhonePanel(ctrl PhoneController, win fyne.Window) *phonePanel {
	p := &phonePanel{ctrl: ctrl}

	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord
	p.status.Importance = widget.LowImportance
	p.qrBox = container.NewCenter()
	p.devices = container.NewVBox()

	p.toggle = newToggleSwitch(ctrl.PhoneLinkEnabled(), p.onToggle)
	p.pairBtn = widget.NewButtonWithIcon("Show pairing QR code", theme.VisibilityIcon(), p.showQR)

	title := widget.NewLabelWithStyle("Phone", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	subtitle := widget.NewLabel("Share the clipboard and send files with your Android phone.")
	subtitle.Importance = widget.LowImportance
	header := container.NewVBox(
		container.NewPadded(container.NewVBox(title, subtitle)),
		widget.NewSeparator(),
	)

	linkCard := widget.NewCard("", "", settingsRow(
		"Phone link", "Listen on your local network so a paired phone can connect.", p.toggle))

	pairCard := widget.NewCard("", "", container.NewVBox(
		sectionHeading("Pair a phone", "Open the wintp link app and scan the code."),
		container.NewHBox(p.pairBtn),
		p.qrBox,
		p.status,
	))

	p.driveStatus = widget.NewLabel("")
	p.driveStatus.Wrapping = fyne.TextWrapWord
	p.driveLetter = widget.NewSelect(nil, nil)
	p.driveAuto = newToggleSwitch(false, nil)
	p.driveBtn = widget.NewButtonWithIcon("Mount", theme.MediaPlayIcon(), p.onDriveButton)
	p.driveBtn.Importance = widget.SuccessImportance
	p.driveForget = widget.NewButtonWithIcon("Remove drive", theme.DeleteIcon(), p.onDriveForget)
	driveCard := widget.NewCard("", "", container.NewVBox(
		sectionHeading("Phone drive", "Browse the phone's files in File Explorer."),
		p.driveStatus,
		widget.NewForm(widget.NewFormItem("Drive letter", p.driveLetter)),
		settingsRow("Mount on startup", "Mount when wintp starts and the phone is sharing.", p.driveAuto),
		container.NewHBox(p.driveBtn, p.driveForget),
	))

	devicesCard := widget.NewCard("", "", container.NewVBox(
		sectionHeading("Paired phones", ""),
		p.devices,
		widget.NewSeparator(),
		sectionHeading("Received files", ctrl.PhoneInbox()),
	))

	p.content = container.NewBorder(header, nil, nil, nil,
		container.NewScroll(container.NewVBox(linkCard, pairCard, driveCard, openRulesCard(ctrl, win), pcSharesCard(ctrl, win), devicesCard)))
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
		p.driveBtn.Importance = widget.DangerImportance
	} else {
		p.driveBtn.SetText("Mount")
		p.driveBtn.SetIcon(theme.MediaPlayIcon())
		p.driveBtn.Importance = widget.SuccessImportance
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
		p.qrBox.Refresh()
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
		p.devices.Add(container.NewBorder(nil, nil, nil, remove,
			widget.NewLabel(fmt.Sprintf("%s  ·  paired %s", d.Name, d.AddedAt.Format("2006-01-02")))))
	}
	if len(p.devices.Objects) == 0 {
		none := widget.NewLabel("No paired phones.")
		none.Importance = widget.LowImportance
		p.devices.Add(none)
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
