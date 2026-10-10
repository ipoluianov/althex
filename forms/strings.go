package forms

import (
	"fmt"

	"github.com/ipoluianov/nui/ui"
	"github.com/ipoluianov/nui/ui/i18n"
)

// Strings are all the texts of the application. A misspelled field is a
// compile error; a field a language leaves empty falls back to English
// (strings_test.go checks that none is left).
type Strings struct {
	Error string

	// Menus
	MenuFile             string
	MenuOpen             string
	MenuSave             string
	MenuSaveAs           string
	MenuClose            string
	MenuRecent           string
	MenuExit             string
	MenuEdit             string
	MenuUndo             string
	MenuRedo             string
	MenuCut              string
	MenuCopy             string
	MenuPaste            string
	MenuSelectAll        string
	MenuInsertMode       string
	MenuReadOnly         string
	MenuView             string
	MenuGoTo             string
	MenuZoomIn           string
	MenuZoomOut          string
	MenuZoomReset        string
	MenuNew              string
	MenuNewMore          string
	MenuNewSize          string
	MenuNewClipboard     string
	MenuCloseAll         string
	MenuNavigate         string
	MenuBack             string
	MenuForward          string
	MenuToggleBookmark   string
	MenuNextBookmark     string
	MenuPrevBookmark     string
	MenuExportBookmarks  string
	MenuImportBookmarks  string
	MenuNextTab          string
	MenuPrevTab          string
	MenuBytesPerRow      string
	MenuFitWidth         string
	MenuGroup            string
	MenuMode             string
	MenuOffsets          string
	MenuRelativeOffsets  string
	MenuSetMark          string
	MenuEncoding         string
	MenuSplit            string
	MenuSyncScroll       string
	ModeHex              string
	ModeBin              string
	ModeOct              string
	ModeDec              string
	SizeLabel            string
	MenuPanels           string
	PageInspector        string
	BigEndian            string
	ColType              string
	ColValue             string
	PageBookmarks        string
	PageHashes           string
	ColName              string
	ColOffset            string
	ColLength            string
	ColComment           string
	ColAlgorithm         string
	Remove               string
	Range                string
	RangeSelection       string
	RangeDocument        string
	CustomCRC            string
	Calculate            string
	Cancel               string
	StatsLine            func(off, n int64, entropy, seconds float64) string
	Working              func(percent int) string
	BytesLabel           string
	BytesBad             string
	BitsLabel            string
	BitsBad              string
	MenuFill             string
	OpAdd                string
	OpSub                string
	OpShl                string
	OpShr                string
	OpRol                string
	OpRor                string
	OpSwap               func(width int) string
	OpReverse            string
	CopyHex              string
	CopyHexPlain         string
	CopyEscaped          string
	ImportArray          string
	MenuCopyAs           string
	MenuBitOps           string
	MenuImport           string
	MenuExport           string
	ImportedAt           func(base uint64) string
	AddressLabel         string
	FillKind             string
	FillPattern          string
	FillRandom           string
	FillIncrement        string
	FillValue            string
	FillStart            string
	FillStep             string
	FillWidth            string
	FillInsert           string
	MenuPalette          string
	ColCommand           string
	ColShortcut          string
	PageSearch           string
	MenuSearch           string
	MenuFind             string
	MenuReplace          string
	FindHex              string
	FindText             string
	ReplaceWith          string
	IgnoreCase           string
	AllDocuments         string
	FindNext             string
	FindPrev             string
	FindAll              string
	Replace              string
	ReplaceAll           string
	Strings              string
	Export               string
	ColDocument          string
	ColFound             string
	NothingToFind        string
	NotFound             string
	Wrapped              string
	ClearMarks           string
	FoundCount           func(n int, more bool, seconds float64) string
	ReplacedCount        func(n int) string
	MenuCompareMenu      string
	MenuCompare          string
	MenuCompareShifts    string
	MenuNextDiff         string
	MenuPrevDiff         string
	MenuCompareReport    string
	MenuMakePatch        string
	MenuApplyPatch       string
	CompareSaveFirst     string
	NoMoreDiffs          string
	PatchNoFile          string
	PatchSaved           string
	PatchUnknown         string
	Comparing            func(percent int) string
	CompareSummary       func(ranges int, changed, inserted, deleted int64, seconds float64) string
	DiffStatus           func(cur, total int) string
	ColorRules           string
	ColorRulesHint       string
	PageStructure        string
	PageConsole          string
	MenuApplyTemplate    string
	Apply                string
	ColSize              string
	RunFunction          string
	AutoTemplate         string
	NoTemplate           string
	NoTemplateApplied    string
	ChecksumPrefix       string
	Evaluate             string
	Clear                string
	ConsoleHint          string
	NoDocument           string
	MoreElements         func(n int64) string
	TemplateError        func(name, err string) string
	TemplateWrites       func(name string) string
	TemplateRunning      func(name string) string
	TemplateDone         func(name string, fields int, seconds float64) string
	MenuOpenDisk         string
	PageDisk             string
	PageFiles            string
	PageBoot             string
	PageDisasm           string
	ColModel             string
	ColPath              string
	ColProblem           string
	ColModified          string
	ColCompression       string
	ColAddress           string
	ColBytes             string
	ColInstruction       string
	DiskReadOnlyNote     string
	DiskWriteTitle       string
	DiskWriteEdits       string
	DiskWriteSave        string
	OpenPartition        string
	ShowFiles            string
	BootCode             string
	Refresh              string
	NoPartitionTable     string
	ChoosePartition      string
	NoFileSystemShown    string
	GoToBytes            string
	Extract              string
	OpenAsDocument       string
	NoExtents            string
	NoBootImage          string
	LoadAddress          string
	FromCaret            string
	OpenDiskInside       func(name, format string) string
	DiskNeedsRights      func(path string) string
	DiskWriteAsk1        func(path, what string) string
	DiskWriteAsk2        func(path string) string
	PartitionInfo        func(scheme string, parts, issues int) string
	NoFileSystem         func(name string) string
	ExtentsCount         func(n int) string
	Extracted            func(n int) string
	PageFirmware         string
	PageFunctions        string
	FirmwareHint         string
	Device               string
	Architecture         string
	NoDevice             string
	Detect               string
	Analyze              string
	ExportAsm            string
	DecodeSelection      string
	NothingDetected      string
	Analyzing            string
	SelectConfig         string
	AnalyzeFirst         string
	Rename               string
	CommentTitle         string
	ColCallers           string
	ColFunction          string
	ReferencesTo         string
	AnalysisDone         func(insts, funcs int) string
	MenuExternal         string
	ExtStartFailed       func(err string) string
	ExtAskTitle          func(client string) string
	ExtUnknownClient     string
	ExtAskOpen           func(path string) string
	ExtAskSave           func(name string) string
	ExtAskEdits          func(name, why, list string) string
	ExtOverwrite         func(n int, off int64) string
	ExtInsert            func(n int, off int64) string
	ExtDelete            func(n, off int64) string
	ExtMoreEdits         func(n, total int) string
	ExtUndoKind          func(client string) string
	ExtApplied           func(client string) string
	ExtApply             string
	ExtDecline           string
	ExtAPI               string
	ExtHTTP              string
	ExtPort              string
	ExtToken             string
	ExtNewToken          string
	ExtCopyToken         string
	ExtTrust             string
	ExtHint              string
	ExtRunning           func(where string) string
	ExtBadPort           string
	ExtShortToken        string
	Copied               string
	PageHistory          string
	ColEdit              string
	ColTime              string
	HistoryGoTo          string
	HistoryOriginal      string
	ColColor             string
	BadColor             string
	Canceled             string
	OpenedReadOnly       func(name, reason string) string
	FileChangedAskEdited func(name string) string
	DiskWriteRanges      func(n int, total int64, list string, more int) string
	MenuCompareOpen      string
	CompareWith          func(name string) string
	CompareShiftsOne     string
	CompareAddFiles      string
	CompareChooseOne     string
	CompareTooMany       func(n int) string
	CompareSummaryN      func(ranges int, bytes int64, seconds float64) string
	MenuMinimap          string
	MemoryMap            string
	CallTreeOf           string
	CallTreeCallers      string
	CallTreeCallees      string
	CallCycle            string
	ExportCallGraph      string
	AddrNotInImage       string
	OpenDecompressed     string
	ExtractDecompressed  string
	FindHintHex          string
	FindHintText         string
	FindHintNumber       string
	MinLengthLabel       string
	MatchOf              func(i, n int, more bool) string
	GoToHint             string
	MenuOptions          string
	MenuSettings         string
	MenuHelp             string
	MenuDocs             string
	MenuAbout            string

	// The main window
	DropHint   string // shown when no file is open
	OpenFailed func(path, err string) string
	Untitled   string // a document not saved yet

	// The status bar
	StatusOffset   string
	StatusSelected string
	StatusValue    string
	StatusSize     string
	ModeInsert     string
	ModeOverwrite  string
	ReadOnly       string
	Saving         func(percent int) string
	Saved          string
	SaveFailed     func(err string) string

	// Closing a document with edits
	SaveChangesTitle string
	SaveChangesAsk   func(name string) string
	Save             string
	DontSave         string

	// Go to
	GoToTitle string
	GoToLabel string
	GoToBad   string

	// Recovery after a crash, a file changed by another program
	RecoverTitle   string
	RecoverAsk     func(name, when string) string
	RecoverFailed  func(err string) string
	FileChangedAsk func(name string) string

	// Settings dialog
	BackupOnSave   string
	BookmarkName   func(n int) string
	AlwaysOnTop    string
	Language       string
	LanguageSystem string
	Theme          string
	ThemeDark      string
	ThemeLight     string

	// About dialog
	AboutTitle   func(name string) string
	Version      string
	Author       string
	License      string
	VisitWebsite string
	Close        string

	// Installing a downloaded copy to ~/.altbins (Windows) and removing it
	Install          string
	Update           string // an older version is installed
	Uninstall        string
	InstallAsk       func(dir string) string
	InstallFailed    func(err string) string
	Installed        string // a toast in the installed copy
	UninstallAsk     func(dir string) string
	UninstallRunning string
	Uninstalled      string
}

