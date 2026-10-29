package ui

import (
	"hash/fnv"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ---------------------------------------------------------------------------
// Layout helpers
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

// fixedWidthLayout gives its content a fixed width and the full height.
type fixedWidthLayout struct{ width float32 }

func (l fixedWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objects {
		if m := o.MinSize().Height; m > h {
			h = m
		}
	}
	return fyne.NewSize(l.width, h)
}

func (l fixedWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(fyne.NewSize(l.width, size.Height))
		o.Move(fyne.NewPos(0, 0))
	}
}

// hGap returns an invisible fixed-width spacer for widening the gap between
// items in an HBox beyond the theme's default padding.
func hGap(width float32) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(width, 1), canvas.NewRectangle(color.Transparent))
}

// vbox stacks objects vertically with a custom gap.
func vbox(gap float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(layout.NewCustomPaddedVBoxLayout(gap), objects...)
}

// hbox lays objects out horizontally with a custom gap.
func hbox(gap float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(layout.NewCustomPaddedHBoxLayout(gap), objects...)
}

// inset pads obj on each side (top, bottom, left, right).
func inset(t, b, l, r float32, obj fyne.CanvasObject) *fyne.Container {
	return container.New(layout.NewCustomPaddedLayout(t, b, l, r), obj)
}

// unpadded cancels a widget's built-in inner padding so its text lines up
// with canvas.Text drawn alongside it.
func unpadded(obj fyne.CanvasObject) fyne.CanvasObject {
	p := -theme.Size(theme.SizeNameInnerPadding)
	return inset(p/2, p/2, p, p, obj)
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

func newText(s string, size float32, c color.Color, bold bool) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	t.TextStyle = fyne.TextStyle{Bold: bold}
	return t
}

// wrapText is a muted, word-wrapped paragraph without label padding.
func wrapText(s string) (*widget.Label, fyne.CanvasObject) {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	l.Importance = widget.LowImportance
	l.SizeName = theme.SizeNameCaptionText
	return l, unpadded(l)
}

// overline is a small uppercase section label (e.g. "CONNECTIONS").
func overline(s string) fyne.CanvasObject {
	return inset(6, 2, 4, 0, newText(s, 10.5, colFaint, true))
}

// sectionHeading is a title with an optional muted description underneath.
func sectionHeading(title, subtitle string) fyne.CanvasObject {
	t := newText(title, 14, colText, true)
	if subtitle == "" {
		return t
	}
	_, sub := wrapText(subtitle)
	return vbox(4, t, sub)
}

// field stacks a small label above its input, the way modern forms do.
func field(label string, input fyne.CanvasObject) fyne.CanvasObject {
	return vbox(6, newText(label, 12, colMuted, true), input)
}

// ---------------------------------------------------------------------------
// card: a rounded, bordered surface grouping related settings.
// ---------------------------------------------------------------------------

func card(title, subtitle string, body ...fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(colCard)
	bg.CornerRadius = 10
	bg.StrokeColor = colBorder
	bg.StrokeWidth = 1

	items := body
	if title != "" {
		items = append([]fyne.CanvasObject{sectionHeading(title, subtitle)}, body...)
	}
	return container.NewStack(bg, inset(18, 18, 20, 20, vbox(14, items...)))
}

// divider is a thin separator line for use inside cards.
func divider() fyne.CanvasObject {
	r := canvas.NewRectangle(colBorder)
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

// ---------------------------------------------------------------------------
// tapSurface: a rounded background with hover / selected states that makes
// arbitrary content tappable (sidebar rows, provider tiles, segments).
// ---------------------------------------------------------------------------

type surfaceStyle struct {
	fill, hover, selFill color.Color
	stroke, selStroke    color.Color
	radius, strokeWidth  float32
}

type tapSurface struct {
	widget.BaseWidget
	style    surfaceStyle
	content  fyne.CanvasObject
	OnTapped func()

	bg       *canvas.Rectangle
	hovered  bool
	selected bool
}

func newTapSurface(style surfaceStyle, content fyne.CanvasObject, onTap func()) *tapSurface {
	s := &tapSurface{style: style, content: content, OnTapped: onTap}
	s.bg = canvas.NewRectangle(style.fill)
	s.bg.CornerRadius = style.radius
	s.bg.StrokeWidth = style.strokeWidth
	s.ExtendBaseWidget(s)
	s.apply()
	return s
}

func (s *tapSurface) apply() {
	fill, stroke := s.style.fill, s.style.stroke
	switch {
	case s.selected:
		fill, stroke = s.style.selFill, s.style.selStroke
	case s.hovered:
		fill = s.style.hover
	}
	s.bg.FillColor = orTransparent(fill)
	s.bg.StrokeColor = orTransparent(stroke)
	s.bg.Refresh()
}

func orTransparent(c color.Color) color.Color {
	if c == nil {
		return color.Transparent
	}
	return c
}

func (s *tapSurface) SetSelected(v bool) {
	if v == s.selected {
		return
	}
	s.selected = v
	s.apply()
}

func (s *tapSurface) Tapped(*fyne.PointEvent) {
	if s.OnTapped != nil {
		s.OnTapped()
	}
}

func (s *tapSurface) MouseIn(*desktop.MouseEvent)    { s.hovered = true; s.apply() }
func (s *tapSurface) MouseMoved(*desktop.MouseEvent) {}
func (s *tapSurface) MouseOut()                      { s.hovered = false; s.apply() }
func (s *tapSurface) Cursor() desktop.Cursor         { return desktop.PointerCursor }

func (s *tapSurface) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(s.bg, s.content))
}

