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
	MenuFile            string
	MenuOpen            string
	MenuSave            string
	MenuSaveAs          string
	MenuClose           string
	MenuRecent          string
	MenuExit            string
	MenuEdit            string
	MenuUndo            string
	MenuRedo            string
	MenuCut             string
	MenuCopy            string
	MenuPaste           string
	MenuSelectAll       string
	MenuInsertMode      string
	MenuReadOnly        string
	MenuView            string
	MenuGoTo            string
	MenuZoomIn          string
	MenuZoomOut         string
	MenuZoomReset       string
	MenuNew             string
	MenuNewMore         string
	MenuNewSize         string
	MenuNewClipboard    string
	MenuCloseAll        string
	MenuNavigate        string
	MenuBack            string
	MenuForward         string
	MenuToggleBookmark  string
	MenuNextBookmark    string
	MenuPrevBookmark    string
	MenuExportBookmarks string
	MenuImportBookmarks string
	MenuNextTab         string
	MenuPrevTab         string
	MenuBytesPerRow     string
	MenuFitWidth        string
	MenuGroup           string
	MenuMode            string
	MenuOffsets         string
	MenuRelativeOffsets string
	MenuSetMark         string
	MenuEncoding        string
	MenuSplit           string
	MenuSyncScroll      string
	ModeHex             string
	ModeBin             string
	ModeOct             string
	ModeDec             string
	SizeLabel           string
	MenuPanels          string
	PageInspector       string
	BigEndian           string
	ColType             string
	ColValue            string
	PageBookmarks       string
	PageHashes          string
	ColName             string
	ColOffset           string
	ColLength           string
	ColComment          string
	ColAlgorithm        string
	Remove              string
	Range               string
	RangeSelection      string
	RangeDocument       string
	CustomCRC           string
	Calculate           string
	Cancel              string
	StatsLine           func(off, n int64, entropy, seconds float64) string
	Working             func(percent int) string
	BytesLabel          string
	BytesBad            string
	BitsLabel           string
	BitsBad             string
	MenuFill            string
	OpAdd               string
	OpSub               string
	OpShl               string
	OpShr               string
	OpRol               string
	OpRor               string
	OpSwap              func(width int) string
	OpReverse           string
	CopyHex             string
	CopyHexPlain        string
	CopyEscaped         string
	ImportArray         string
	MenuCopyAs          string
	MenuBitOps          string
	MenuImport          string
	MenuExport          string
	ImportedAt          func(base uint64) string
	AddressLabel        string
	FillKind            string
	FillPattern         string
	FillRandom          string
	FillIncrement       string
	FillValue           string
	FillStart           string
	FillStep            string
	FillWidth           string
	FillInsert          string
	MenuPalette         string
	ColCommand          string
	ColShortcut         string
	PageSearch          string
	MenuSearch          string
	MenuFind            string
	MenuReplace         string
	FindHex             string
	FindText            string
	ReplaceWith         string
	IgnoreCase          string
	AllDocuments        string
	FindNext            string
	FindPrev            string
	FindAll             string
	Replace             string
	ReplaceAll          string
	Strings             string
	Export              string
	ColDocument         string
	ColFound            string
	NothingToFind       string
	NotFound            string
	Wrapped             string
	ClearMarks          string
	FoundCount          func(n int, more bool, seconds float64) string
	ReplacedCount       func(n int) string
	MenuCompareMenu     string
	MenuCompare         string
	MenuCompareShifts   string
	MenuNextDiff        string
	MenuPrevDiff        string
	MenuCompareReport   string
	MenuMakePatch       string
	MenuApplyPatch      string
	CompareSaveFirst    string
	NoMoreDiffs         string
	PatchNoFile         string
	PatchSaved          string
	PatchUnknown        string
	Comparing           func(percent int) string
	CompareSummary      func(ranges int, changed, inserted, deleted int64, seconds float64) string
	DiffStatus          func(cur, total int) string
	ColorRules          string
	ColorRulesHint      string
	PageStructure       string
	PageConsole         string
	MenuApplyTemplate   string
	Apply               string
	ColSize             string
	RunFunction         string
	AutoTemplate        string
	NoTemplate          string
	NoTemplateApplied   string
	ChecksumPrefix      string
	Evaluate            string
	Clear               string
	ConsoleHint         string
	NoDocument          string
	MoreElements        func(n int64) string
	TemplateError       func(name, err string) string
	TemplateWrites      func(name string) string
	TemplateRunning     func(name string) string
	TemplateDone        func(name string, fields int, seconds float64) string
	MenuOptions         string
	MenuSettings        string
	MenuHelp            string
	MenuDocs            string
	MenuAbout           string

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
	"ru": ru,
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
