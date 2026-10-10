package bootimg

import (
	"fmt"
	"strings"
)

// Flattened device tree (the Devicetree Specification, chapter 5) and the
// U-Boot FIT image, which is a device tree with the images in /images

const (
	fdtMagic     = 0xD00DFEED
	fdtBeginNode = 1
	fdtEndNode   = 2
	fdtProp      = 3
	fdtNop       = 4
	fdtEnd       = 9

	maxFDTTokens = 1 << 20
	maxFDTDepth  = 64
)

// fdtNode is a node of the tree as far as FIT needs it
type fdtNode struct {
	name     string
	props    map[string]fdtValue
	children []*fdtNode
}

// fdtValue is where the value of a property is in the image
type fdtValue struct {
	off, size int64
	b         []byte // the value when it is small; nil for large ones
}

func (n *fdtNode) child(name string) *fdtNode {
	for _, c := range n.children {
		if c.name == name {
			return c
		}
	}
	return nil
}

// str is a property as text; a list of strings is joined with ", "
func (n *fdtNode) str(prop string) string {
	v, ok := n.props[prop]
	if !ok || v.b == nil {
		return ""
	}
	s := strings.TrimRight(string(v.b), "\x00")
	return strings.ReplaceAll(s, "\x00", ", ")
}

// u32 is a property of one cell (or the low cell of two)
func (n *fdtNode) u32(prop string) (int64, bool) {
	v, ok := n.props[prop]
	if !ok || len(v.b) < 4 {
		return 0, false
	}
	return int64(be32(v.b, len(v.b)-4)), true
}

// addr is an address property of one or two cells
func (n *fdtNode) addr(prop string) string {
	v, ok := n.props[prop]
	switch {
	case !ok:
		return ""
	case len(v.b) == 4:
		return fmt.Sprintf("0x%08X", be32(v.b, 0))
	case len(v.b) == 8:
		return fmt.Sprintf("0x%08X%08X", be32(v.b, 0), be32(v.b, 4))
	}
	return ""
}

func detectFDT(d *reader, head []byte) *Image {
	if len(head) < 40 || be32(head, 0) != fdtMagic {
		return nil
	}
	total := int64(be32(head, 4))
	structOff, stringsOff := int64(be32(head, 8)), int64(be32(head, 12))
	version := be32(head, 20)
	stringsSize := int64(be32(head, 32))
	structSize := int64(be32(head, 36))
	if version < 17 {
		structSize = total - structOff // no size_dt_struct before v17
	}
	img := &Image{Format: "Device tree"}
	img.info("total size", "%d", total)
	img.info("version", "%d", version)
	if !d.in(structOff, structSize) || !d.in(stringsOff, stringsSize) || stringsSize > maxRead {
		img.info("error", "the structure or the strings are out of the file")
		return img
	}
	names := d.bytes(stringsOff, stringsSize)
	root, err := fdtParse(d, structOff, structSize, names)
	if err != "" {
		img.info("error", "%s", err)
	}
	if root == nil {
		return img
	}
	if s := root.str("description"); s != "" {
		img.info("description", "%s", s)
	}
	if s := root.str("model"); s != "" {
		img.info("model", "%s", s)
	}
	if s := root.str("compatible"); s != "" {
		img.info("compatible", "%s", s)
	}
	images := root.child("images")
	if images == nil {
		return img
	}
	img.Format = "U-Boot FIT"
	if t, ok := root.u32("timestamp"); ok {
		img.info("timestamp", "%d", t)
	}
	// External data follows the tree, aligned to 4
	extBase := alignUp(total, 4)
	for _, n := range images.children {
		fitImage(img, d, n, extBase)
	}
	if conf := root.child("configurations"); conf != nil {
		if def := conf.str("default"); def != "" {
			img.info("default configuration", "%s", def)
		}
		for _, c := range conf.children {
			var refs []string
			for _, p := range []string{"description", "kernel", "ramdisk", "fdt", "loadables", "firmware", "script"} {
				if s := c.str(p); s != "" {
					refs = append(refs, p+"="+s)
				}
			}
			img.info("configuration "+c.name, "%s", strings.Join(refs, "; "))
		}
	}
	return img
}

