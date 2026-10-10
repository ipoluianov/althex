package api

import (
	"encoding/json"

	"github.com/ipoluianov/althex/rpc"
)

// Instructions tell an MCP client what the server is for
const Instructions = `AltHex is a hex editor. These tools work on the documents open in it ` +
	`(or on the files given to "althex mcp" when no window runs). Call list_documents first: ` +
	`a document is named by its id; an empty id means the one the user looks at. ` +
	`Offsets and sizes are in bytes, decimal. Reading never changes anything. ` +
	`propose_edits and save_document ask the user, who may decline; the edits become one undo step ` +
	`and are highlighted. Templates (list_templates, apply_template) parse file formats; ` +
	`get_structure walks the tree of fields they make.`

const docProp = `"document": {"type": "string", "description": "The id from list_documents; empty or absent - the current document"}`

func schema(props string, required ...string) json.RawMessage {
	req, _ := json.Marshal(required)
	if required == nil {
		req = []byte("[]")
	}
	return json.RawMessage(`{"type": "object", "properties": {` + props + `}, "required": ` + string(req) + `, "additionalProperties": false}`)
}

var tools = []tool{
	{rpc.Tool{
		Name: "list_documents", Title: "List documents", ReadOnly: true,
		Description: "The open documents: id, name, path, size, whether modified or read-only, which is current, the caret and the selection.",
		InputSchema: schema(``),
	}, listDocuments},
	{rpc.Tool{
		Name: "open_document", Title: "Open a file",
		Description: "Opens a file (read-only for disks). The user is asked first in the window.",
		InputSchema: schema(`"path": {"type": "string", "description": "An absolute path"}`, "path"),
	}, openDocument},
	{rpc.Tool{
		Name: "read", Title: "Read bytes", ReadOnly: true,
		Description: "Reads bytes of a document. Formats: dump (offset, hex, ASCII; up to 64 KB), hex, base64 (up to 1 MB), text (decoded; up to 1 MB). Unreadable sectors of disks are listed and read as zeros.",
		InputSchema: schema(docProp + `,
			"offset": {"type": "integer", "minimum": 0, "default": 0},
			"length": {"type": "integer", "minimum": 1, "default": 256},
			"format": {"type": "string", "enum": ["dump", "hex", "base64", "text"], "default": "dump"},
			"encoding": {"type": "string", "description": "For text: utf8 (default), ascii, latin1, utf16le, utf16be, cp1251, cp437, ..."}`),
	}, read},
	{rpc.Tool{
		Name: "search", Title: "Search", ReadOnly: true,
		Description: "Finds bytes or text in a document and returns the offsets of the matches. Hex patterns take ?? for any byte and nibble wildcards (4?).",
		InputSchema: schema(docProp + `,
			"hex": {"type": "string", "description": "Bytes in hex, e.g. \"7F 45 4C 46\" or \"FF ?? 00\""},
			"text": {"type": "string"},
			"encoding": {"type": "string", "description": "The encoding of text; utf8 by default"},
			"ignore_case": {"type": "boolean", "default": false},
			"from": {"type": "integer", "minimum": 0, "default": 0},
			"to": {"type": "integer", "minimum": 0, "description": "The end of the range; 0 - the end of the document"},
			"align": {"type": "integer", "minimum": 0, "description": "Only offsets that are multiples of it"},
			"max_results": {"type": "integer", "minimum": 1, "maximum": 10000, "default": 100}`),
	}, find},
	{rpc.Tool{
		Name: "hash", Title: "Hashes and checksums", ReadOnly: true,
		Description: "Computes hashes of a document or of a range: crc8, crc16-*, crc32, crc32c, crc64-*, adler32, xor8, sum8/16/32, md5, sha1, sha256, sha512, blake3 and others.",
		InputSchema: schema(docProp + `,
			"algorithms": {"type": "array", "items": {"type": "string"}, "default": ["crc32", "md5", "sha256"]},
			"offset": {"type": "integer", "minimum": 0, "default": 0},
			"length": {"type": "integer", "minimum": 0, "description": "0 - to the end"}`),
	}, hashes},
	{rpc.Tool{
		Name: "list_templates", Title: "List templates", ReadOnly: true,
		Description: "The templates of file formats (ELF, PE, PNG, ZIP, MBR, GPT, FAT, ext4, UF2, ...). With a document, also the one that fits it.",
		InputSchema: schema(docProp),
	}, listTemplates},
	{rpc.Tool{
		Name: "apply_template", Title: "Apply a template", ReadOnly: true,
		Description: "Parses a document with a template (by name; empty - the one that fits) and keeps the tree of fields for get_structure. Returns the number of fields, warnings, errors and the top of the tree.",
		InputSchema: schema(docProp + `,
			"template": {"type": "string"},
			"start": {"type": "integer", "minimum": 0, "default": 0, "description": "Where the format starts in the document"}`),
	}, applyTemplate},
	{rpc.Tool{
		Name: "get_structure", Title: "Get the structure", ReadOnly: true,
		Description: "The tree of fields of the template applied to a document (the fitting one is applied when none was): name, path, type, offset, size, value, comment, children. Start from the root, a path (\"header.e_entry\", \"sections[2]\") or the field at an offset.",
		InputSchema: schema(docProp + `,
			"path": {"type": "string"},
			"offset": {"type": "integer", "minimum": 0},
			"depth": {"type": "integer", "minimum": 0, "maximum": 8, "default": 2},
			"max_children": {"type": "integer", "minimum": 1, "maximum": 1000, "default": 100}`),
	}, getStructure},
	{rpc.Tool{
		Name: "propose_edits", Title: "Propose edits", Destructive: true,
		Description: "Proposes changes of a document: each edit overwrites, inserts (hex bytes) or deletes (a count) at an offset; the offsets of an edit are of the document as the edits before it left it. The user sees them and agrees or declines; agreed, they are one undo step, highlighted, not saved to the file.",
		InputSchema: schema(docProp+`,
			"description": {"type": "string", "description": "Why: shown to the user"},
			"edits": {"type": "array", "minItems": 1, "items": {"type": "object", "properties": {
				"offset": {"type": "integer", "minimum": 0},
				"overwrite": {"type": "string", "description": "Hex bytes written over"},
				"insert": {"type": "string", "description": "Hex bytes inserted"},
				"delete": {"type": "integer", "minimum": 1, "description": "Bytes deleted"}
			}, "required": ["offset"], "additionalProperties": false}}`, "edits"),
	}, proposeEdits},
	{rpc.Tool{
		Name: "save_document", Title: "Save", Destructive: true,
		Description: "Saves a document to its file; the user is asked first.",
		InputSchema: schema(docProp),
	}, save},
	{rpc.Tool{
		Name: "show", Title: "Show in the window", ReadOnly: true,
		Description: "Selects bytes of a document in the window and scrolls to them, to show the user something.",
		InputSchema: schema(docProp+`,
			"offset": {"type": "integer", "minimum": 0},
			"length": {"type": "integer", "minimum": 0, "default": 0}`, "offset"),
	}, show},
}
