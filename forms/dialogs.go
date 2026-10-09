package forms

import (
	"strconv"
	"strings"

	"github.com/ipoluianov/nui/ui"
)

// SaveChangesDialog asks what to do with the edits of a document being
// closed: save them, drop them or not close it
type SaveChangesDialog struct {
	ui.DialogContent

	btnSave    *ui.Button
	btnDiscard *ui.Button
	btnCancel  *ui.Button
}

// NewSaveChangesDialog calls onSave or onDiscard on the goroutine of the
// window below; nothing when canceled
func NewSaveChangesDialog(name string, onSave, onDiscard func()) *SaveChangesDialog {
	var c SaveChangesDialog
	c.InitWidget()
	text := ui.NewLabel(T().SaveChangesAsk(name))
	c.AddWidget(0, 0, text)
	c.AddWidget(1, 0, ui.NewVSpacer())
	buttons := ui.NewPanel()
	c.AddWidget(2, 0, buttons)

	choose := func(f func()) func() {
		return func() {
			c.RunInParent(f)
			c.Form().Close()
		}
	}
	c.btnSave = ui.NewButton(T().Save)
	c.btnSave.SetOnClick(choose(onSave))
	c.btnDiscard = ui.NewButton(T().DontSave)
	c.btnDiscard.SetOnClick(choose(onDiscard))
	c.btnCancel = ui.NewButton(ui.UIText().Cancel)
	c.btnCancel.SetOnClick(func() { c.Form().Close() })
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, c.btnSave)
	buttons.AddWidget(0, 2, c.btnDiscard)
	buttons.AddWidget(0, 3, c.btnCancel)

	c.OnDialogShow = func() {
		c.Form().SetTitle(T().SaveChangesTitle)
		c.Form().SetSize(460, 150)
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(c.btnSave)
		c.Form().SetCancelButton(c.btnCancel)
	}
	return &c
}

// GoToDialog asks for an offset
type GoToDialog struct {
	ui.DialogContent

	txt       *ui.TextBox
	lblError  *ui.Label
	btnOK     *ui.Button
	btnCancel *ui.Button
	onAccept  func(off int64)
}

func NewGoToDialog(current int64, onAccept func(off int64)) *GoToDialog {
	var c GoToDialog
	c.InitWidget()
	c.onAccept = onAccept
	content := ui.NewPanel()
	c.AddWidget(0, 0, content)
	c.AddWidget(1, 0, ui.NewVSpacer())
	buttons := ui.NewPanel()
	c.AddWidget(2, 0, buttons)

	content.AddWidget(0, 0, ui.NewLabel(T().GoToLabel))
	c.txt = ui.NewTextBox()
	c.txt.SetText("0x" + strconv.FormatInt(current, 16))
	c.txt.SetXExpandable(true)
	content.AddWidget(0, 1, c.txt)
	c.lblError = ui.NewLabel("")
	c.lblError.SetForegroundColor(colorSecondaryText.get())
	content.AddWidget(1, 1, c.lblError)

	c.btnOK = ui.NewButton(ui.UIText().OK)
	c.btnOK.SetOnClick(c.accept)
	c.btnCancel = ui.NewButton(ui.UIText().Cancel)
	c.btnCancel.SetOnClick(func() { c.Form().Close() })
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, c.btnOK)
	buttons.AddWidget(0, 2, c.btnCancel)

	c.OnDialogShow = func() {
		c.Form().SetTitle(T().GoToTitle)
		c.Form().SetSize(420, 160)
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(c.btnOK)
		c.Form().SetCancelButton(c.btnCancel)
		c.txt.Focus()
		c.txt.SelectAllText()
	}
	return &c
}

func (c *GoToDialog) accept() {
	off, ok := ParseOffset(c.txt.Text())
	if !ok {
		c.lblError.SetText(T().GoToBad)
		return
	}
	c.RunInParent(func() { c.onAccept(off) })
	c.Form().Close()
}

// ParseOffset reads an offset: "0x1A2B", "1A2Bh" or "$1A2B" in hex, "6699" in decimal
func ParseOffset(s string) (int64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, " ", ""))
	base := 10
	switch {
	case strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"):
		s, base = s[2:], 16
	case strings.HasPrefix(s, "$"):
		s, base = s[1:], 16
	case strings.HasSuffix(s, "h") || strings.HasSuffix(s, "H"):
		s, base = s[:len(s)-1], 16
	}
	v, err := strconv.ParseInt(s, base, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}
