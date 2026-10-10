package forms

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/install"
	"github.com/ipoluianov/nui/ui"
)

// StatusBar shows the state of the document and the install link
type StatusBar struct {
	ui.Widget

	lblInfo *ui.Label
	// The long operations running in the background (6.1): the link cancels them
	lblCancel *ui.Label
	tasks     map[int]context.CancelFunc
	nextTask  int

	// What the install link does: install, update or uninstall
	installStatus install.Status
}

func NewStatusBar() *StatusBar {
	var c StatusBar
	c.InitWidget()
	c.SetPanelPadding(4)
	c.lblInfo = ui.NewLabel("")
	c.AddWidget(0, 0, c.lblInfo)
	c.lblCancel = newLinkLabel(func() string { return T().Cancel }, c.cancelTasks)
	c.lblCancel.SetVisible(false)
	c.AddWidget(0, 1, c.lblCancel)
	c.AddWidget(0, 2, ui.NewHSpacer())
	// A downloaded copy offers to install or update itself, the installed one to be removed
	c.installStatus = install.CurrentStatus()
	if c.installStatus != install.StatusNone {
		c.AddWidget(0, 3, newLinkLabel(c.installText, c.onInstallLink))
	}
	return &c
}

// startTask counts a long operation in: the Cancel link shows while any
// runs (6.1). Returns its context and the function that ends it, called on
// the goroutine of the window.
func (c *StatusBar) startTask() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	if c.tasks == nil {
		c.tasks = make(map[int]context.CancelFunc)
	}
	id := c.nextTask
	c.nextTask++
	c.tasks[id] = cancel
	c.lblCancel.SetVisible(true)
	return ctx, func() {
		cancel()
		delete(c.tasks, id)
		c.lblCancel.SetVisible(len(c.tasks) > 0)
	}
}

// cancelTasks cancels the operations running
func (c *StatusBar) cancelTasks() {
	for _, cancel := range c.tasks {
		cancel()
	}
}

// background runs work off the window, its progress shown in the status bar
// as text of the percent of total, with the Cancel link (6.1); done gets
// the error of work (context.Canceled when canceled) on the goroutine of the
// window. Returns the function that cancels it.
func (c *MainForm) background(text func(percent int) string, total int64, work func(ctx context.Context, progress func(done int64)) error, done func(err error)) context.CancelFunc {
	form := c.Form()
	taskCtx, end := c.status.startTask()
	ctx, cancel := context.WithCancel(taskCtx)
	c.status.SetInfo(text(0))
	var lastShown atomic.Int64
	progress := func(d int64) {
		// Shown at most ten times a second
		now := time.Now().UnixMilli()
		if now-lastShown.Load() < 100 || total <= 0 {
			return
		}
		lastShown.Store(now)
		pct := int(min(d, total) * 100 / total)
		form.Invoke(func() { c.status.SetInfo(text(pct)) })
	}
	go func() {
		err := work(ctx, progress)
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		form.Invoke(func() {
			cancel()
			end()
			c.updateStatus()
			done(err)
		})
	}()
	return cancel
}

// SetInfo sets the text on the left
func (c *StatusBar) SetInfo(text string) {
	c.lblInfo.SetText(text)
}

// newLinkLabel creates a hyperlink-style label with the text from text() that calls onClick on left click.
// It is underlined only under the mouse, so a row of links stays quiet.
func newLinkLabel(text func() string, onClick func()) *ui.Label {
	lbl := ui.NewLabel("")
	lbl.SetTextFunc(text)
	lbl.SetForegroundColor(colorLink.get())
	lbl.SetOnMouseEnter(func() { lbl.SetUnderline(true) })
	lbl.SetOnMouseLeave(func() { lbl.SetUnderline(false) })
	linkLabels = append(linkLabels, lbl)
	lbl.SetMouseCursor(ui.MouseCursorPointer)
	lbl.SetOnMouseDown(func(button ui.MouseButton, x int, y int, mods ui.KeyModifiers) bool {
		if button != ui.MouseButtonLeft {
			return false
		}
		onClick()
		return true
	})
	return lbl
}

// openDocs opens the docs on the site; campaign tells which place in the app the visit came from
func openDocs(parent ui.Widgeter, campaign string) {
	if err := app.OpenSiteURL(app.DocsURL, campaign); err != nil {
		ui.ShowMessageBox(parent, T().Error, err.Error())
	}
}

// installText is the text of the install link for what it does now
func (c *StatusBar) installText() string {
	switch c.installStatus {
	case install.StatusUpdate:
		return T().Update
	case install.StatusUninstall:
		return T().Uninstall
	}
	return T().Install
}

func (c *StatusBar) onInstallLink() {
	if c.installStatus == install.StatusUninstall {
		c.onUninstall()
		return
	}
	c.onInstall()
}

// onInstall copies the application to ~/.altbins and registers it, then
// quits for the installed copy to start (see main). Over an older installed
// version it is the same: that one is replaced.
func (c *StatusBar) onInstall() {
	ui.ShowQuestionMessageBoxOKCancel(c, c.installText(), T().InstallAsk(install.Dir()), func() {
		if err := install.Install(); err != nil {
			ui.ShowMessageBox(c, T().Error, T().InstallFailed(err.Error()))
			return
		}
		install.RelaunchAfterExit()
		lastCreatedMainWidget.SaveWindowState()
		lastCreatedMainWidget.Form().Close()
	}, nil)
}

// onUninstall removes the installed copy; the settings stay.
// When that is this copy, it quits and its binary is deleted once it has.
func (c *StatusBar) onUninstall() {
	ui.ShowQuestionMessageBoxOKCancel(c, T().Uninstall, T().UninstallAsk(config.ConfigDirectory()), func() {
		installed := install.IsInstalledCopy()
		if err := install.Uninstall(); err != nil {
			ui.ShowMessageBox(c, T().Error, err.Error())
			return
		}
		if installed {
			lastCreatedMainWidget.SaveWindowState()
			lastCreatedMainWidget.Form().Close()
			return
		}
		c.installStatus = install.CurrentStatus()
		ui.ShowToast(c, T().Uninstalled, ui.ToastSuccess)
	}, nil)
}