// languages are offered in the settings; the names stay in their own language
var languages = []struct{ tag, name string }{
	{"en", "English"},
	{"ru", "Русский"},
	{"pl", "Polski"},
	{"sr", "Српски"},
	{"de", "Deutsch"},
	{"fr", "Français"},
	{"es", "Español"},
	{"it", "Italiano"},
	{"pt", "Português"},
	{"zh", "中文"},
	{"ja", "日本語"},
	{"ko", "한국어"},
}

var en = Strings{
	Error: "Error",

	MenuFile:            "File",
	MenuOpen:            "Open...",
	MenuSave:            "Save",
	MenuSaveAs:          "Save as...",
	MenuClose:           "Close",
	MenuRecent:          "Recent files",
	MenuExit:            "Exit",
	MenuEdit:            "Edit",
	MenuUndo:            "Undo",
	MenuRedo:            "Redo",
	MenuCut:             "Cut",
	MenuCopy:            "Copy",
	MenuPaste:           "Paste",
	MenuSelectAll:       "Select all",
	MenuInsertMode:      "Insert mode (Ins)",
	MenuReadOnly:        "Read-only",
	MenuView:            "View",
	MenuGoTo:            "Go to offset...",
	MenuZoomIn:          "Zoom in",
	MenuZoomOut:         "Zoom out",
	MenuZoomReset:       "Normal size",
	MenuNew:             "New",
	MenuNewMore:         "New from",
	MenuNewSize:         "Zeros of a size...",
	MenuNewClipboard:    "Clipboard",
	MenuCloseAll:        "Close all",
	MenuNavigate:        "Navigate",
	MenuBack:            "Back",
	MenuForward:         "Forward",
	MenuToggleBookmark:  "Toggle bookmark",
	MenuNextBookmark:    "Next bookmark",
	MenuPrevBookmark:    "Previous bookmark",
	MenuExportBookmarks: "Export bookmarks...",
	MenuImportBookmarks: "Import bookmarks...",
	MenuNextTab:         "Next tab",
	MenuPrevTab:         "Previous tab",
	MenuBytesPerRow:     "Bytes per row",
	MenuFitWidth:        "Fit the window",
	MenuGroup:           "Group bytes",
	MenuMode:            "Bytes as",
	MenuOffsets:         "Offsets",
	MenuRelativeOffsets: "From the mark",
	MenuSetMark:         "Set the mark here",
	MenuEncoding:        "Text encoding",
	MenuSplit:           "Split view",
	MenuSyncScroll:      "Scroll split views together",
	ModeHex:             "Hexadecimal",
	ModeBin:             "Binary",
	ModeOct:             "Octal",
	ModeDec:             "Decimal",
	SizeLabel:           "Size:",
	MenuPanels:          "Panels",
	PageInspector:       "Data inspector",
	BigEndian:           "Big-endian",
	ColType:             "Type",
	ColValue:            "Value",
	PageBookmarks:       "Bookmarks",
	PageHashes:          "Hashes",
	ColName:             "Name",
	ColOffset:           "Offset",
	ColLength:           "Length",
	ColComment:          "Comment",
	ColAlgorithm:        "Algorithm",
	Remove:              "Remove",
	Range:               "Range:",
	RangeSelection:      "Selection",
	RangeDocument:       "Whole document",
	CustomCRC:           "Custom CRC:",
	Calculate:           "Calculate",
	Cancel:              "Cancel",
	StatsLine: func(off, n int64, entropy, seconds float64) string {
		return fmt.Sprintf("%X..%X (%d bytes): entropy %.4f bits/byte, %.2f s", off, off+n, n, entropy, seconds)
	},
	Working:       func(percent int) string { return fmt.Sprintf("Working... %d%%", percent) },
	BytesLabel:    "Bytes (hex, or \"text\"):",
	BytesBad:      "Hex digits like FF 00 or a text in quotes",
	BitsLabel:     "Bits (0-7):",
	BitsBad:       "A number of bits from 0 to 7",
	MenuFill:      "Fill...",
	OpAdd:         "Add...",
	OpSub:         "Subtract...",
	OpShl:         "Shift left...",
	OpShr:         "Shift right...",
	OpRol:         "Rotate left...",
	OpRor:         "Rotate right...",
	OpSwap:        func(width int) string { return fmt.Sprintf("Swap bytes in groups of %d", width) },
	OpReverse:     "Reverse the order of bytes",
	CopyHex:       "Hex with spaces",
	CopyHexPlain:  "Hex without spaces",
	CopyEscaped:   "Escaped string",
	ImportArray:   "Array of code...",
	MenuCopyAs:    "Copy as",
	MenuBitOps:    "Bitwise operations",
	MenuImport:    "Import",
	MenuExport:    "Export selection",
	ImportedAt:    func(base uint64) string { return fmt.Sprintf("The data starts at address %X", base) },
	AddressLabel:  "Address of the first byte:",
	FillKind:      "Fill with:",
	FillPattern:   "Bytes repeated",
	FillRandom:    "Random bytes",
	FillIncrement: "Increasing numbers",
	FillValue:     "Bytes:",
	FillStart:     "First number:",
	FillStep:      "Step:",
	FillWidth:     "Size of a number:",
	FillInsert:    "Insert instead of overwriting",
	MenuPalette:   "Command palette...",
	ColCommand:    "Command",
	ColShortcut:   "Keys",
	PageSearch:    "Search",
	MenuSearch:    "Search",
	MenuFind:      "Find...",
	MenuReplace:   "Replace...",
	FindHex:       "Hex",
	FindText:      "Text",
	ReplaceWith:   "Replace with:",
	IgnoreCase:    "Ignore case",
	AllDocuments:  "All documents",
	FindNext:      "Find next",
	FindPrev:      "Find previous",
	FindAll:       "Find all",
	Replace:       "Replace",
	ReplaceAll:    "Replace all",
	Strings:       "Strings",
	Export:        "Export...",
	ColDocument:   "Document",
	ColFound:      "Found",
	NothingToFind: "Nothing to find",
	NotFound:      "Not found",
	Wrapped:       "Searched from the other end",
	ClearMarks:    "Clear the marks of the search",
	FoundCount: func(n int, more bool, seconds float64) string {
		s := fmt.Sprintf("Found: %d in %.2f s", n, seconds)
		if more {
			s += " (the first ones)"
		}
		return s
	},
	ReplacedCount:     func(n int) string { return fmt.Sprintf("Replaced: %d", n) },
	MenuCompareMenu:   "Compare",
	MenuCompare:       "Compare with a file...",
	MenuCompareShifts: "Compare with shifts...",
	MenuNextDiff:      "Next difference",
	MenuPrevDiff:      "Previous difference",
	MenuCompareReport: "Export the report...",
	MenuMakePatch:     "Save changes as a patch",
	MenuApplyPatch:    "Apply a patch...",
	CompareSaveFirst:  "Save the document before comparing it",
	NoMoreDiffs:       "No more differences",
	PatchNoFile:       "The document has no file to make a patch from",
	PatchSaved:        "The patch is saved",
	PatchUnknown:      "Not a patch of a known format",
	Comparing:         func(percent int) string { return fmt.Sprintf("Comparing... %d%%", percent) },
	CompareSummary: func(ranges int, changed, inserted, deleted int64, seconds float64) string {
		return fmt.Sprintf("Differences: %d (changed %d, inserted %d, deleted %d bytes), %.2f s", ranges, changed, inserted, deleted, seconds)
	},
	DiffStatus:        func(cur, total int) string { return fmt.Sprintf("Difference %d of %d", cur, total) },
	ColorRules:        "Byte colors:",
	ColorRulesHint:    "e.g. 00-1F=#ff8800; 80-FF=#4488ff",
	PageStructure:     "Structure",
	PageConsole:       "Console",
	MenuApplyTemplate: "Apply the template",
	Apply:             "Apply",
	ColSize:           "Size",
	RunFunction:       "Run",
	AutoTemplate:      "Auto (by the format)",
	NoTemplate:        "No template fits this file",
	NoTemplateApplied: "No template applied (F5 to apply)",
	ChecksumPrefix:    "Checksum: ",
	Evaluate:          "Run",
	Clear:             "Clear",
	ConsoleHint:       "Expressions over the document: ReadUInt(0x3C), Checksum(\"crc32\", 0, FileSize()), fields of the template by name",
	NoDocument:        "No document is open",
	MoreElements:      func(n int64) string { return fmt.Sprintf("... %d more", n) },
	TemplateError:     func(name, err string) string { return "The template " + name + " has an error:\n\n" + err },
	TemplateWrites: func(name string) string {
		return "The template " + name + " writes into the document. Run it?\n\nIts changes can be undone at once."
	},
	TemplateRunning: func(name string) string { return name + ": running..." },
	TemplateDone: func(name string, fields int, seconds float64) string {
		return fmt.Sprintf("%s: %d fields, %.2f s", name, fields, seconds)
	},
	MenuOpenDisk:      "Open a disk...",
	PageDisk:          "Disk",
	PageFiles:         "Files",
	PageBoot:          "Boot image",
	PageDisasm:        "Disassembly",
	ColModel:          "Model",
	ColPath:           "Path",
	ColProblem:        "Problem",
	ColModified:       "Modified",
	ColCompression:    "Compression",
	ColAddress:        "Address",
	ColBytes:          "Bytes",
	ColInstruction:    "Instruction",
	DiskReadOnlyNote:  "Disks open read-only; writing to them is asked about twice",
	DiskWriteTitle:    "Writing to a disk",
	DiskWriteEdits:    "allow editing",
	DiskWriteSave:     "write the changed sectors",
	OpenPartition:     "Open",
	ShowFiles:         "Files",
	BootCode:          "Boot code",
	Refresh:           "Refresh",
	NoPartitionTable:  "No partition table",
	ChoosePartition:   "Choose a partition",
	NoFileSystemShown: "Choose a partition on the Disk page and press Files",
	GoToBytes:         "Go to the bytes",
	Extract:           "Extract...",
	OpenAsDocument:    "Open",
	NoExtents:         "The bytes of the file are not stored as runs",
	NoBootImage:       "Not a boot image of a known format",
	LoadAddress:       "at",
	FromCaret:         "From the caret",
	OpenDiskInside: func(name, format string) string {
		return name + " is a disk image (" + format + "). Open the disk inside it as well?"
	},
	DiskNeedsRights: func(path string) string {
		return "No access to " + path + ". Reading disks needs administrator rights: run AltHex as an administrator (on Linux: as root, or add yourself to the disk group)."
	},
	DiskWriteAsk1: func(path, what string) string {
		return "You are about to " + what + " on the disk " + path + ". A mistake can destroy the data of the whole disk. Continue?"
	},
	DiskWriteAsk2: func(path string) string { return "Are you sure? The changes go to " + path + " directly." },
	PartitionInfo: func(scheme string, parts, issues int) string {
		return fmt.Sprintf("%s: %d partitions, %d problems", scheme, parts, issues)
	},
	NoFileSystem:    func(name string) string { return "No known file system in " + name },
	ExtentsCount:    func(n int) string { return fmt.Sprintf("The file is in %d runs", n) },
	Extracted:       func(n int) string { return fmt.Sprintf("Extracted: %d", n) },
	PageFirmware:    "Firmware",
	PageFunctions:   "Functions",
	FirmwareHint:    "Choose the device, or press Detect; then Analyze",
	Device:          "Device:",
	Architecture:    "Architecture:",
	NoDevice:        "(no device)",
	Detect:          "Detect",
	Analyze:         "Analyze",
	ExportAsm:       "Export .s...",
	DecodeSelection: "Decode the selection",
	NothingDetected: "the architecture is not recognized",
	Analyzing:       "Analyzing...",
	SelectConfig:    "Select the bytes of the configuration area",
	AnalyzeFirst:    "Analyze the firmware first",
	Rename:          "Rename",
	CommentTitle:    "Comment",
	ColCallers:      "Callers",
	ColFunction:     "Function",
	ReferencesTo:    "References to the line chosen in the listing:",
	AnalysisDone: func(insts, funcs int) string {
		return fmt.Sprintf("Instructions: %d, functions: %d. In the listing: N - rename, ; - comment, C - code here, Enter - go to", insts, funcs)
	},
	MenuExternal:     "External interfaces...",
	ExtStartFailed:   func(err string) string { return "The external interface did not start: " + err },
	ExtAskTitle:      func(client string) string { return "Request of " + client },
	ExtUnknownClient: "a program",
	ExtAskOpen:       func(path string) string { return "Open the file\n" + path + "?" },
	ExtAskSave:       func(name string) string { return "Save " + name + " to its file?" },
	ExtAskEdits: func(name, why, list string) string {
		s := "Change " + name + ":\n\n" + list
		if why != "" {
			s += "\nWhy: " + why + "\n"
		}
		return s + "\nThe edits will be one undo step and are not saved to the file."
	},
	ExtOverwrite:    func(n int, off int64) string { return fmt.Sprintf("write %d bytes at 0x%X", n, off) },
	ExtInsert:       func(n int, off int64) string { return fmt.Sprintf("insert %d bytes at 0x%X", n, off) },
	ExtDelete:       func(n, off int64) string { return fmt.Sprintf("delete %d bytes at 0x%X", n, off) },
	ExtMoreEdits:    func(n, total int) string { return fmt.Sprintf("... and %d edits more (%d bytes in all)", n, total) },
	ExtUndoKind:     func(client string) string { return "edits of " + client },
	ExtApplied:      func(client string) string { return "Edits of " + client + " applied" },
	ExtApply:        "Apply",
	ExtDecline:      "Decline",
	ExtAPI:          "Local API (JSON-RPC) for programs and \"althex mcp\"",
	ExtHTTP:         "MCP over HTTP for AI agents (127.0.0.1 only)",
	ExtPort:         "Port",
	ExtToken:        "Token",
	ExtNewToken:     "New",
	ExtCopyToken:    "Copy",
	ExtTrust:        "Apply the edits of clients without asking (not recommended)",
	ExtHint:         "Only local connections are taken. An AI agent can also start \"althex mcp\" (stdio):\nit works with the documents of this window when the local API is on.\nOver HTTP the agent sends \"Authorization: Bearer <token>\" to http://127.0.0.1:<port>/mcp.",
	ExtRunning:      func(where string) string { return "Running: " + where },
	ExtBadPort:      "The port is a number from 1 to 65535",
	ExtShortToken:   "The token must be at least 16 characters",
	Copied:          "Copied",
	PageHistory:     "History",
	ColEdit:         "Edit",
	ColTime:         "Time",
	HistoryGoTo:     "Go to this state",
	HistoryOriginal: "As opened",
	ColColor:        "Color",
	BadColor:        "A color is written as #rrggbb",
	Canceled:        "Canceled",
	OpenedReadOnly: func(name, reason string) string {
		return "Cannot write " + name + " (" + reason + "). It is opened read-only: save it under another name to keep the edits."
	},
	FileChangedAskEdited: func(name string) string {
		return name + " was changed by another program.\n\nThe document has edits that are not saved: reloading it loses them. Reload it? No keeps the document with your edits."
	},
	DiskWriteRanges: func(n int, total int64, list string, more int) string {
		s := fmt.Sprintf("\n\nTo be written: %d ranges, %d bytes: %s", n, total, list)
		if more > 0 {
			s += fmt.Sprintf(" and %d more", more)
		}
		return s
	},
	MenuCompareOpen:  "Compare with open documents...",
	CompareWith:      func(name string) string { return "Compare " + name + " with:" },
	CompareShiftsOne: "With shifts (one document only)",
	CompareAddFiles:  "Add files...",
	CompareChooseOne: "Choose at least one document",
	CompareTooMany:   func(n int) string { return fmt.Sprintf("At most %d documents are compared at once", n) },
	CompareSummaryN: func(ranges int, bytes int64, seconds float64) string {
		return fmt.Sprintf("Differences: %d ranges, %d bytes, %.2f s", ranges, bytes, seconds)
	},
	MenuMinimap:         "Minimap",
	MemoryMap:           "Memory map of the device:",
	CallTreeOf:          "Call tree of the function chosen:",
	CallTreeCallers:     "Callers",
	CallTreeCallees:     "Callees",
	CallCycle:           "(recursion)",
	ExportCallGraph:     "Export call graph",
	AddrNotInImage:      "The address is not in the image",
	OpenDecompressed:    "Open decompressed",
	ExtractDecompressed: "Extract decompressed",
	FindHintHex:         "Bytes in hex: 4D 5A ?? 50",
	FindHintText:        "Text to find",
	FindHintNumber:      "A number: 1234, -5, 0x1F, 1.5",
	MinLengthLabel:      "min.",
	MatchOf: func(i, n int, more bool) string {
		s := fmt.Sprintf("Match %d of %d", i, n)
		if more {
			s += "+"
		}
		return s
	},
	GoToHint:     "0x1F, 1F, 1Fh, 31 (decimal), +16, 0x400 + 3*512",
	MenuOptions:  "Options",
	MenuSettings: "Settings...",
	MenuHelp:     "Help",
	MenuDocs:     "Documentation",
	MenuAbout:    "About",

	DropHint:   "Drop a file here or open it with Ctrl+O",
	OpenFailed: func(path, err string) string { return "Cannot open " + path + ":\n\n" + err },
	Untitled:   "Untitled",

	StatusOffset:   "Offset",
	StatusSelected: "Selected",
	StatusValue:    "Value",
	StatusSize:     "Size",
	ModeInsert:     "INS",
	ModeOverwrite:  "OVR",
	ReadOnly:       "Read-only",
	Saving:         func(percent int) string { return fmt.Sprintf("Saving... %d%%", percent) },
	Saved:          "Saved",
	SaveFailed:     func(err string) string { return "Cannot save the file:\n\n" + err },

	SaveChangesTitle: "Save changes",
	SaveChangesAsk:   func(name string) string { return "Save the changes to " + name + "?" },
	Save:             "Save",
	DontSave:         "Don't save",

	GoToTitle: "Go to offset",
	GoToLabel: "Offset:",
	GoToBad:   "Past the end of the document",

	RecoverTitle: "Recover edits",
	RecoverAsk: func(name, when string) string {
		return "AltHex was closed with unsaved edits of " + name + " (" + when + ").\n\nRecover them?"
	},
	RecoverFailed: func(err string) string { return "Cannot recover the edits:\n\n" + err },
	FileChangedAsk: func(name string) string {
		return name + " was changed by another program.\n\nReload it? The edits made here will be lost."
	},

	BackupOnSave:   "Keep a backup copy (.bak) when saving",
	BookmarkName:   func(n int) string { return fmt.Sprintf("Bookmark %d", n) },
	AlwaysOnTop:    "Always on top",
	Language:       "Language:",
	LanguageSystem: "As in the system",
	Theme:          "Theme:",
	ThemeDark:      "Dark",
	ThemeLight:     "Light",

	AboutTitle:   func(name string) string { return "About " + name },
	Version:      "Version",
	Author:       "Author:",
	License:      "License:",
	VisitWebsite: "Visit website",
	Close:        "Close",

	Install:   "Install",
	Update:    "Update",
	Uninstall: "Uninstall",
	InstallAsk: func(dir string) string {
		return "AltHex will be copied to " + dir + " and added to the Start menu, the desktop and the list of installed apps. An installed copy is updated. The settings stay as they are.\n\nThen AltHex starts again from there."
	},
	InstallFailed: func(err string) string { return "Cannot install AltHex:\n\n" + err },
	Installed:     "AltHex is installed",
	UninstallAsk: func(dir string) string {
		return "Remove AltHex from this computer?\n\nThe settings stay in " + dir + "."
	},
	UninstallRunning: "AltHex is running. Close it and try again.",
	Uninstalled:      "AltHex has been removed.",
}

