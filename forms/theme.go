package forms

import (
	"image/color"

	"github.com/ipoluianov/nui/ui"
)

// The color themes offered in the settings (config.Settings.Theme)
const (
	themeDark  = ""
	themeLight = "light"
)

// themeColors is a color of the application in the dark and the light theme
type themeColors struct {
	dark, light color.RGBA
}

// get returns the color for the current theme
func (c themeColors) get() color.RGBA {
	if ui.IsDarkTheme {
		return c.dark
	}
	return c.light
}

var (
	colorLink          = themeColors{ui.ColorFromHex("#3d8bf2"), ui.ColorFromHex("#1d6fb5")}
	colorSecondaryText = themeColors{ui.ColorFromHex("#8c8c8c"), ui.ColorFromHex("#555555")}
	colorFound         = themeColors{color.RGBA{0xb0, 0x8a, 0x10, 0x90}, color.RGBA{0xf5, 0xc5, 0x18, 0x90}}
	colorChanged       = themeColors{color.RGBA{0xc0, 0x30, 0x30, 0x80}, color.RGBA{0xf0, 0x80, 0x80, 0x90}}
	colorShifted       = themeColors{color.RGBA{0x30, 0x90, 0x40, 0x80}, color.RGBA{0x80, 0xd0, 0x90, 0x90}}
	colorModified      = themeColors{ui.ColorFromHex("#ef6c57"), ui.ColorFromHex("#d9412b")}
)

// linkLabels are recolored when the theme changes, see newLinkLabel
var linkLabels []*ui.Label

// ApplyTheme switches the application to the theme of the settings and
// repaints the open windows
func ApplyTheme(theme string) {
	if theme == themeLight {
		ui.ApplyLightTheme()
	} else {
		ui.ApplyDarkTheme()
	}
	for _, lbl := range linkLabels {
		lbl.SetForegroundColor(colorLink.get())
	}
	for _, setIcon := range themedIcons {
		setIcon()
	}
}
