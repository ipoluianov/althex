package forms

import (
	"errors"
	"strconv"
	"strings"

	"github.com/ipoluianov/althex/core/expr"
	"github.com/ipoluianov/althex/core/ops"
	"github.com/ipoluianov/althex/forms/hexview"
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

// ValueDialog asks for a value as text; onAccept returns an error to show
// (the dialog stays) or nil (it closes). onAccept runs on the goroutine of
// the window below.
type ValueDialog struct {
	ui.DialogContent

	title     string
	txt       *ui.TextBox
	lblError  *ui.Label
	lblHint   *ui.Label
	btnOK     *ui.Button
	btnCancel *ui.Button
	onAccept  func(text string) error
	checking  bool // the value is being checked by the window below
}

func NewValueDialog(title, label, value string, onAccept func(text string) error) *ValueDialog {
	var c ValueDialog
	c.InitWidget()
	c.title = title
	c.onAccept = onAccept
	content := ui.NewPanel()
	c.AddWidget(0, 0, content)
	c.AddWidget(1, 0, ui.NewVSpacer())
	buttons := ui.NewPanel()
	c.AddWidget(2, 0, buttons)

	content.AddWidget(0, 0, ui.NewLabel(label))
	c.txt = ui.NewTextBox()
	c.txt.SetText(value)
	c.txt.SetXExpandable(true)
	content.AddWidget(0, 1, c.txt)
	c.lblHint = ui.NewLabel("")
	c.lblHint.SetForegroundColor(colorSecondaryText.get())
	c.lblHint.SetVisible(false)
	content.AddWidget(1, 1, c.lblHint)
	c.lblError = ui.NewLabel("")
	c.lblError.SetForegroundColor(colorModified.get())
	content.AddWidget(2, 1, c.lblError)

	c.btnOK = ui.NewButton(ui.UIText().OK)
	c.btnOK.SetOnClick(c.accept)
	c.btnCancel = ui.NewButton(ui.UIText().Cancel)
	c.btnCancel.SetOnClick(func() { c.Form().Close() })
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, c.btnOK)
	buttons.AddWidget(0, 2, c.btnCancel)

	c.OnDialogShow = func() {
		c.Form().SetTitle(c.title)
		c.Form().SetSize(460, 150)
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(c.btnOK)
		c.Form().SetCancelButton(c.btnCancel)
		c.txt.Focus()
		c.txt.SelectAllText()
	}
	return &c
}

// SetHint shows a line under the field: what can be typed there
func (c *ValueDialog) SetHint(text string) {
	c.lblHint.SetText(text)
	c.lblHint.SetVisible(text != "")
}

func (c *ValueDialog) accept() {
	if c.checking {
		return
	}
	c.checking = true
	text := c.txt.Text()
	dlg := c.Form()
	// The check runs on the window below, which owns the documents; its
	// answer comes back to the dialog the same way. Waiting for it here
	// would hang: the windows may share one thread (they do on Linux)
	c.RunInParent(func() {
		err := c.onAccept(text)
		dlg.Invoke(func() {
			c.checking = false
			if err != nil {
				c.lblError.SetText(err.Error())
				return
			}
			dlg.Close()
		})
	})
}

// NewGoToDialog asks for an offset to go to: a number or an expression,
// "+n" and "-n" from the caret (NAV-1)
func NewGoToDialog(v *hexview.View, jump func(off int64)) *ValueDialog {
	caret := v.Caret()
	d := NewValueDialog(T().GoToTitle, T().GoToLabel, "0x"+strconv.FormatInt(caret, 16), func(s string) error {
		off, err := evalOffset(s, caret, v.Document().Size())
		if err != nil {
			return err
		}
		jump(off)
		return nil
	})
	d.SetHint(T().GoToHint)
	return d
}

