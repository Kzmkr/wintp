package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Palette for wintp's dark theme: layered zinc neutrals (window < sidebar <
// card < input) with a single indigo accent, in the style of modern desktop
// tools. Widgets that draw their own chrome use these directly.
var (
	colBackground = color.NRGBA{R: 0x0e, G: 0x0f, B: 0x11, A: 0xff} // content area
	colSidebar    = color.NRGBA{R: 0x13, G: 0x14, B: 0x17, A: 0xff}
	colCard       = color.NRGBA{R: 0x17, G: 0x18, B: 0x1c, A: 0xff}
	colInput      = color.NRGBA{R: 0x1e, G: 0x1f, B: 0x24, A: 0xff}
	colBorder     = color.NRGBA{R: 0x26, G: 0x27, B: 0x2d, A: 0xff}
	colBorderHi   = color.NRGBA{R: 0x34, G: 0x35, B: 0x3d, A: 0xff}
	colText       = color.NRGBA{R: 0xec, G: 0xec, B: 0xef, A: 0xff}
	colMuted      = color.NRGBA{R: 0x8b, G: 0x8d, B: 0x98, A: 0xff}
	colFaint      = color.NRGBA{R: 0x6e, G: 0x71, B: 0x7c, A: 0xff}
	colAccent     = color.NRGBA{R: 0x63, G: 0x66, B: 0xf1, A: 0xff}
	colAccentSoft = color.NRGBA{R: 0x63, G: 0x66, B: 0xf1, A: 0x2e}
	colSuccess    = color.NRGBA{R: 0x22, G: 0xc5, B: 0x5e, A: 0xff}
	colWarning    = color.NRGBA{R: 0xf5, G: 0x9e, B: 0x0b, A: 0xff}
	colError      = color.NRGBA{R: 0xef, G: 0x44, B: 0x44, A: 0xff}
)

// withAlpha returns c with its alpha replaced, for tinted backgrounds.
func withAlpha(c color.Color, a uint8) color.NRGBA {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	n.A = a
	return n
}

// darkTheme is the wintp settings window theme.
type darkTheme struct{}

var _ fyne.Theme = (*darkTheme)(nil)

func (darkTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return colBackground
	case theme.ColorNameForeground:
		return colText
	case theme.ColorNameButton:
		return colInput
	case theme.ColorNameInputBackground:
		return colInput
	case theme.ColorNameDisabledButton:
		return colCard
	case theme.ColorNameInputBorder:
		return colBorderHi
	case theme.ColorNameSeparator:
		return colBorder
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return colAccent
	case theme.ColorNameHover:
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0c}
	case theme.ColorNamePressed:
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x16}
	case theme.ColorNameDisabled:
		return colFaint
	case theme.ColorNamePlaceHolder:
		return colFaint
	case theme.ColorNameSuccess:
		return colSuccess
	case theme.ColorNameWarning:
		return colWarning
	case theme.ColorNameError:
		return colError
	case theme.ColorNameForegroundOnPrimary, theme.ColorNameForegroundOnError,
		theme.ColorNameForegroundOnSuccess, theme.ColorNameForegroundOnWarning:
		return color.White
	case theme.ColorNameHeaderBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return colCard
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x22}
	case theme.ColorNameScrollBarBackground:
		return color.Transparent
	case theme.ColorNameSelection:
		return colAccentSoft
	case theme.ColorNameShadow:
		return color.NRGBA{A: 0x88}
	case theme.ColorNameHyperlink:
		return color.NRGBA{R: 0x81, G: 0x8c, B: 0xf8, A: 0xff}
	}
	return theme.DefaultTheme().Color(name, theme.VariantDark)
}

func (darkTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (darkTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (darkTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return 13
	case theme.SizeNameCaptionText:
		return 11.5
	case theme.SizeNameHeadingText:
		return 20
	case theme.SizeNameSubHeadingText:
		return 15
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 6
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameSeparatorThickness:
		return 1
	case theme.SizeNameInnerPadding:
		return 9
	case theme.SizeNameScrollBar:
		return 6
	case theme.SizeNameScrollBarSmall:
		return 3
	case theme.SizeNameScrollBarRadius:
		return 3
	}
	return theme.DefaultTheme().Size(name)
}

// ApplyTheme installs wintp's custom dark theme on the given app.
func ApplyTheme(a fyne.App) {
	a.Settings().SetTheme(darkTheme{})
}
