package forms

import (
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/nui/ui"
)

// SettingsDialog edits the application options (config.Settings)
type SettingsDialog struct {
	ui.DialogContent

	chkBackup *ui.Checkbox
	chkOnTop  *ui.Checkbox
	txtColors *ui.TextBox

	// The first item is the system language, then the languages list
	cmbLanguage *ui.ComboBox
	cmbTheme    *ui.ComboBox

	btnOK     *ui.Button
	btnCancel *ui.Button

	onAccept func(config.Settings)
}

func NewSettingsDialog(settings config.Settings, onAccept func(config.Settings)) *SettingsDialog {
	var c SettingsDialog
	c.InitWidget()
	c.onAccept = onAccept

	checks := ui.NewPanel()
	c.AddWidget(0, 0, checks)
	c.AddWidget(1, 0, ui.NewVSpacer())
	buttons := ui.NewPanel()
	c.AddWidget(2, 0, buttons)

	c.chkBackup = ui.NewCheckbox(T().BackupOnSave)
	c.chkBackup.SetChecked(settings.Backup)
	c.chkBackup.SetXExpandable(true)
	checks.AddWidget(0, 0, c.chkBackup)
	c.chkOnTop = ui.NewCheckbox(T().AlwaysOnTop)
	c.chkOnTop.SetChecked(settings.AlwaysOnTop)
	c.chkOnTop.SetXExpandable(true)
	checks.AddWidget(1, 0, c.chkOnTop)

	// Label and combo box rows, the combo boxes aligned in a column
	choices := ui.NewPanel()
	choices.SetPanelPadding(0)
	checks.AddWidget(2, 0, choices)
	choices.AddWidget(0, 0, ui.NewLabel(T().Language))
	c.cmbLanguage = ui.NewComboBox()
	c.cmbLanguage.AddItem(T().LanguageSystem, "")
	c.cmbLanguage.SetSelectedIndex(0)
	for i, l := range languages {
		c.cmbLanguage.AddItem(l.name, l.tag)
		if l.tag == settings.Language {
			c.cmbLanguage.SetSelectedIndex(i + 1)
		}
	}
	choices.AddWidget(0, 1, c.cmbLanguage)
	choices.AddWidget(0, 2, ui.NewHSpacer())

	choices.AddWidget(1, 0, ui.NewLabel(T().Theme))
	c.cmbTheme = ui.NewComboBox()
	c.cmbTheme.AddItem(T().ThemeDark, themeDark)
	c.cmbTheme.AddItem(T().ThemeLight, themeLight)
	c.cmbTheme.SetSelectedIndex(0)
	if settings.Theme == themeLight {
		c.cmbTheme.SetSelectedIndex(1)
	}
	choices.AddWidget(1, 1, c.cmbTheme)

	choices.AddWidget(2, 0, ui.NewLabel(T().ColorRules))
	c.txtColors = ui.NewTextBox()
	c.txtColors.SetText(settings.View.ColorRules)
	c.txtColors.SetXExpandable(true)
	choices.AddWidget(2, 1, c.txtColors)
	hint := ui.NewLabel(T().ColorRulesHint)
	hint.SetForegroundColor(colorSecondaryText.get())
	choices.AddWidget(3, 1, hint)

	c.btnOK = ui.NewButton(ui.UIText().OK)
	c.btnOK.SetOnClick(c.Accept)
	c.btnCancel = ui.NewButton(ui.UIText().Cancel)
	c.btnCancel.SetOnClick(c.Cancel)
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, c.btnOK)
	buttons.AddWidget(0, 2, c.btnCancel)

	c.OnDialogShow = func() {
		c.Form().SetTitle(T().MenuSettings)
		c.Form().SetSize(520, 330)
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(c.btnOK)
		c.Form().SetCancelButton(c.btnCancel)
	}
	return &c
}

func (c *SettingsDialog) Accept() {
	s := config.GetSettings()
	s.Backup = c.chkBackup.Checked()
	s.AlwaysOnTop = c.chkOnTop.Checked()
	s.View.ColorRules = c.txtColors.Text()
	s.Language, _ = c.cmbLanguage.SelectedItemData().(string)
	s.Theme, _ = c.cmbTheme.SelectedItemData().(string)
	// The callback changes the window below: it runs on its goroutine
	if c.onAccept != nil {
		c.RunInParent(func() { c.onAccept(s) })
	}
	c.Form().Close()
}

func (c *SettingsDialog) Cancel() {
	c.Form().Close()
}
