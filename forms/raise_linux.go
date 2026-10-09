package forms

import "github.com/ipoluianov/nui/ui"

// raiseWindow brings a minimized window or one under the others to the top.
// Show does nothing for a shown window, so it is hidden first: mapping the
// window again brings it out of the minimized state, on top of the others.
// The window manager forgets the maximized state of a hidden window, so it is set again.
func raiseWindow(form *ui.Form) {
	maximized := form.IsMaximized()
	form.Hide()
	form.Show()
	if maximized {
		form.Maximize()
	}
}
