package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// darkTheme is a near-black, blue-accented theme used for the wintp
// settings window instead of Fyne's default light/dark palette.
type darkTheme struct{}

var _ fyne.Theme = (*darkTheme)(nil)

func (darkTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0x0d, G: 0x0d, B: 0x0f, A: 0xff}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 0xf2, G: 0xf2, B: 0xf3, A: 0xff}
	case theme.ColorNameButton, theme.ColorNameInputBackground:
		return color.NRGBA{R: 0x17, G: 0x17, B: 0x1a, A: 0xff}
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 0x1d, G: 0x1d, B: 0x20, A: 0xff}
	case theme.ColorNameInputBorder, theme.ColorNameSeparator:
		return color.NRGBA{R: 0x2a, G: 0x2a, B: 0x2e, A: 0xff}
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return color.NRGBA{R: 0x3b, G: 0x82, B: 0xf6, A: 0xff}
	case theme.ColorNameHover, theme.ColorNamePressed:
		return color.NRGBA{R: 0x22, G: 0x22, B: 0x26, A: 0xff}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 0x55, G: 0x55, B: 0x5c, A: 0xff}
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 0x6b, G: 0x6b, B: 0x70, A: 0xff}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 0x22, G: 0xc5, B: 0x5e, A: 0xff}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 0xf5, G: 0x9e, B: 0x0b, A: 0xff}
	case theme.ColorNameError:
		return color.NRGBA{R: 0xef, G: 0x44, B: 0x44, A: 0xff}
	case theme.ColorNameForegroundOnPrimary, theme.ColorNameForegroundOnError,
		theme.ColorNameForegroundOnSuccess, theme.ColorNameForegroundOnWarning:
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	case theme.ColorNameHeaderBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 0x15, G: 0x15, B: 0x17, A: 0xff}
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 0x3a, G: 0x3a, B: 0x3e, A: 0xaa}
	case theme.ColorNameScrollBarBackground:
		return color.Transparent
	case theme.ColorNameSelection:
		return color.NRGBA{R: 0x3b, G: 0x82, B: 0xf6, A: 0x55}
	case theme.ColorNameShadow:
		return color.NRGBA{A: 0x66}
	case theme.ColorNameHyperlink:
		return color.NRGBA{R: 0x60, G: 0xa5, B: 0xfa, A: 0xff}
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
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 8
	}
	return theme.DefaultTheme().Size(name)
}

// ApplyTheme installs wintp's custom dark theme on the given app.
func ApplyTheme(a fyne.App) {
	a.Settings().SetTheme(darkTheme{})
}
