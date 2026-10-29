package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ---------------------------------------------------------------------------
// fixedHeight / hGap: small layout helpers for compact, evenly-spaced button
// rows (used by the editor's header action bar).
// ---------------------------------------------------------------------------

// fixedHeightLayout forces its content to a fixed height while leaving width
// flexible (sized to content, or stretched by a parent HBox/Border).
type fixedHeightLayout struct{ height float32 }

func (l fixedHeightLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w float32
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		if m := o.MinSize().Width; m > w {
			w = m
		}
	}
	return fyne.NewSize(w, l.height)
}

func (l fixedHeightLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		o.Resize(fyne.NewSize(size.Width, l.height))
		o.Move(fyne.NewPos(0, (size.Height-l.height)/2))
	}
}

// shortButton wraps btn so it renders at a fixed, shorter height than Fyne's
// default button size, keeping its natural (flexible) width.
func shortButton(height float32, btn *widget.Button) fyne.CanvasObject {
	return container.New(fixedHeightLayout{height: height}, btn)
}

// hGap returns an invisible fixed-width spacer for widening the gap between
// items in an HBox beyond the theme's default padding.
func hGap(width float32) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(width, 1), canvas.NewRectangle(color.Transparent))
}

// ---------------------------------------------------------------------------
// toggleSwitch: a compact on/off switch (track + knob), styled closer to a
// modern settings toggle than Fyne's built-in checkbox.
// ---------------------------------------------------------------------------

const (
	toggleWidth  float32 = 40
	toggleHeight float32 = 22
)

type toggleSwitch struct {
	widget.BaseWidget
	Checked   bool
	OnChanged func(bool)

	track *canvas.Rectangle
	knob  *canvas.Circle
}

func newToggleSwitch(checked bool, changed func(bool)) *toggleSwitch {
	t := &toggleSwitch{Checked: checked, OnChanged: changed}
	t.ExtendBaseWidget(t)
	return t
}

func (t *toggleSwitch) SetChecked(v bool) {
	if v == t.Checked {
		return
	}
	t.Checked = v
	t.Refresh()
}

func (t *toggleSwitch) Tapped(_ *fyne.PointEvent) {
	t.Checked = !t.Checked
	t.Refresh()
	if t.OnChanged != nil {
		t.OnChanged(t.Checked)
	}
}

func (t *toggleSwitch) MinSize() fyne.Size {
	return fyne.NewSize(toggleWidth, toggleHeight)
}

func (t *toggleSwitch) trackColor() color.Color {
	if t.Checked {
		return theme.Color(theme.ColorNamePrimary)
	}
	return theme.Color(theme.ColorNameDisabled)
}

func (t *toggleSwitch) CreateRenderer() fyne.WidgetRenderer {
	t.track = canvas.NewRectangle(t.trackColor())
	t.track.CornerRadius = toggleHeight / 2
	t.knob = canvas.NewCircle(color.White)
	return &toggleRenderer{t: t, objects: []fyne.CanvasObject{t.track, t.knob}}
}

type toggleRenderer struct {
	t       *toggleSwitch
	objects []fyne.CanvasObject
}

func (r *toggleRenderer) Layout(size fyne.Size) {
	r.t.track.Resize(fyne.NewSize(toggleWidth, toggleHeight))
	r.t.track.Move(fyne.NewPos(0, (size.Height-toggleHeight)/2))

	const knobInset = 2
	knobSize := toggleHeight - knobInset*2
	x := float32(knobInset)
	if r.t.Checked {
		x = toggleWidth - knobSize - knobInset
	}
	r.t.knob.Resize(fyne.NewSize(knobSize, knobSize))
	r.t.knob.Move(fyne.NewPos(x, (size.Height-knobSize)/2))
}

func (r *toggleRenderer) MinSize() fyne.Size { return fyne.NewSize(toggleWidth, toggleHeight) }

func (r *toggleRenderer) Refresh() {
	r.t.track.FillColor = r.t.trackColor()
	r.t.track.Refresh()
	r.Layout(r.t.Size())
	canvas.Refresh(r.t)
}

func (r *toggleRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *toggleRenderer) Destroy()                     {}

// settingsRow builds a settings-style row: bold title + muted subtitle on
// the left, the given control vertically centered on the right.
func settingsRow(title, subtitle string, control fyne.CanvasObject) fyne.CanvasObject {
	titleLbl := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	var text fyne.CanvasObject = titleLbl
	if subtitle != "" {
		subLbl := widget.NewLabel(subtitle)
		subLbl.Importance = widget.LowImportance
		text = container.NewVBox(titleLbl, subLbl)
	}
	return container.NewBorder(nil, nil, nil, container.NewCenter(control), text)
}