var ru = Strings{
	Error: "Ошибка",

	MenuFile:            "Файл",
	MenuOpen:            "Открыть...",
	MenuSave:            "Сохранить",
	MenuSaveAs:          "Сохранить как...",
	MenuClose:           "Закрыть",
	MenuRecent:          "Недавние файлы",
	MenuExit:            "Выход",
	MenuEdit:            "Правка",
	MenuUndo:            "Отменить",
	MenuRedo:            "Повторить",
	MenuCut:             "Вырезать",
	MenuCopy:            "Копировать",
	MenuPaste:           "Вставить",
	MenuSelectAll:       "Выделить всё",
	MenuInsertMode:      "Режим вставки (Ins)",
	MenuReadOnly:        "Только чтение",
	MenuView:            "Вид",
	MenuGoTo:            "Перейти к смещению...",
	MenuZoomIn:          "Увеличить",
	MenuZoomOut:         "Уменьшить",
	MenuZoomReset:       "Обычный размер",
	MenuNew:             "Новый",
	MenuNewMore:         "Новый из",
	MenuNewSize:         "Нулей заданного размера...",
	MenuNewClipboard:    "Буфера обмена",
	MenuCloseAll:        "Закрыть все",
	MenuNavigate:        "Переход",
	MenuBack:            "Назад",
	MenuForward:         "Вперёд",
	MenuToggleBookmark:  "Поставить или снять закладку",
	MenuNextBookmark:    "Следующая закладка",
	MenuPrevBookmark:    "Предыдущая закладка",
	MenuExportBookmarks: "Экспорт закладок...",
	MenuImportBookmarks: "Импорт закладок...",
	MenuNextTab:         "Следующая вкладка",
	MenuPrevTab:         "Предыдущая вкладка",
	MenuBytesPerRow:     "Байтов в строке",
	MenuFitWidth:        "По ширине окна",
	MenuGroup:           "Группировка байтов",
	MenuMode:            "Байты как",
	MenuOffsets:         "Смещения",
	MenuRelativeOffsets: "От метки",
	MenuSetMark:         "Поставить метку здесь",
	MenuEncoding:        "Кодировка текста",
	MenuSplit:           "Разделить вид",
	MenuSyncScroll:      "Синхронная прокрутка",
	ModeHex:             "Шестнадцатеричные",
	ModeBin:             "Двоичные",
	ModeOct:             "Восьмеричные",
	ModeDec:             "Десятичные",
	SizeLabel:           "Размер:",
	MenuPanels:          "Панели",
	PageInspector:       "Интерпретатор",
	BigEndian:           "Big-endian (старший байт первым)",
	ColType:             "Тип",
	ColValue:            "Значение",
	PageBookmarks:       "Закладки",
	PageHashes:          "Хеши",
	ColName:             "Имя",
	ColOffset:           "Смещение",
	ColLength:           "Длина",
	ColComment:          "Комментарий",
	ColAlgorithm:        "Алгоритм",
	Remove:              "Удалить",
	Range:               "Диапазон:",
	RangeSelection:      "Выделение",
	RangeDocument:       "Весь документ",
	CustomCRC:           "Свой CRC:",
	Calculate:           "Вычислить",
	Cancel:              "Отмена",
	StatsLine: func(off, n int64, entropy, seconds float64) string {
		return fmt.Sprintf("%X..%X (%d байт): энтропия %.4f бит/байт, %.2f с", off, off+n, n, entropy, seconds)
	},
	Working:    func(percent int) string { return fmt.Sprintf("Выполняется... %d%%", percent) },
	BytesLabel: "Байты (hex или \"текст\"):",
	BytesBad:   "Hex-цифры вида FF 00 или текст в кавычках",
	BitsLabel:  "Бит (0-7):",
	BitsBad:    "Число бит от 0 до 7",
	MenuFill:   "Заполнить...",
	OpAdd:      "Прибавить...",
	OpSub:      "Вычесть...",
	OpShl:      "Сдвиг влево...",
	OpShr:      "Сдвиг вправо...",
	OpRol:      "Циклический сдвиг влево...",
	OpRor:      "Циклический сдвиг вправо...",
	OpSwap: func(width int) string {
		return fmt.Sprintf("Переставить байты в группах по %d", width)
	},
	OpReverse:    "Обратный порядок байтов",
	CopyHex:      "Hex с пробелами",
	CopyHexPlain: "Hex без пробелов",
	CopyEscaped:  "Строка с экранированием",
	ImportArray:  "Массив из кода...",
	MenuCopyAs:   "Копировать как",
	MenuBitOps:   "Побитовые операции",
	MenuImport:   "Импорт",
	MenuExport:   "Экспорт выделения",
	ImportedAt: func(base uint64) string {
		return fmt.Sprintf("Данные начинаются с адреса %X", base)
	},
	AddressLabel:  "Адрес первого байта:",
	FillKind:      "Заполнить:",
	FillPattern:   "Повтором байтов",
	FillRandom:    "Случайными байтами",
	FillIncrement: "Возрастающими числами",
	FillValue:     "Байты:",
	FillStart:     "Первое число:",
	FillStep:      "Шаг:",
	FillWidth:     "Размер числа:",
	FillInsert:    "Вставить, а не заменить",
	MenuPalette:   "Палитра команд...",
	ColCommand:    "Команда",
	ColShortcut:   "Клавиши",
	PageSearch:    "Поиск",
	MenuSearch:    "Поиск",
	MenuFind:      "Найти...",
	MenuReplace:   "Заменить...",
	FindHex:       "Hex",
	FindText:      "Текст",
	ReplaceWith:   "Заменить на:",
	IgnoreCase:    "Без учёта регистра",
	AllDocuments:  "Во всех документах",
	FindNext:      "Найти далее",
	FindPrev:      "Найти назад",
	FindAll:       "Найти все",
	Replace:       "Заменить",
	ReplaceAll:    "Заменить все",
	Strings:       "Строки",
	Export:        "Экспорт...",
	ColDocument:   "Документ",
	ColFound:      "Найдено",
	NothingToFind: "Нечего искать",
	NotFound:      "Не найдено",
	Wrapped:       "Поиск продолжен с другого конца",
	ClearMarks:    "Убрать отметки поиска",
	FoundCount: func(n int, more bool, seconds float64) string {
		s := fmt.Sprintf("Найдено: %d за %.2f с", n, seconds)
		if more {
			s += " (только первые)"
		}
		return s
	},
	ReplacedCount:     func(n int) string { return fmt.Sprintf("Заменено: %d", n) },
	MenuCompareMenu:   "Сравнение",
	MenuCompare:       "Сравнить с файлом...",
	MenuCompareShifts: "Сравнить со сдвигами...",
	MenuNextDiff:      "Следующее отличие",
	MenuPrevDiff:      "Предыдущее отличие",
	MenuCompareReport: "Экспорт отчёта...",
	MenuMakePatch:     "Сохранить изменения как патч",
	MenuApplyPatch:    "Применить патч...",
	CompareSaveFirst:  "Сохраните документ перед сравнением",
	NoMoreDiffs:       "Больше отличий нет",
	PatchNoFile:       "У документа нет файла, от которого строить патч",
	PatchSaved:        "Патч сохранён",
	PatchUnknown:      "Неизвестный формат патча",
	Comparing:         func(percent int) string { return fmt.Sprintf("Сравнение... %d%%", percent) },
	CompareSummary: func(ranges int, changed, inserted, deleted int64, seconds float64) string {
		return fmt.Sprintf("Отличий: %d (изменено %d, вставлено %d, удалено %d байт), %.2f с", ranges, changed, inserted, deleted, seconds)
	},
	DiffStatus:        func(cur, total int) string { return fmt.Sprintf("Отличие %d из %d", cur, total) },
	ColorRules:        "Цвета байтов:",
	ColorRulesHint:    "например 00-1F=#ff8800; 80-FF=#4488ff",
	PageStructure:     "Структура",
	PageConsole:       "Консоль",
	MenuApplyTemplate: "Применить шаблон",
	Apply:             "Применить",
	ColSize:           "Размер",
	RunFunction:       "Выполнить",
	AutoTemplate:      "Авто (по формату)",
	NoTemplate:        "Нет шаблона для этого файла",
	NoTemplateApplied: "Шаблон не применён (F5 — применить)",
	ChecksumPrefix:    "Контрольная сумма: ",
	Evaluate:          "Выполнить",
	Clear:             "Очистить",
	ConsoleHint:       "Выражения над документом: ReadUInt(0x3C), Checksum(\"crc32\", 0, FileSize()), поля шаблона по имени",
	NoDocument:        "Нет открытого документа",
	MoreElements:      func(n int64) string { return fmt.Sprintf("... ещё %d", n) },
	TemplateError:     func(name, err string) string { return "Ошибка в шаблоне " + name + ":\n\n" + err },
	TemplateWrites: func(name string) string {
		return "Шаблон " + name + " изменяет документ. Выполнить его?\n\nЕго изменения отменяются одним шагом."
	},
	TemplateRunning: func(name string) string { return name + ": выполняется..." },
	TemplateDone: func(name string, fields int, seconds float64) string {
		return fmt.Sprintf("%s: полей %d, %.2f с", name, fields, seconds)
	},
	MenuOpenDisk:      "Открыть диск...",
	PageDisk:          "Диск",
	PageFiles:         "Файлы",
	PageBoot:          "Загрузочный образ",
	PageDisasm:        "Дизассемблер",
	ColModel:          "Модель",
	ColPath:           "Путь",
	ColProblem:        "Проблема",
	ColModified:       "Изменён",
	ColCompression:    "Сжатие",
	ColAddress:        "Адрес",
	ColBytes:          "Байты",
	ColInstruction:    "Инструкция",
	DiskReadOnlyNote:  "Диски открываются только для чтения; запись на них подтверждается дважды",
	DiskWriteTitle:    "Запись на диск",
	DiskWriteEdits:    "разрешить правку",
	DiskWriteSave:     "записать изменённые сектора",
	OpenPartition:     "Открыть",
	ShowFiles:         "Файлы",
	BootCode:          "Загрузочный код",
	Refresh:           "Обновить",
	NoPartitionTable:  "Таблицы разделов нет",
	ChoosePartition:   "Выберите раздел",
	NoFileSystemShown: "Выберите раздел на странице «Диск» и нажмите «Файлы»",
	GoToBytes:         "К байтам",
	Extract:           "Извлечь...",
	OpenAsDocument:    "Открыть",
	NoExtents:         "Байты файла хранятся не отрезками",
	NoBootImage:       "Не загрузочный образ известного формата",
	LoadAddress:       "адрес",
	FromCaret:         "От каретки",
	OpenDiskInside: func(name, format string) string {
		return name + " — образ диска (" + format + "). Открыть также диск внутри него?"
	},
	DiskNeedsRights: func(path string) string {
		return "Нет доступа к " + path + ". Для чтения дисков нужны права администратора: запустите AltHex от имени администратора (в Linux — от root или добавьте себя в группу disk)."
	},
	DiskWriteAsk1: func(path, what string) string {
		return "Сейчас будет: " + what + " — на диске " + path + ". Ошибка может уничтожить данные всего диска. Продолжить?"
	},
	DiskWriteAsk2: func(path string) string {
		return "Вы уверены? Изменения пойдут прямо на " + path + "."
	},
	PartitionInfo: func(scheme string, parts, issues int) string {
		return fmt.Sprintf("%s: разделов %d, проблем %d", scheme, parts, issues)
	},
	NoFileSystem: func(name string) string {
		return "В " + name + " нет известной файловой системы"
	},
	ExtentsCount:    func(n int) string { return fmt.Sprintf("Файл лежит в %d отрезках", n) },
	Extracted:       func(n int) string { return fmt.Sprintf("Извлечено: %d", n) },
	PageFirmware:    "Прошивка",
	PageFunctions:   "Функции",
	FirmwareHint:    "Выберите устройство или нажмите «Определить»; затем «Анализ»",
	Device:          "Устройство:",
	Architecture:    "Архитектура:",
	NoDevice:        "(без устройства)",
	Detect:          "Определить",
	Analyze:         "Анализ",
	ExportAsm:       "Экспорт .s...",
	DecodeSelection: "Расшифровать выделение",
	NothingDetected: "архитектура не распознана",
	Analyzing:       "Анализ...",
	SelectConfig:    "Выделите байты конфигурационной области",
	AnalyzeFirst:    "Сначала выполните анализ прошивки",
	Rename:          "Переименовать",
	CommentTitle:    "Комментарий",
	ColCallers:      "Вызывающих",
	ColFunction:     "Функция",
	ReferencesTo:    "Ссылки на строку, выбранную в листинге:",
	AnalysisDone: func(insts, funcs int) string {
		return fmt.Sprintf("Инструкций: %d, функций: %d. В листинге: N — переименовать, ; — комментарий, C — здесь код, Enter — перейти", insts, funcs)
	},
	MenuExternal:     "Внешние интерфейсы...",
	ExtStartFailed:   func(err string) string { return "Внешний интерфейс не запустился: " + err },
	ExtAskTitle:      func(client string) string { return "Запрос " + client },
	ExtUnknownClient: "программы",
	ExtAskOpen:       func(path string) string { return "Открыть файл\n" + path + "?" },
	ExtAskSave:       func(name string) string { return "Сохранить " + name + " в файл?" },
	ExtAskEdits: func(name, why, list string) string {
		s := "Изменить " + name + ":\n\n" + list
		if why != "" {
			s += "\nЗачем: " + why + "\n"
		}
		return s + "\nПравки станут одним шагом отмены и не сохраняются в файл."
	},
	ExtOverwrite: func(n int, off int64) string { return fmt.Sprintf("записать %d байт по 0x%X", n, off) },
	ExtInsert:    func(n int, off int64) string { return fmt.Sprintf("вставить %d байт по 0x%X", n, off) },
	ExtDelete:    func(n, off int64) string { return fmt.Sprintf("удалить %d байт по 0x%X", n, off) },
	ExtMoreEdits: func(n, total int) string {
		return fmt.Sprintf("... и ещё %d правок (всего %d байт)", n, total)
	},
	ExtUndoKind:     func(client string) string { return "правки " + client },
	ExtApplied:      func(client string) string { return "Правки " + client + " применены" },
	ExtApply:        "Применить",
	ExtDecline:      "Отклонить",
	ExtAPI:          "Локальный API (JSON-RPC) для программ и \"althex mcp\"",
	ExtHTTP:         "MCP по HTTP для ИИ-агентов (только 127.0.0.1)",
	ExtPort:         "Порт",
	ExtToken:        "Токен",
	ExtNewToken:     "Новый",
	ExtCopyToken:    "Копировать",
	ExtTrust:        "Применять правки клиентов без вопроса (не рекомендуется)",
	ExtHint:         "Принимаются только локальные подключения. ИИ-агент может запустить и \"althex mcp\" (stdio):\nпри включённом локальном API он работает с документами этого окна.\nПо HTTP агент передаёт \"Authorization: Bearer <токен>\" на http://127.0.0.1:<порт>/mcp.",
	ExtRunning:      func(where string) string { return "Работает: " + where },
	ExtBadPort:      "Порт — число от 1 до 65535",
	ExtShortToken:   "Токен должен быть не короче 16 символов",
	Copied:          "Скопировано",
	PageHistory:     "История",
	ColEdit:         "Правка",
	ColTime:         "Время",
	HistoryGoTo:     "Перейти к этому состоянию",
	HistoryOriginal: "Как при открытии",
	ColColor:        "Цвет",
	BadColor:        "Цвет записывается как #rrggbb",
	Canceled:        "Отменено",
	OpenedReadOnly: func(name, reason string) string {
		return "Нельзя записать " + name + " (" + reason + "). Файл открыт только для чтения: чтобы сохранить правки, сохраните его под другим именем."
	},
	FileChangedAskEdited: func(name string) string {
		return name + " изменён другой программой.\n\nВ документе есть несохранённые правки: при перезагрузке они пропадут. Перезагрузить? «Нет» оставит документ с вашими правками."
	},
	DiskWriteRanges: func(n int, total int64, list string, more int) string {
		s := fmt.Sprintf("\n\nБудет записано: диапазонов %d, байт %d: %s", n, total, list)
		if more > 0 {
			s += fmt.Sprintf(" и ещё %d", more)
		}
		return s
	},
	MenuCompareOpen:  "Сравнить с открытыми документами...",
	CompareWith:      func(name string) string { return "Сравнить " + name + " с:" },
	CompareShiftsOne: "Со сдвигами (только один документ)",
	CompareAddFiles:  "Добавить файлы...",
	CompareChooseOne: "Выберите хотя бы один документ",
	CompareTooMany: func(n int) string {
		return fmt.Sprintf("Одновременно сравнивается не более %d документов", n)
	},
	CompareSummaryN: func(ranges int, bytes int64, seconds float64) string {
		return fmt.Sprintf("Отличия: диапазонов %d, байт %d, %.2f с", ranges, bytes, seconds)
	},
	MenuMinimap:         "Миникарта",
	MemoryMap:           "Карта памяти устройства:",
	CallTreeOf:          "Дерево вызовов выбранной функции:",
	CallTreeCallers:     "Кто вызывает",
	CallTreeCallees:     "Что вызывает",
	CallCycle:           "(рекурсия)",
	ExportCallGraph:     "Экспорт графа вызовов",
	AddrNotInImage:      "Адреса нет в образе",
	OpenDecompressed:    "Открыть распакованным",
	ExtractDecompressed: "Извлечь распакованным",
	FindHintHex:         "Байты в hex: 4D 5A ?? 50",
	FindHintText:        "Искомый текст",
	FindHintNumber:      "Число: 1234, -5, 0x1F, 1.5",
	MinLengthLabel:      "мин.",
	MatchOf: func(i, n int, more bool) string {
		s := fmt.Sprintf("Совпадение %d из %d", i, n)
		if more {
			s += "+"
		}
		return s
	},
	GoToHint:     "0x1F, 1F, 1Fh, 31 (десятичное), +16, 0x400 + 3*512",
	MenuOptions:  "Параметры",
	MenuSettings: "Настройки...",
	MenuHelp:     "Справка",
	MenuDocs:     "Документация",
	MenuAbout:    "О программе",

	DropHint:   "Перетащите файл сюда или откройте его по Ctrl+O",
	OpenFailed: func(path, err string) string { return "Не удаётся открыть " + path + ":\n\n" + err },
	Untitled:   "Без имени",

	StatusOffset:   "Смещение",
	StatusSelected: "Выделено",
	StatusValue:    "Значение",
	StatusSize:     "Размер",
	ModeInsert:     "ВСТ",
	ModeOverwrite:  "ЗАМ",
	ReadOnly:       "Только чтение",
	Saving:         func(percent int) string { return fmt.Sprintf("Сохранение... %d%%", percent) },
	Saved:          "Сохранено",
	SaveFailed:     func(err string) string { return "Не удаётся сохранить файл:\n\n" + err },

	SaveChangesTitle: "Сохранение изменений",
	SaveChangesAsk:   func(name string) string { return "Сохранить изменения в " + name + "?" },
	Save:             "Сохранить",
	DontSave:         "Не сохранять",

	GoToTitle: "Переход к смещению",
	GoToLabel: "Смещение:",
	GoToBad:   "За концом документа",

	RecoverTitle: "Восстановление правок",
	RecoverAsk: func(name, when string) string {
		return "AltHex был закрыт с несохранёнными правками в " + name + " (" + when + ").\n\nВосстановить их?"
	},
	RecoverFailed: func(err string) string { return "Не удаётся восстановить правки:\n\n" + err },
	FileChangedAsk: func(name string) string {
		return name + " изменён другой программой.\n\nПерезагрузить его? Правки, сделанные здесь, будут потеряны."
	},

	BackupOnSave:   "Сохранять резервную копию (.bak)",
	BookmarkName:   func(n int) string { return fmt.Sprintf("Закладка %d", n) },
	AlwaysOnTop:    "Поверх других окон",
	Language:       "Язык:",
	LanguageSystem: "Как в системе",
	Theme:          "Тема:",
	ThemeDark:      "Тёмная",
	ThemeLight:     "Светлая",

	AboutTitle:   func(name string) string { return "О программе " + name },
	Version:      "Версия",
	Author:       "Автор:",
	License:      "Лицензия:",
	VisitWebsite: "Открыть сайт",
	Close:        "Закрыть",

	Install:   "Установить",
	Update:    "Обновить",
	Uninstall: "Удалить",
	InstallAsk: func(dir string) string {
		return "AltHex будет скопирован в " + dir + " и добавлен в меню «Пуск», на рабочий стол и в список установленных приложений. Установленная копия обновится. Настройки останутся как есть.\n\nЗатем AltHex запустится оттуда."
	},
	InstallFailed: func(err string) string { return "Не удаётся установить AltHex:\n\n" + err },
	Installed:     "AltHex установлен",
	UninstallAsk: func(dir string) string {
		return "Удалить AltHex с этого компьютера?\n\nНастройки останутся в " + dir + "."
	},
	UninstallRunning: "AltHex запущен. Закройте его и попробуйте снова.",
	Uninstalled:      "AltHex удалён.",
}

var catalog = i18n.NewCatalog(en, map[string]Strings{
	"ru": ru, "pl": pl, "sr": sr, "de": de, "fr": fr, "es": es,
	"it": it, "pt": pt, "zh": zh, "ja": ja, "ko": ko,
})

// T returns the texts in the language of the application
func T() *Strings {
	return catalog.Get(ui.Language())
}

// SetLanguage switches the application to the language of the settings, "" - the system's
func SetLanguage(lang string) {
	if lang == "" {
		lang = ui.SystemLanguage()
	}
	ui.SetLanguage(lang)
}
