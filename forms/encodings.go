package forms

import (
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/codec"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// encodingByID returns the encoding of the text column; nil - ASCII, which
// the view draws by itself
func encodingByID(id string) hexview.TextEncoding {
	if id == "" || id == "ascii" {
		return nil
	}
	if e := codec.ByID(id); e != nil {
		return e
	}
	return nil
}

// currentEncoding is the encoding of the text column, ASCII when none is chosen
func currentEncoding() codec.Encoding {
	if e := codec.ByID(config.GetSettings().View.Encoding); e != nil {
		return e
	}
	return codec.ByID("ascii")
}

// encodingName is the name of the encoding of the text column, for the status bar
func (c *MainForm) encodingName() string {
	return currentEncoding().Name()
}

func (c *MainForm) registerEncodingCommands() {
	for _, e := range codec.Encodings() {
		c.radio("view.enc."+e.ID(), fixed(e.Name()),
			func(vs config.ViewSettings) bool { return currentEncoding().ID() == e.ID() },
			func(vs *config.ViewSettings) { vs.Encoding = e.ID() })
	}
}

func (c *MainForm) buildEncodingMenu(view *ui.ContextMenu) {
	var ids []string
	for _, e := range codec.Encodings() {
		ids = append(ids, "view.enc."+e.ID())
	}
	c.subMenu(view, func() string { return T().MenuEncoding }, ids...)
}