// evalOffset reads an offset typed by the user: an expression like
// "0x400 + 3*512", or with a leading sign from the caret ("+0x10"); the
// names cursor, size and end can be used
func evalOffset(s string, caret, size int64) (int64, error) {
	vars := map[string]int64{"cursor": caret, "caret": caret, "size": size, "end": size}
	off, err := expr.Offset(s, caret, vars)
	if err != nil {
		// Hex as the view shows it, without 0x: "8A3D" (a number of digits
		// only stays decimal)
		h := strings.TrimSpace(s)
		if n, herr := strconv.ParseInt(h, 16, 64); herr == nil && strings.ContainsAny(strings.ToUpper(h), "ABCDEF") {
			off, err = n, nil
		}
	}
	if err != nil {
		return 0, err
	}
	if off > size {
		return 0, errors.New(T().GoToBad)
	}
	return off, nil
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

// FillDialog asks how to fill the selection (EDIT-4)
type FillDialog struct {
	ui.DialogContent

	cmbKind   *ui.ComboBox
	txtValue  *ui.TextBox
	txtStart  *ui.TextBox
	txtStep   *ui.TextBox
	cmbWidth  *ui.ComboBox
	chkBE     *ui.Checkbox
	chkInsert *ui.Checkbox
	lblError  *ui.Label
	btnOK     *ui.Button
	btnCancel *ui.Button
	onAccept  func(f ops.Fill)
}

func NewFillDialog(onAccept func(f ops.Fill)) *FillDialog {
	var c FillDialog
	c.InitWidget()
	c.onAccept = onAccept
	grid := ui.NewPanel()
	c.AddWidget(0, 0, grid)
	c.AddWidget(1, 0, ui.NewVSpacer())
	buttons := ui.NewPanel()
	c.AddWidget(2, 0, buttons)

	row := 0
	addRow := func(label string, w ui.Widgeter) {
		grid.AddWidget(row, 0, ui.NewLabel(label))
		grid.AddWidget(row, 1, w)
		row++
	}
	c.cmbKind = ui.NewComboBox()
	c.cmbKind.AddItem(T().FillPattern, ops.FillPattern)
	c.cmbKind.AddItem(T().FillRandom, ops.FillRandom)
	c.cmbKind.AddItem(T().FillIncrement, ops.FillIncrement)
	c.cmbKind.SetSelectedIndex(0)
	addRow(T().FillKind, c.cmbKind)
	c.txtValue = ui.NewTextBox()
	c.txtValue.SetText("00")
	c.txtValue.SetXExpandable(true)
	addRow(T().FillValue, c.txtValue)
	c.txtStart = ui.NewTextBox()
	c.txtStart.SetText("0")
	addRow(T().FillStart, c.txtStart)
	c.txtStep = ui.NewTextBox()
	c.txtStep.SetText("1")
	addRow(T().FillStep, c.txtStep)
	c.cmbWidth = ui.NewComboBox()
	for _, w := range []int{1, 2, 4, 8} {
		c.cmbWidth.AddItem(strconv.Itoa(w), w)
	}
	c.cmbWidth.SetSelectedIndex(0)
	addRow(T().FillWidth, c.cmbWidth)
	c.chkBE = ui.NewCheckbox(T().BigEndian)
	grid.AddWidget(row, 1, c.chkBE)
	row++
	c.chkInsert = ui.NewCheckbox(T().FillInsert)
	grid.AddWidget(row, 1, c.chkInsert)
	row++
	c.lblError = ui.NewLabel("")
	c.lblError.SetForegroundColor(colorModified.get())
	grid.AddWidget(row, 1, c.lblError)

	c.btnOK = ui.NewButton(ui.UIText().OK)
	c.btnOK.SetOnClick(c.accept)
	c.btnCancel = ui.NewButton(ui.UIText().Cancel)
	c.btnCancel.SetOnClick(func() { c.Form().Close() })
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, c.btnOK)
	buttons.AddWidget(0, 2, c.btnCancel)

	c.OnDialogShow = func() {
		c.Form().SetTitle(T().MenuFill)
		c.Form().SetSize(460, 330)
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(c.btnOK)
		c.Form().SetCancelButton(c.btnCancel)
		c.txtValue.Focus()
	}
	return &c
}

func (c *FillDialog) accept() {
	f := ops.Fill{Kind: c.cmbKind.SelectedItemData().(ops.FillKind), Insert: c.chkInsert.Checked()}
	var err error
	switch f.Kind {
	case ops.FillPattern:
		f.Pattern, err = parseBytes(c.txtValue.Text())
	case ops.FillIncrement:
		var start, step int64
		var ok1, ok2 bool
		start, ok1 = ParseOffset(c.txtStart.Text())
		step, ok2 = ParseOffset(c.txtStep.Text())
		if !ok1 || !ok2 {
			err = errors.New(T().BytesBad)
		}
		f.Start, f.Step = uint64(start), uint64(step)
		f.Width = c.cmbWidth.SelectedItemData().(int)
		f.BigEndian = c.chkBE.Checked()
	}
	if err != nil {
		c.lblError.SetText(err.Error())
		return
	}
	c.RunInParent(func() { c.onAccept(f) })
	c.Form().Close()
}