var navRowStyle = surfaceStyle{
	hover:   color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0a},
	selFill: colAccentSoft,
	radius:  6,
}

// ---------------------------------------------------------------------------
// badge: a tinted, rounded tile with a short code (provider initials), whose
// hue is derived from the code so each provider keeps a stable colour.
// ---------------------------------------------------------------------------

var badgeHues = []color.NRGBA{
	{R: 0x81, G: 0x8c, B: 0xf8, A: 0xff}, // indigo
	{R: 0x38, G: 0xbd, B: 0xf8, A: 0xff}, // sky
	{R: 0x34, G: 0xd3, B: 0x99, A: 0xff}, // emerald
	{R: 0xfb, G: 0xbf, B: 0x24, A: 0xff}, // amber
	{R: 0xfb, G: 0x71, B: 0x85, A: 0xff}, // rose
	{R: 0xc0, G: 0x84, B: 0xfc, A: 0xff}, // violet
	{R: 0x2d, G: 0xd4, B: 0xbf, A: 0xff}, // teal
	{R: 0xfb, G: 0x92, B: 0x3c, A: 0xff}, // orange
}

func badgeHue(code string) color.NRGBA {
	h := fnv.New32a()
	_, _ = h.Write([]byte(code))
	return badgeHues[h.Sum32()%uint32(len(badgeHues))]
}

type badge struct {
	root fyne.CanvasObject
	bg   *canvas.Rectangle
	lbl  *canvas.Text
}

func newBadge(code string, size float32) *badge {
	b := &badge{
		bg:  canvas.NewRectangle(color.Transparent),
		lbl: newText("", size*0.34, colText, true),
	}
	b.bg.CornerRadius = size * 0.25
	b.lbl.Alignment = fyne.TextAlignCenter
	b.root = container.NewGridWrap(fyne.NewSize(size, size),
		container.NewStack(b.bg, container.NewCenter(b.lbl)))
	b.SetText(code)
	return b
}

func (b *badge) SetText(code string) {
	hue := badgeHue(code)
	b.bg.FillColor = withAlpha(hue, 0x24)
	b.bg.Refresh()
	b.lbl.Text = code
	b.lbl.Color = hue
	b.lbl.Refresh()
}

// ---------------------------------------------------------------------------
// statusPill: a small tinted pill with a dot and label ("Mounted").
// ---------------------------------------------------------------------------

type statusPill struct {
	root fyne.CanvasObject
	bg   *canvas.Rectangle
	dot  *canvas.Circle
	lbl  *canvas.Text
}

func newStatusPill() *statusPill {
	p := &statusPill{
		bg:  canvas.NewRectangle(color.Transparent),
		dot: canvas.NewCircle(colFaint),
		lbl: newText("", 11.5, colMuted, true),
	}
	p.bg.CornerRadius = 11
	dot := container.NewCenter(container.NewGridWrap(fyne.NewSize(7, 7), p.dot))
	p.root = container.NewCenter(container.NewStack(p.bg, inset(3, 3, 9, 11, hbox(6, dot, p.lbl))))
	return p
}

func (p *statusPill) Set(text string, c color.Color) {
	p.bg.FillColor = withAlpha(c, 0x22)
	p.bg.Refresh()
	p.dot.FillColor = c
	p.dot.Refresh()
	p.lbl.Text = text
	p.lbl.Color = c
	p.lbl.Refresh()
}

// ---------------------------------------------------------------------------
// toggleSwitch: a compact on/off switch (track + knob).
// ---------------------------------------------------------------------------

