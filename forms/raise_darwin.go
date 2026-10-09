package forms

import "github.com/ipoluianov/nui/ui"

// raiseWindow brings the window to the front and activates the application;
// a minimized window comes out of the Dock
func raiseWindow(form *ui.Form) {
	form.Show()
}
