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
	MenuFile       string
	MenuOpen       string
	MenuSave       string
	MenuSaveAs     string
	MenuClose      string
	MenuRecent     string
	MenuExit       string
	MenuEdit       string
	MenuUndo       string
	MenuRedo       string
	MenuCut        string
	MenuCopy       string
	MenuPaste      string
	MenuSelectAll  string
	MenuInsertMode string
	MenuReadOnly   string
	MenuView       string
	MenuGoTo       string
	MenuZoomIn     string
	MenuZoomOut    string
	MenuZoomReset  string
	MenuOptions    string
	MenuSettings   string
	MenuHelp       string
	MenuDocs       string
	MenuAbout      string

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

	MenuFile:       "File",
	MenuOpen:       "Open...",
	MenuSave:       "Save",
	MenuSaveAs:     "Save as...",
	MenuClose:      "Close",
	MenuRecent:     "Recent files",
	MenuExit:       "Exit",
	MenuEdit:       "Edit",
	MenuUndo:       "Undo",
	MenuRedo:       "Redo",
	MenuCut:        "Cut",
	MenuCopy:       "Copy",
	MenuPaste:      "Paste",
	MenuSelectAll:  "Select all",
	MenuInsertMode: "Insert mode (Ins)",
	MenuReadOnly:   "Read-only",
	MenuView:       "View",
	MenuGoTo:       "Go to offset...",
	MenuZoomIn:     "Zoom in",
	MenuZoomOut:    "Zoom out",
	MenuZoomReset:  "Normal size",
	MenuOptions:    "Options",
	MenuSettings:   "Settings...",
	MenuHelp:       "Help",
	MenuDocs:       "Documentation",
	MenuAbout:      "About",

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
	GoToBad:   "An offset like 0x1A2B, 1A2Bh or 6699",

	RecoverTitle: "Recover edits",
	RecoverAsk: func(name, when string) string {
		return "AltHex was closed with unsaved edits of " + name + " (" + when + ").\n\nRecover them?"
	},
	RecoverFailed: func(err string) string { return "Cannot recover the edits:\n\n" + err },
	FileChangedAsk: func(name string) string {
		return name + " was changed by another program.\n\nReload it? The edits made here will be lost."
	},

	BackupOnSave:   "Keep a backup copy (.bak) when saving",
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

	MenuFile:       "Файл",
	MenuOpen:       "Открыть...",
	MenuSave:       "Сохранить",
	MenuSaveAs:     "Сохранить как...",
	MenuClose:      "Закрыть",
	MenuRecent:     "Недавние файлы",
	MenuExit:       "Выход",
	MenuEdit:       "Правка",
	MenuUndo:       "Отменить",
	MenuRedo:       "Повторить",
	MenuCut:        "Вырезать",
	MenuCopy:       "Копировать",
	MenuPaste:      "Вставить",
	MenuSelectAll:  "Выделить всё",
	MenuInsertMode: "Режим вставки (Ins)",
	MenuReadOnly:   "Только чтение",
	MenuView:       "Вид",
	MenuGoTo:       "Перейти к смещению...",
	MenuZoomIn:     "Увеличить",
	MenuZoomOut:    "Уменьшить",
	MenuZoomReset:  "Обычный размер",
	MenuOptions:    "Параметры",
	MenuSettings:   "Настройки...",
	MenuHelp:       "Справка",
	MenuDocs:       "Документация",
	MenuAbout:      "О программе",

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
	GoToBad:   "Смещение вида 0x1A2B, 1A2Bh или 6699",

	RecoverTitle: "Восстановление правок",
	RecoverAsk: func(name, when string) string {
		return "AltHex был закрыт с несохранёнными правками в " + name + " (" + when + ").\n\nВосстановить их?"
	},
	RecoverFailed: func(err string) string { return "Не удаётся восстановить правки:\n\n" + err },
	FileChangedAsk: func(name string) string {
		return name + " изменён другой программой.\n\nПерезагрузить его? Правки, сделанные здесь, будут потеряны."
	},

	BackupOnSave:   "Сохранять резервную копию (.bak)",
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