// ---------------------------------------------------------------------------
// segmentedControl: a row of mutually-exclusive buttons standing in for a
// pill-style segmented control (e.g. VFS cache mode: off/minimal/writes/full).
// ---------------------------------------------------------------------------

type segmentedControl struct {
	row       *fyne.Container
	buttons   map[string]*widget.Button
	order     []string
	Selected  string
	OnChanged func(string)
}

func newSegmentedControl(options []string, changed func(string)) *segmentedControl {
	sc := &segmentedControl{
		row:       container.NewHBox(),
		buttons:   make(map[string]*widget.Button, len(options)),
		order:     options,
		OnChanged: changed,
	}
	for _, opt := range options {
		opt := opt
		b := widget.NewButton(opt, func() { sc.setSelected(opt, true) })
		sc.buttons[opt] = b
		sc.row.Add(b)
	}
	return sc
}

func (sc *segmentedControl) setSelected(v string, notify bool) {
	sc.Selected = v
	for opt, b := range sc.buttons {
		if opt == v {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.LowImportance
		}
		b.Refresh()
	}
	if notify && sc.OnChanged != nil {
		sc.OnChanged(v)
	}
}

// SetSelected updates the control's state without firing OnChanged.
func (sc *segmentedControl) SetSelected(v string) { sc.setSelected(v, false) }

func (sc *segmentedControl) CanvasObject() fyne.CanvasObject { return sc.row }

// ---------------------------------------------------------------------------
// providerCard: a selectable card used in the provider picker grid - radio
// dot, bold title, muted subtitle, highlighted border when selected.
// ---------------------------------------------------------------------------

type providerCard struct {
	widget.BaseWidget
	title    string
	subtitle string
	selected bool
	onTap    func()

	bg    *canvas.Rectangle
	radio *canvas.Circle
}

func newProviderCard(title, subtitle string, onTap func()) *providerCard {
	c := &providerCard{title: title, subtitle: subtitle, onTap: onTap}
	c.ExtendBaseWidget(c)
	return c
}

func (c *providerCard) SetSelected(v bool) {
	if v == c.selected {
		return
	}
	c.selected = v
	c.Refresh()
}

func (c *providerCard) Tapped(_ *fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

func (c *providerCard) borderColor() color.Color {
	if c.selected {
		return theme.Color(theme.ColorNamePrimary)
	}
	return theme.Color(theme.ColorNameInputBorder)
}

func (c *providerCard) CreateRenderer() fyne.WidgetRenderer {
	c.bg = canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	c.bg.CornerRadius = 8
	c.bg.StrokeWidth = 1.5
	c.bg.StrokeColor = c.borderColor()

	c.radio = canvas.NewCircle(color.Transparent)
	c.radio.StrokeWidth = 1.5
	c.radio.StrokeColor = theme.Color(theme.ColorNameDisabled)

	title := widget.NewLabelWithStyle(c.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	var sub fyne.CanvasObject
	if c.subtitle != "" {
		subLbl := widget.NewLabel(c.subtitle)
		subLbl.Importance = widget.LowImportance
		sub = subLbl
	}

	radioWrap := container.NewGridWrap(fyne.NewSize(16, 16), c.radio)

	var text fyne.CanvasObject = title
	if sub != nil {
		text = container.NewVBox(title, sub)
	}

	content := container.NewBorder(nil, nil, container.NewVBox(container.NewPadded(radioWrap)), nil, text)
	padded := container.NewPadded(content)

	return &providerCardRenderer{
		c:       c,
		objects: []fyne.CanvasObject{c.bg, padded},
		content: padded,
	}
}

type providerCardRenderer struct {
	c       *providerCard
	objects []fyne.CanvasObject
	content fyne.CanvasObject
}

func (r *providerCardRenderer) Layout(size fyne.Size) {
	r.c.bg.Resize(size)
	r.content.Resize(size)
}

func (r *providerCardRenderer) MinSize() fyne.Size {
	return r.content.MinSize().Max(fyne.NewSize(160, 56))
}

func (r *providerCardRenderer) Refresh() {
	r.c.bg.StrokeColor = r.c.borderColor()
	r.c.bg.Refresh()
	if r.c.selected {
		r.c.radio.StrokeColor = theme.Color(theme.ColorNamePrimary)
		r.c.radio.FillColor = theme.Color(theme.ColorNamePrimary)
	} else {
		r.c.radio.StrokeColor = theme.Color(theme.ColorNameDisabled)
		r.c.radio.FillColor = color.Transparent
	}
	r.c.radio.Refresh()
	canvas.Refresh(r.c)
}

func (r *providerCardRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *providerCardRenderer) Destroy()                     {}