// fitImage adds the data of an /images node and its signatures
func fitImage(img *Image, d *reader, n *fdtNode, extBase int64) {
	typ := n.str("type")
	var desc []string
	for _, p := range []string{"description", "type", "arch", "os", "compression"} {
		if s := n.str(p); s != "" {
			desc = append(desc, p+"="+s)
		}
	}
	for _, p := range []string{"load", "entry"} {
		if s := n.addr(p); s != "" {
			desc = append(desc, p+"="+s)
		}
	}
	img.info("image "+n.name, "%s", strings.Join(desc, "; "))

	off, size := int64(-1), int64(0)
	if v, ok := n.props["data"]; ok {
		off, size = v.off, v.size
	} else if s, ok := n.u32("data-size"); ok {
		size = s
		if p, ok := n.u32("data-position"); ok {
			off = p
		} else if o, ok := n.u32("data-offset"); ok {
			off = extBase + o
		}
	}
	if off >= 0 && d.in(off, size) {
		if img.add(d, n.name, fitKind(typ), off, size) {
			p := &img.Parts[len(img.Parts)-1]
			if c := n.str("compression"); p.Compression == "" && c != "" && c != "none" {
				p.Compression = c
			}
		}
	}
	for _, c := range n.children {
		if v, ok := c.props["value"]; ok && strings.HasPrefix(c.name, "signature") {
			img.add(d, n.name+"/"+c.name, "signature", v.off, v.size)
		}
	}
}

func fitKind(typ string) string {
	switch typ {
	case "kernel", "kernel_noload":
		return "kernel"
	case "ramdisk":
		return "ramdisk"
	case "flat_dt", "fdt":
		return "dtb"
	case "script":
		return "config"
	}
	return "data"
}

// fdtParse walks the structure block; it returns the tree read so far and
// an error text when the block is broken
func fdtParse(d *reader, off, size int64, names []byte) (*fdtNode, string) {
	end := off + size
	var stack []*fdtNode
	var root *fdtNode
	word := func() (uint32, bool) {
		b := d.bytes(off, 4)
		if b == nil || off+4 > end {
			return 0, false
		}
		off += 4
		return be32(b, 0), true
	}
	for tokens := 0; tokens < maxFDTTokens; tokens++ {
		tok, ok := word()
		if !ok {
			return root, "the structure block ends without FDT_END"
		}
		switch tok {
		case fdtBeginNode:
			b := d.bytes(off, min(256, end-off))
			if b == nil {
				return root, "a node name is cut"
			}
			i := strings.IndexByte(string(b), 0)
			if i < 0 {
				return root, "a node name is too long"
			}
			off += alignUp(int64(i)+1, 4)
			n := &fdtNode{name: string(b[:i]), props: map[string]fdtValue{}}
			if len(stack) == 0 {
				if root != nil {
					return root, "a second root node"
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			}
			if len(stack) >= maxFDTDepth {
				return root, "the nodes are nested too deep"
			}
			stack = append(stack, n)
		case fdtEndNode:
			if len(stack) == 0 {
				return root, "FDT_END_NODE without a node"
			}
			stack = stack[:len(stack)-1]
		case fdtProp:
			l, ok1 := word()
			no, ok2 := word()
			if !ok1 || !ok2 || int64(l) > end-off || len(stack) == 0 {
				return root, "a property is cut"
			}
			pname := ""
			if int64(no) < int64(len(names)) {
				pname = cstr(names[no:])
			}
			v := fdtValue{off: off, size: int64(l)}
			if l <= 4096 {
				v.b = d.bytes(off, int64(l))
			}
			stack[len(stack)-1].props[pname] = v
			off += alignUp(int64(l), 4)
		case fdtNop:
		case fdtEnd:
			return root, ""
		default:
			return root, fmt.Sprintf("unknown token 0x%X", tok)
		}
	}
	return root, "too many tokens"
}