const (
	toggleWidth  float32 = 36
	toggleHeight float32 = 20
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

func (t *toggleSwitch) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (t *toggleSwitch) MinSize() fyne.Size {
	return fyne.NewSize(toggleWidth, toggleHeight)
}

func (t *toggleSwitch) trackColor() color.Color {
	if t.Checked {
		return colAccent
	}
	return colBorderHi
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

	const knobInset = 3
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

// settingsRow builds a settings-style row: title + muted subtitle on the
// left, the given control vertically centered on the right.
func settingsRow(title, subtitle string, control fyne.CanvasObject) fyne.CanvasObject {
	var text fyne.CanvasObject = newText(title, 13, colText, true)
	if subtitle != "" {
		_, sub := wrapText(subtitle)
		text = vbox(3, text, sub)
	}
	return container.NewBorder(nil, nil, nil, inset(0, 0, 16, 0, container.NewCenter(control)), text)
}

// ---------------------------------------------------------------------------
// segmentedControl: a pill-style group of mutually-exclusive options
// (e.g. VFS cache mode: off/minimal/writes/full).
// ---------------------------------------------------------------------------

var segmentStyle = surfaceStyle{
	hover:   color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x08},
	selFill: colBorderHi,
	radius:  5,
}

type segmentedControl struct {
	root      fyne.CanvasObject
	segments  map[string]*tapSurface
	labels    map[string]*canvas.Text
	Selected  string
	OnChanged func(string)
}

func newSegmentedControl(options []string, changed func(string)) *segmentedControl {
	sc := &segmentedControl{
		segments:  make(map[string]*tapSurface, len(options)),
		labels:    make(map[string]*canvas.Text, len(options)),
		OnChanged: changed,
	}
	grid := container.New(layout.NewGridLayoutWithColumns(len(options)))
	for _, opt := range options {
		opt := opt
		lbl := newText(opt, 12.5, colMuted, true)
		seg := newTapSurface(segmentStyle, inset(7, 7, 12, 12, container.NewCenter(lbl)),
			func() { sc.setSelected(opt, true) })
		sc.segments[opt] = seg
		sc.labels[opt] = lbl
		grid.Add(seg)
	}
	bg := canvas.NewRectangle(colInput)
	bg.CornerRadius = 7
	bg.StrokeColor = colBorder
	bg.StrokeWidth = 1
	sc.root = container.NewStack(bg, inset(3, 3, 3, 3, grid))
	return sc
}

func (sc *segmentedControl) setSelected(v string, notify bool) {
	sc.Selected = v
	for opt, seg := range sc.segments {
		seg.SetSelected(opt == v)
		lbl := sc.labels[opt]
		if opt == v {
			lbl.Color = colText
		} else {
			lbl.Color = colMuted
		}
		lbl.Refresh()
	}
	if notify && sc.OnChanged != nil {
		sc.OnChanged(v)
	}
}

// SetSelected updates the control's state without firing OnChanged.
func (sc *segmentedControl) SetSelected(v string) { sc.setSelected(v, false) }

func (sc *segmentedControl) CanvasObject() fyne.CanvasObject { return sc.root }

// ---------------------------------------------------------------------------
// providerCard: a selectable tile in the provider picker grid - badge, bold
// title, muted subtitle, accent border when selected.
// ---------------------------------------------------------------------------

var providerTileStyle = surfaceStyle{
	fill:        colInput,
	hover:       color.NRGBA{R: 0x24, G: 0x25, B: 0x2b, A: 0xff},
	selFill:     withAlpha(colAccent, 0x1c),
	stroke:      colBorder,
	selStroke:   colAccent,
	radius:      8,
	strokeWidth: 1.5,
}

type providerCard struct {
	title   string
	surface *tapSurface
}

func newProviderCard(def *ProviderDef, onTap func()) *providerCard {
	b := newBadge(badgeFor(def), 30)
	text := vbox(3, newText(def.Name, 12.5, colText, true))
	if def.Subtitle != "" {
		sub := newText(def.Subtitle, 11, colMuted, false)
		text.Add(sub)
	}
	content := inset(10, 10, 10, 10,
		container.NewBorder(nil, nil, inset(0, 0, 0, 10, container.NewCenter(b.root)), nil,
			container.NewVBox(layout.NewSpacer(), text, layout.NewSpacer())))
	return &providerCard{title: def.Name, surface: newTapSurface(providerTileStyle, content, onTap)}
}

func (c *providerCard) SetSelected(v bool)        { c.surface.SetSelected(v) }
func (c *providerCard) Object() fyne.CanvasObject { return c.surface }
