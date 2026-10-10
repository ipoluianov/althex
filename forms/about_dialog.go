package forms

import (
	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/nui/ui"
)

type AboutDialog struct {
	ui.DialogContent

	btnWebsite *ui.Button
	btnClose   *ui.Button

	panelContent *ui.Panel
	panelButtons *ui.Panel
}

func NewAboutDialog() *AboutDialog {
	var c AboutDialog
	c.InitWidget()

	c.panelContent = ui.NewPanel()
	c.AddWidget(0, 0, c.panelContent)
	c.AddWidget(1, 0, ui.NewVSpacer())
	c.panelButtons = ui.NewPanel()
	c.AddWidget(2, 0, c.panelButtons)

	lblName := ui.NewLabel(app.DisplayName)
	lblName.SetFontSize(24)
	lblName.SetTextAlign(ui.HAlignCenter)
	lblVersion := ui.NewLabel(T().Version + " " + app.Version)
	lblVersion.SetTextAlign(ui.HAlignCenter)
	lblAuthor := ui.NewLabel(T().Author + " " + app.Author)
	lblAuthor.SetTextAlign(ui.HAlignCenter)
	lblCopyright := ui.NewLabel(app.Copyright())
	lblCopyright.SetTextAlign(ui.HAlignCenter)
	lblLicense := ui.NewLabel(T().License + " " + app.License)
	lblLicense.SetTextAlign(ui.HAlignCenter)
	lblWebsite := ui.NewLabel(app.Website)
	lblWebsite.SetTextAlign(ui.HAlignCenter)

	c.panelContent.AddWidget(0, 0, lblName)
	c.panelContent.AddWidget(1, 0, lblVersion)
	c.panelContent.AddWidget(2, 0, lblAuthor)
	c.panelContent.AddWidget(3, 0, lblCopyright)
	c.panelContent.AddWidget(4, 0, lblLicense)
	c.panelContent.AddWidget(5, 0, lblWebsite)

	c.btnWebsite = ui.NewButton(T().VisitWebsite)
	c.btnWebsite.SetOnClick(c.VisitWebsite)
	c.btnClose = ui.NewButton(T().Close)
	c.btnClose.SetOnClick(c.Close)

	// The notices of what is built in: their licences ask for them
	btnLicenses := ui.NewButton(T().License)
	btnLicenses.SetOnClick(func() { c.ShowDialog(NewNoticesDialog()) })

	c.panelButtons.AddWidget(0, 0, ui.NewHSpacer())
	c.panelButtons.AddWidget(0, 1, btnLicenses)
	c.panelButtons.AddWidget(0, 2, c.btnWebsite)
	c.panelButtons.AddWidget(0, 3, c.btnClose)

	c.OnDialogShow = func() {
		c.Form().SetTitle(T().AboutTitle(app.DisplayName))
		c.Form().SetSize(400, 300)
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(c.btnClose)
		c.Form().SetCancelButton(c.btnClose)
	}

	return &c
}

// NoticesDialog shows the licences of AltHex and of what is built into it
type NoticesDialog struct {
	ui.DialogContent
}

func NewNoticesDialog() *NoticesDialog {
	c := &NoticesDialog{}
	c.InitWidget()
	text := ui.NewTextBox()
	text.SetMultiline(true)
	text.SetReadOnly(true)
	text.SetText(app.Notices)
	text.SetXExpandable(true)
	text.SetYExpandable(true)
	c.AddWidget(0, 0, text)
	buttons := ui.NewPanel()
	c.AddWidget(1, 0, buttons)
	close := ui.NewButton(T().Close)
	close.SetOnClick(func() { c.Form().Close() })
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, close)
	c.OnDialogShow = func() {
		c.Form().SetTitle(T().License)
		c.Form().SetSize(760, 560)
		c.Form().MoveToCenterOfParent()
		c.Form().SetCancelButton(close)
	}
	return c
}

func (c *AboutDialog) VisitWebsite() {
	if err := app.OpenSiteURL(app.Website, "about_dialog"); err != nil {
		ui.ShowMessageBox(c, T().Error, err.Error())
	}
}

func (c *AboutDialog) Close() {
	c.Form().Close()
}
